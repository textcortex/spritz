package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	spritzv1 "spritz.sh/operator/api/v1"
)

type acpBootstrapResponse struct {
	Conversation       *spritzv1.SpritzConversation    `json:"conversation"`
	EffectiveSessionID string                          `json:"effectiveSessionId,omitempty"`
	BindingState       string                          `json:"bindingState,omitempty"`
	Loaded             bool                            `json:"loaded,omitempty"`
	Replaced           bool                            `json:"replaced,omitempty"`
	ReplayMessageCount int32                           `json:"replayMessageCount,omitempty"`
	AgentInfo          *spritzv1.SpritzACPAgentInfo    `json:"agentInfo,omitempty"`
	Capabilities       *spritzv1.SpritzACPCapabilities `json:"capabilities,omitempty"`
}

type acpBootstrapClientInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type acpBootstrapPromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type acpBootstrapMCPTransportCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

type acpBootstrapAgentCapabilities struct {
	LoadSession        bool                                  `json:"loadSession,omitempty"`
	PromptCapabilities *acpBootstrapPromptCapabilities       `json:"promptCapabilities,omitempty"`
	MCP                *acpBootstrapMCPTransportCapabilities `json:"mcp,omitempty"`
}

type acpBootstrapInitializeRequest struct {
	ProtocolVersion    int                    `json:"protocolVersion"`
	ClientCapabilities map[string]any         `json:"clientCapabilities,omitempty"`
	ClientInfo         acpBootstrapClientInfo `json:"clientInfo,omitempty"`
}

type acpBootstrapInitializeResult struct {
	ProtocolVersion   int32                         `json:"protocolVersion"`
	AgentCapabilities acpBootstrapAgentCapabilities `json:"agentCapabilities,omitempty"`
	AgentInfo         acpBootstrapClientInfo        `json:"agentInfo,omitempty"`
}

type acpBootstrapJSONRPCMessage struct {
	JSONRPC string                    `json:"jsonrpc,omitempty"`
	ID      any                       `json:"id,omitempty"`
	Method  string                    `json:"method,omitempty"`
	Params  json.RawMessage           `json:"params,omitempty"`
	Result  json.RawMessage           `json:"result,omitempty"`
	Error   *acpBootstrapJSONRPCError `json:"error,omitempty"`
}

type acpBootstrapJSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type acpBootstrapRPCError struct {
	Code    int
	Message string
	Details string
}

func (e *acpBootstrapRPCError) Error() string {
	if strings.TrimSpace(e.Details) != "" {
		return strings.TrimSpace(e.Details)
	}
	return strings.TrimSpace(e.Message)
}

func (e *acpBootstrapRPCError) missingSession() bool {
	if e == nil {
		return false
	}
	if e.Code == -32002 {
		return true
	}
	message := strings.ToLower(e.Error())
	return strings.Contains(message, "session") && strings.Contains(message, "not found")
}

type acpBootstrapInstanceClient struct {
	conn       *websocket.Conn
	nextID     int64
	writeMu    sync.Mutex
	readerOnce sync.Once
	readCh     chan *acpBootstrapJSONRPCMessage
	readErrCh  chan error
}

func (c *acpBootstrapInstanceClient) close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *acpBootstrapInstanceClient) initialize(ctx context.Context, clientInfo acpBootstrapClientInfo, clientCapabilities map[string]any) (*acpBootstrapInitializeResult, error) {
	result := &acpBootstrapInitializeResult{}
	if err := c.request(ctx, "initialize", acpBootstrapInitializeRequest{
		ProtocolVersion:    1,
		ClientCapabilities: clientCapabilities,
		ClientInfo:         clientInfo,
	}, result, nil); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *acpBootstrapInstanceClient) loadSession(ctx context.Context, sessionID, cwd string) (int32, error) {
	var replayCount int32
	err := c.request(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        cwd,
		"mcpServers": []any{},
	}, &map[string]any{}, func(message *acpBootstrapJSONRPCMessage) {
		if isACPTranscriptReplayUpdate(message) {
			replayCount++
		}
	})
	return replayCount, err
}

func (c *acpBootstrapInstanceClient) newSession(ctx context.Context, cwd string) (string, error) {
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.request(ctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	}, &result, nil); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.SessionID), nil
}

func (c *acpBootstrapInstanceClient) request(ctx context.Context, method string, params any, target any, onNotification func(*acpBootstrapJSONRPCMessage)) error {
	c.nextID++
	requestID := fmt.Sprintf("bootstrap-%d", c.nextID)
	if err := c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}); err != nil {
		return err
	}

	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return err
		}
		if message.Method != "" && message.ID == nil {
			if onNotification != nil {
				onNotification(message)
			}
			continue
		}
		if fmt.Sprint(message.ID) != requestID {
			continue
		}
		if message.Error != nil {
			return newACPBootstrapRPCError(message.Error)
		}
		if target == nil || len(message.Result) == 0 {
			return nil
		}
		return json.Unmarshal(message.Result, target)
	}
}

func (c *acpBootstrapInstanceClient) writeJSON(ctx context.Context, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	return c.conn.WriteJSON(payload)
}

func (c *acpBootstrapInstanceClient) notify(ctx context.Context, method string, params any) error {
	return c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *acpBootstrapInstanceClient) cancelPrompt(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return c.notify(ctx, "session/cancel", map[string]any{
		"sessionId": sessionID,
	})
}

func (c *acpBootstrapInstanceClient) respondError(ctx context.Context, id any, code int, message string) error {
	if id == nil {
		return nil
	}
	return c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *acpBootstrapInstanceClient) handleServerRequest(ctx context.Context, message *acpBootstrapJSONRPCMessage, unsupportedMessage string) (bool, error) {
	if message == nil || strings.TrimSpace(message.Method) == "" || message.ID == nil {
		return false, nil
	}
	if message.Method == "session/request_permission" {
		return true, c.respondError(ctx, message.ID, -32000, "Permission requests are not supported by internal debug chat.")
	}
	return true, c.respondError(ctx, message.ID, -32601, unsupportedMessage)
}

func (c *acpBootstrapInstanceClient) readMessage(ctx context.Context) (*acpBootstrapJSONRPCMessage, error) {
	c.startReader()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case message, ok := <-c.readCh:
		if !ok {
			if err, ok := <-c.readErrCh; ok && err != nil {
				return nil, err
			}
			return nil, websocket.ErrCloseSent
		}
		return message, nil
	case err, ok := <-c.readErrCh:
		if ok && err != nil {
			return nil, err
		}
		return nil, websocket.ErrCloseSent
	}
}

func (c *acpBootstrapInstanceClient) startReader() {
	c.readerOnce.Do(func() {
		c.readCh = make(chan *acpBootstrapJSONRPCMessage, 32)
		c.readErrCh = make(chan error, 1)
		go func() {
			defer close(c.readCh)
			defer close(c.readErrCh)
			for {
				_, payload, err := c.conn.ReadMessage()
				if err != nil {
					c.readErrCh <- err
					return
				}
				message := &acpBootstrapJSONRPCMessage{}
				if err := json.Unmarshal(payload, message); err != nil {
					c.readErrCh <- err
					return
				}
				c.readCh <- message
			}
		}()
	})
}

func newACPBootstrapRPCError(payload *acpBootstrapJSONRPCError) *acpBootstrapRPCError {
	if payload == nil {
		return &acpBootstrapRPCError{}
	}
	errorValue := &acpBootstrapRPCError{
		Code:    payload.Code,
		Message: payload.Message,
	}
	if len(payload.Data) > 0 {
		var details struct {
			Details string `json:"details"`
		}
		if err := json.Unmarshal(payload.Data, &details); err == nil {
			errorValue.Details = details.Details
		}
	}
	return errorValue
}

func isACPTranscriptReplayUpdate(message *acpBootstrapJSONRPCMessage) bool {
	if message == nil || message.Method != "session/update" || len(message.Params) == 0 {
		return false
	}
	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
		} `json:"update"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return false
	}
	switch params.Update.SessionUpdate {
	case "user_message_chunk", "agent_message_chunk", "tool_call", "tool_call_update":
		return true
	default:
		return false
	}
}

func normalizeBootstrapAgentInfo(result *acpBootstrapInitializeResult) *spritzv1.SpritzACPAgentInfo {
	if result == nil {
		return nil
	}
	return &spritzv1.SpritzACPAgentInfo{
		Name:    strings.TrimSpace(result.AgentInfo.Name),
		Title:   strings.TrimSpace(result.AgentInfo.Title),
		Version: strings.TrimSpace(result.AgentInfo.Version),
	}
}

func normalizeBootstrapCapabilities(result *acpBootstrapInitializeResult) *spritzv1.SpritzACPCapabilities {
	if result == nil {
		return nil
	}
	capabilities := &spritzv1.SpritzACPCapabilities{
		LoadSession: result.AgentCapabilities.LoadSession,
	}
	if result.AgentCapabilities.PromptCapabilities != nil {
		capabilities.Prompt = &spritzv1.SpritzACPPromptCapabilities{
			Image:           result.AgentCapabilities.PromptCapabilities.Image,
			Audio:           result.AgentCapabilities.PromptCapabilities.Audio,
			EmbeddedContext: result.AgentCapabilities.PromptCapabilities.EmbeddedContext,
		}
	}
	if result.AgentCapabilities.MCP != nil {
		capabilities.MCP = &spritzv1.SpritzACPMCPTransportCapabilities{
			HTTP: result.AgentCapabilities.MCP.HTTP,
			SSE:  result.AgentCapabilities.MCP.SSE,
		}
	}
	return capabilities
}

func (s *server) bootstrapACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}
	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}
	conversation, err := s.getAuthorizedConversation(c.Request().Context(), principal, namespace, c.Param("id"))
	if err != nil {
		return s.writeACPConversationError(c, err)
	}
	spritz, err := s.getAuthorizedACPReadySpritz(c.Request().Context(), principal, namespace, conversation.Spec.SpritzName)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}
	response, err := s.bootstrapACPConversationBinding(c.Request().Context(), conversation, spritz)
	if err != nil {
		var rpcErr *acpBootstrapRPCError
		switch {
		case errors.As(err, &rpcErr):
			return writeError(c, http.StatusBadGateway, rpcErr.Error())
		default:
			return writeError(c, http.StatusBadGateway, err.Error())
		}
	}
	return writeJSON(c, http.StatusOK, response)
}

func (s *server) bootstrapACPConversationBinding(ctx context.Context, conversation *spritzv1.SpritzConversation, spritz *spritzv1.Spritz) (*acpBootstrapResponse, error) {
	dialCtx, cancel := context.WithTimeout(ctx, s.acp.bootstrapDialTimeout)
	defer cancel()

	instanceConn, _, err := websocket.DefaultDialer.DialContext(dialCtx, s.acpInstanceURL(spritz.Namespace, spritz.Name), nil)
	if err != nil {
		s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, "", err)
		return nil, err
	}
	client := &acpBootstrapInstanceClient{conn: instanceConn}
	defer func() {
		_ = client.close()
	}()

	initResult, err := client.initialize(ctx, s.acp.clientInfo, s.acp.clientCapabilities)
	if err != nil {
		s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, "", err)
		return nil, err
	}

	return s.bootstrapACPConversationBindingWithClient(ctx, conversation, client, initResult)
}

func (s *server) bootstrapACPConversationBindingWithClient(ctx context.Context, conversation *spritzv1.SpritzConversation, client *acpBootstrapInstanceClient, initResult *acpBootstrapInitializeResult) (*acpBootstrapResponse, error) {
	if !initResult.AgentCapabilities.LoadSession {
		err := errors.New("agent does not support session/load")
		s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, "", err)
		return nil, err
	}

	agentInfo := normalizeBootstrapAgentInfo(initResult)
	capabilities := normalizeBootstrapCapabilities(initResult)
	effectiveSessionID := strings.TrimSpace(conversation.Spec.SessionID)
	previousSessionID := ""
	bindingState := "active"
	replaced := false
	loaded := false
	var replayMessageCount int32
	var err error

	if effectiveSessionID != "" {
		replayMessageCount, err = client.loadSession(ctx, effectiveSessionID, normalizeConversationCWD(conversation.Spec.CWD))
		if err != nil {
			var rpcErr *acpBootstrapRPCError
			if errors.As(err, &rpcErr) && rpcErr.missingSession() {
				previousSessionID = effectiveSessionID
				effectiveSessionID, err = client.newSession(ctx, normalizeConversationCWD(conversation.Spec.CWD))
				if err != nil {
					s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, previousSessionID, err)
					return nil, err
				}
				bindingState = "replaced"
				replaced = true
			} else {
				s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, "", err)
				return nil, err
			}
		} else {
			loaded = true
		}
	} else {
		effectiveSessionID, err = client.newSession(ctx, normalizeConversationCWD(conversation.Spec.CWD))
		if err != nil {
			s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, "", err)
			return nil, err
		}
	}

	if strings.TrimSpace(effectiveSessionID) == "" {
		err = errors.New("acp bootstrap returned empty session id")
		s.recordConversationBindingError(ctx, conversation.Namespace, conversation.Name, "", err)
		return nil, err
	}

	updatedConversation, err := s.updateConversationBinding(ctx, conversation.Namespace, conversation.Name, func(current *spritzv1.SpritzConversation) {
		now := metav1.Now()
		current.Spec.SessionID = effectiveSessionID
		current.Spec.AgentInfo = agentInfo
		current.Spec.Capabilities = capabilities
		current.Status.BoundSessionID = effectiveSessionID
		current.Status.BindingState = bindingState
		current.Status.PreviousSessionID = previousSessionID
		current.Status.LastBoundAt = &now
		current.Status.LastReplayMessageCount = replayMessageCount
		if loaded {
			current.Status.LastReplayAt = &now
		} else {
			current.Status.LastReplayAt = nil
		}
		current.Status.LastError = ""
		current.Status.UpdatedAt = &now
	})
	if err != nil {
		return nil, err
	}

	return &acpBootstrapResponse{
		Conversation:       updatedConversation,
		EffectiveSessionID: effectiveSessionID,
		BindingState:       bindingState,
		Loaded:             loaded,
		Replaced:           replaced,
		ReplayMessageCount: replayMessageCount,
		AgentInfo:          agentInfo,
		Capabilities:       capabilities,
	}, nil
}

func (s *server) recordConversationBindingError(ctx context.Context, namespace, name, previousSessionID string, cause error) {
	if cause == nil {
		return
	}
	_, _ = s.updateConversationBinding(ctx, namespace, name, func(current *spritzv1.SpritzConversation) {
		now := metav1.Now()
		current.Status.BindingState = "error"
		current.Status.PreviousSessionID = previousSessionID
		current.Status.LastError = cause.Error()
		current.Status.UpdatedAt = &now
	})
}

func (s *server) updateConversationBinding(ctx context.Context, namespace, name string, mutate func(*spritzv1.SpritzConversation)) (*spritzv1.SpritzConversation, error) {
	var updated *spritzv1.SpritzConversation
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &spritzv1.SpritzConversation{}
		if err := s.client.Get(ctx, clientKey(namespace, name), current); err != nil {
			return err
		}
		beforeSpec := current.Spec
		beforeStatus := current.Status
		mutate(current)
		specChanged := !apiequality.Semantic.DeepEqual(beforeSpec, current.Spec)
		statusChanged := !apiequality.Semantic.DeepEqual(beforeStatus, current.Status)
		desiredStatus := current.Status
		if specChanged {
			if err := s.client.Update(ctx, current); err != nil {
				return err
			}
		}
		if statusChanged {
			statusTarget := current
			if specChanged {
				statusTarget = &spritzv1.SpritzConversation{}
				if err := s.client.Get(ctx, clientKey(namespace, name), statusTarget); err != nil {
					return err
				}
				statusTarget.Status = desiredStatus
			}
			if err := s.client.Status().Update(ctx, statusTarget); err != nil {
				return err
			}
			current = statusTarget
		}
		updated = current.DeepCopy()
		return nil
	})
	return updated, err
}
