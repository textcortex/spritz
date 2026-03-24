package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	spritzv1 "spritz.sh/operator/api/v1"
)

type internalDebugChatSendRequest struct {
	Target  internalDebugChatTarget `json:"target"`
	Reason  string                  `json:"reason,omitempty"`
	Message string                  `json:"message"`
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
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := body.Target.validate(); err != nil {
		s.auditInternalDebugChatFailure(principal.ID, body.Target, strings.TrimSpace(body.Reason), body.Message, "invalid_target", err)
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if strings.TrimSpace(body.Message) == "" {
		s.auditInternalDebugChatFailure(principal.ID, body.Target, strings.TrimSpace(body.Reason), body.Message, "invalid_message", errors.New("message is required"))
		return writeError(c, http.StatusBadRequest, "message is required")
	}
	message := body.Message
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "spz chat send"
	}

	conversation, spritz, createdConversation, err := s.resolveInternalDebugChatTarget(c.Request().Context(), principal, body.Target)
	if err != nil {
		s.auditInternalDebugChatFailure(principal.ID, body.Target, reason, message, "target_error", err)
		return s.writeInternalDebugChatTargetError(c, err)
	}

	bootstrap, promptResult, err := s.runInternalDebugChat(c.Request().Context(), conversation, spritz, message)
	if err != nil {
		s.cleanupInternalDebugConversation(c.Request().Context(), conversation, createdConversation)
		outcome := "prompt_error"
		if promptResult == nil {
			outcome = "bootstrap_error"
		}
		s.auditInternalDebugChatFailure(principal.ID, body.Target, reason, message, outcome, err)
		return s.writeInternalDebugChatRuntimeError(c, err)
	}

	s.auditInternalDebugChat(principal.ID, conversation, reason, message, promptResult)

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

func (s *server) resolveInternalDebugChatTarget(ctx context.Context, principal principal, target internalDebugChatTarget) (*spritzv1.SpritzConversation, *spritzv1.Spritz, bool, error) {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(target.Namespace))
	if err != nil {
		return nil, nil, false, errForbidden
	}
	if namespace == "" {
		namespace = "default"
	}

	if conversationID := strings.TrimSpace(target.ConversationID); conversationID != "" {
		conversation, err := s.getInternalDebugConversation(ctx, principal, namespace, conversationID)
		if err != nil {
			return nil, nil, false, err
		}
		spritz, err := s.getInternalDebugACPReadySpritz(ctx, principal, namespace, conversation.Spec.SpritzName)
		if err != nil {
			return nil, nil, false, err
		}
		return conversation, spritz, false, nil
	}

	spritz, err := s.getInternalDebugACPReadySpritz(ctx, principal, namespace, strings.TrimSpace(target.SpritzName))
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

func (s *server) getInternalDebugConversation(ctx context.Context, principal principal, namespace, conversationID string) (*spritzv1.SpritzConversation, error) {
	conversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(ctx, clientKey(namespace, conversationID), conversation); err != nil {
		return nil, err
	}
	if err := authorizeExactOwnerAccess(principal, conversation.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return nil, err
	}
	return conversation, nil
}

func (s *server) getInternalDebugACPReadySpritz(ctx context.Context, principal principal, namespace, name string) (*spritzv1.Spritz, error) {
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, name), spritz); err != nil {
		return nil, err
	}
	if err := authorizeExactOwnerAccess(principal, spritz.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return nil, err
	}
	if !spritzSupportsACPConversations(spritz) {
		return nil, errACPUnavailable
	}
	return spritz, nil
}

func (s *server) cleanupInternalDebugConversation(ctx context.Context, conversation *spritzv1.SpritzConversation, created bool) {
	if !created || conversation == nil {
		return
	}
	cleanupCtx := ctx
	cleanupCancel := func() {}
	if cleanupCtx == nil || cleanupCtx.Err() != nil {
		cleanupCtx, cleanupCancel = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cleanupCancel()
	if err := s.client.Delete(cleanupCtx, conversation); err != nil && !apierrors.IsNotFound(err) {
		log.Printf(
			"spritz internal-debug-chat cleanup_failed namespace=%s conversation_id=%s err=%v",
			conversation.Namespace,
			conversation.Name,
			err,
		)
	}
}

func (s *server) runInternalDebugChat(ctx context.Context, conversation *spritzv1.SpritzConversation, spritz *spritzv1.Spritz, message string) (*acpBootstrapResponse, *acpPromptResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.acp.promptTimeout)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(runCtx, s.acp.bootstrapDialTimeout)
	defer dialCancel()

	instanceConn, _, err := websocket.DefaultDialer.DialContext(dialCtx, s.acpInstanceURL(spritz.Namespace, spritz.Name), nil)
	if err != nil {
		s.recordConversationBindingError(runCtx, conversation.Namespace, conversation.Name, "", err)
		return nil, nil, err
	}
	client := &acpBootstrapInstanceClient{conn: instanceConn}
	defer func() {
		_ = client.close()
	}()

	initResult, err := client.initialize(runCtx, s.acp.clientInfo, s.acp.clientCapabilities)
	if err != nil {
		s.recordConversationBindingError(runCtx, conversation.Namespace, conversation.Name, "", err)
		return nil, nil, err
	}

	bootstrap, err := s.bootstrapACPConversationBindingWithClient(runCtx, conversation, client, initResult)
	if err != nil {
		return nil, nil, err
	}

	if bootstrap.Loaded {
		ignoredReplayUpdates := make([]map[string]any, 0, 8)
		if err := client.drainSessionUpdates(runCtx, s.acp.promptSettleTimeout, &ignoredReplayUpdates); err != nil {
			return bootstrap, nil, err
		}
	}

	cancelWatcherDone := make(chan struct{})
	defer close(cancelWatcherDone)
	var cancelOnce sync.Once
	sendCancel := func() {
		cancelOnce.Do(func() {
			cancelCtx, cancelPrompt := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.cancelInternalDebugChatPrompt(cancelCtx, spritz, bootstrap.EffectiveSessionID)
			cancelPrompt()
		})
	}
	go func() {
		select {
		case <-runCtx.Done():
			select {
			case <-cancelWatcherDone:
				return
			default:
			}
			sendCancel()
		case <-cancelWatcherDone:
		}
	}()

	result, err := client.prompt(runCtx, bootstrap.EffectiveSessionID, message, s.acp.promptSettleTimeout)
	if err != nil && runCtx.Err() != nil {
		sendCancel()
	}
	return bootstrap, result, err
}

func (s *server) cancelInternalDebugChatPrompt(ctx context.Context, spritz *spritzv1.Spritz, sessionID string) error {
	if spritz == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, s.acp.bootstrapDialTimeout)
	defer cancel()

	instanceConn, _, err := websocket.DefaultDialer.DialContext(dialCtx, s.acpInstanceURL(spritz.Namespace, spritz.Name), nil)
	if err != nil {
		return err
	}
	client := &acpBootstrapInstanceClient{conn: instanceConn}
	defer func() {
		_ = client.close()
	}()
	if _, err := client.initialize(ctx, s.acp.clientInfo, s.acp.clientCapabilities); err != nil {
		return err
	}
	return client.cancelPrompt(ctx, sessionID)
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

func (s *server) auditInternalDebugChatFailure(actorID string, target internalDebugChatTarget, reason, message, outcome string, cause error) {
	promptHash := sha256.Sum256([]byte(message))
	log.Printf(
		"spritz internal-debug-chat actor_id=%s namespace=%s spritz_name=%s conversation_id=%s reason=%q outcome=%s prompt_sha256=%s err=%v",
		strings.TrimSpace(actorID),
		strings.TrimSpace(target.Namespace),
		strings.TrimSpace(target.SpritzName),
		strings.TrimSpace(target.ConversationID),
		strings.TrimSpace(reason),
		strings.TrimSpace(outcome),
		hex.EncodeToString(promptHash[:]),
		cause,
	)
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
