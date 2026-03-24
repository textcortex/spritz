package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	spritzv1 "spritz.sh/operator/api/v1"
)

type internalDebugChatSendRequest struct {
	Principal internalDebugChatPrincipal `json:"principal"`
	Target    internalDebugChatTarget    `json:"target"`
	Reason    string                     `json:"reason,omitempty"`
	Message   string                     `json:"message"`
}

type internalDebugChatPrincipal struct {
	ID string `json:"id"`
}

type internalDebugChatTarget struct {
	Namespace      string `json:"namespace,omitempty"`
	SpritzName     string `json:"spritzName,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	Title          string `json:"title,omitempty"`
	CWD            string `json:"cwd,omitempty"`
}

type internalDebugChatSendResponse struct {
	Conversation        *spritzv1.SpritzConversation `json:"conversation"`
	EffectiveSessionID  string                       `json:"effectiveSessionId,omitempty"`
	BindingState        string                       `json:"bindingState,omitempty"`
	Loaded              bool                         `json:"loaded,omitempty"`
	Replaced            bool                         `json:"replaced,omitempty"`
	ReplayMessageCount  int32                        `json:"replayMessageCount,omitempty"`
	StopReason          string                       `json:"stopReason,omitempty"`
	AssistantText       string                       `json:"assistantText,omitempty"`
	Updates             []map[string]any             `json:"updates,omitempty"`
	CreatedConversation bool                         `json:"createdConversation,omitempty"`
}

func (p internalDebugChatPrincipal) normalize() (string, error) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return "", errors.New("principal.id is required")
	}
	return id, nil
}

func (t internalDebugChatTarget) validate() error {
	hasSpritz := strings.TrimSpace(t.SpritzName) != ""
	hasConversation := strings.TrimSpace(t.ConversationID) != ""
	switch {
	case hasSpritz == hasConversation:
		return errors.New("target must include exactly one of spritzName or conversationId")
	default:
		return nil
	}
}

func (s *server) sendInternalDebugChat(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}

	var body internalDebugChatSendRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	actorID, err := body.Principal.normalize()
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if err := body.Target.validate(); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		return writeError(c, http.StatusBadRequest, "message is required")
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "spz chat send"
	}

	conversation, spritz, createdConversation, err := s.resolveInternalDebugChatTarget(c.Request().Context(), actorID, body.Target)
	if err != nil {
		return s.writeInternalDebugChatTargetError(c, err)
	}

	bootstrap, err := s.bootstrapACPConversationBinding(c.Request().Context(), conversation, spritz)
	if err != nil {
		return s.writeInternalDebugChatRuntimeError(c, err)
	}

	promptResult, err := s.runInternalDebugChatPrompt(c.Request().Context(), spritz, bootstrap.EffectiveSessionID, conversation.Spec.CWD, message)
	if err != nil {
		return s.writeInternalDebugChatRuntimeError(c, err)
	}

	s.auditInternalDebugChat(actorID, conversation, reason, message, promptResult)

	return writeJSON(c, http.StatusOK, internalDebugChatSendResponse{
		Conversation:        bootstrap.Conversation,
		EffectiveSessionID:  bootstrap.EffectiveSessionID,
		BindingState:        bootstrap.BindingState,
		Loaded:              bootstrap.Loaded,
		Replaced:            bootstrap.Replaced,
		ReplayMessageCount:  bootstrap.ReplayMessageCount,
		StopReason:          promptResult.StopReason,
		AssistantText:       promptResult.AssistantText,
		Updates:             promptResult.Updates,
		CreatedConversation: createdConversation,
	})
}

func (s *server) resolveInternalDebugChatTarget(ctx context.Context, actorID string, target internalDebugChatTarget) (*spritzv1.SpritzConversation, *spritzv1.Spritz, bool, error) {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(target.Namespace))
	if err != nil {
		return nil, nil, false, errForbidden
	}
	if namespace == "" {
		namespace = "default"
	}

	if conversationID := strings.TrimSpace(target.ConversationID); conversationID != "" {
		conversation, err := s.getInternalDebugConversation(ctx, actorID, namespace, conversationID)
		if err != nil {
			return nil, nil, false, err
		}
		spritz, err := s.getInternalDebugACPReadySpritz(ctx, actorID, namespace, conversation.Spec.SpritzName)
		if err != nil {
			return nil, nil, false, err
		}
		return conversation, spritz, false, nil
	}

	spritz, err := s.getInternalDebugACPReadySpritz(ctx, actorID, namespace, strings.TrimSpace(target.SpritzName))
	if err != nil {
		return nil, nil, false, err
	}
	conversation, err := buildACPConversationResource(spritz, target.Title, target.CWD)
	if err != nil {
		return nil, nil, false, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.client.Create(ctx, conversation); err == nil {
			return conversation, spritz, true, nil
		} else if !apierrors.IsAlreadyExists(err) {
			return nil, nil, false, err
		}
		conversation.Name, err = newConversationName(spritz.Name)
		if err != nil {
			return nil, nil, false, err
		}
	}
	return nil, nil, false, errors.New("failed to allocate conversation id")
}

func (s *server) getInternalDebugConversation(ctx context.Context, actorID, namespace, conversationID string) (*spritzv1.SpritzConversation, error) {
	conversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(ctx, clientKey(namespace, conversationID), conversation); err != nil {
		return nil, err
	}
	if err := authorizeOwnerIDAccess(actorID, conversation.Spec.Owner.ID); err != nil {
		return nil, err
	}
	return conversation, nil
}

func (s *server) getInternalDebugACPReadySpritz(ctx context.Context, actorID, namespace, name string) (*spritzv1.Spritz, error) {
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, name), spritz); err != nil {
		return nil, err
	}
	if err := authorizeOwnerIDAccess(actorID, spritz.Spec.Owner.ID); err != nil {
		return nil, err
	}
	if !spritzSupportsACPConversations(spritz) {
		return nil, errACPUnavailable
	}
	return spritz, nil
}

func (s *server) runInternalDebugChatPrompt(ctx context.Context, spritz *spritzv1.Spritz, sessionID, cwd, message string) (*acpPromptResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.acp.promptTimeout)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(runCtx, s.acp.bootstrapDialTimeout)
	defer dialCancel()

	instanceConn, _, err := websocket.DefaultDialer.DialContext(dialCtx, s.acpInstanceURL(spritz.Namespace, spritz.Name), nil)
	if err != nil {
		return nil, err
	}
	client := &acpBootstrapInstanceClient{conn: instanceConn}
	defer func() {
		_ = client.close()
	}()

	if _, err := client.initialize(runCtx, s.acp.clientInfo, s.acp.clientCapabilities); err != nil {
		return nil, err
	}
	if _, err := client.loadSession(runCtx, sessionID, normalizeConversationCWD(cwd)); err != nil {
		return nil, err
	}
	return client.prompt(runCtx, sessionID, message, s.acp.promptSettleTimeout)
}

func (s *server) auditInternalDebugChat(actorID string, conversation *spritzv1.SpritzConversation, reason, message string, result *acpPromptResult) {
	if conversation == nil || result == nil {
		return
	}
	promptHash := sha256.Sum256([]byte(message))
	assistantHash := sha256.Sum256([]byte(result.AssistantText))
	log.Printf(
		"spritz internal-debug-chat actor_id=%s owner_id=%s namespace=%s conversation_id=%s spritz_name=%s reason=%q stop_reason=%s updates=%d prompt_sha256=%s response_sha256=%s",
		actorID,
		conversation.Spec.Owner.ID,
		conversation.Namespace,
		conversation.Name,
		conversation.Spec.SpritzName,
		reason,
		result.StopReason,
		len(result.Updates),
		hex.EncodeToString(promptHash[:]),
		hex.EncodeToString(assistantHash[:]),
	)
}

func authorizeOwnerIDAccess(actorID, ownerID string) error {
	if strings.TrimSpace(actorID) == "" {
		return errUnauthenticated
	}
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(actorID) != strings.TrimSpace(ownerID) {
		return errForbidden
	}
	return nil
}

func (s *server) writeInternalDebugChatTargetError(c echo.Context, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return writeError(c, http.StatusNotFound, "target not found")
	case errors.Is(err, errForbidden):
		return writeError(c, http.StatusForbidden, "forbidden")
	case errors.Is(err, errACPUnavailable):
		return writeError(c, http.StatusConflict, "acp unavailable")
	default:
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
}

func (s *server) writeInternalDebugChatRuntimeError(c echo.Context, err error) error {
	var rpcErr *acpBootstrapRPCError
	switch {
	case errors.As(err, &rpcErr):
		return writeError(c, http.StatusBadGateway, rpcErr.Error())
	default:
		return writeError(c, http.StatusBadGateway, err.Error())
	}
}
