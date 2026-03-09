package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	defaultACPCWD             = "/home/dev"
	defaultACPProbeTimeout    = 3 * time.Second
	defaultACPProbeCacheTTL   = 30 * time.Second
	acpConversationLabelKey   = "spritz.sh/acp-conversation"
	acpConversationLabelValue = "true"
)

type acpConfig struct {
	enabled        bool
	port           int32
	path           string
	probeTimeout   time.Duration
	probeCacheTTL  time.Duration
	allowedOrigins map[string]struct{}
	clientInfo     acpImplementationInfo
	workspaceURL   func(namespace, name string) string
}

type acpImplementationInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type acpInitializeRequest struct {
	ProtocolVersion    int                   `json:"protocolVersion"`
	ClientCapabilities map[string]any        `json:"clientCapabilities,omitempty"`
	ClientInfo         acpImplementationInfo `json:"clientInfo,omitempty"`
}

type acpPromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type acpMCPTransportCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

type acpAgentCapabilities struct {
	LoadSession        bool                         `json:"loadSession,omitempty"`
	PromptCapabilities *acpPromptCapabilities       `json:"promptCapabilities,omitempty"`
	MCP                *acpMCPTransportCapabilities `json:"mcp,omitempty"`
}

type acpInitializeResult struct {
	ProtocolVersion   int32                 `json:"protocolVersion"`
	AgentCapabilities acpAgentCapabilities  `json:"agentCapabilities,omitempty"`
	AgentInfo         acpImplementationInfo `json:"agentInfo,omitempty"`
	AuthMethods       []string              `json:"authMethods,omitempty"`
}

type acpJSONRPCMessage struct {
	JSONRPC string           `json:"jsonrpc,omitempty"`
	ID      any              `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acpJSONRPCError `json:"error,omitempty"`
}

type acpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type acpAgentResponse struct {
	Spritz       spritzv1.Spritz              `json:"spritz"`
	Conversation *spritzv1.SpritzConversation `json:"conversation,omitempty"`
}

type ensureACPConversationRequest struct {
	CWD string `json:"cwd,omitempty"`
}

type updateACPConversationRequest struct {
	Title     *string `json:"title,omitempty"`
	SessionID *string `json:"sessionId,omitempty"`
	CWD       *string `json:"cwd,omitempty"`
}

func newACPConfig() acpConfig {
	path := strings.TrimSpace(os.Getenv("SPRITZ_ACP_PATH"))
	if path == "" {
		path = spritzv1.DefaultACPPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return acpConfig{
		enabled:        parseBoolEnv("SPRITZ_ACP_ENABLED", true),
		port:           int32(parseIntEnv("SPRITZ_ACP_PORT", int(spritzv1.DefaultACPPort))),
		path:           path,
		probeTimeout:   parseDurationEnv("SPRITZ_ACP_PROBE_TIMEOUT", defaultACPProbeTimeout),
		probeCacheTTL:  parseDurationEnv("SPRITZ_ACP_PROBE_CACHE_TTL", defaultACPProbeCacheTTL),
		allowedOrigins: splitSet(os.Getenv("SPRITZ_ACP_ORIGINS")),
		clientInfo: acpImplementationInfo{
			Name:    envOrDefault("SPRITZ_ACP_CLIENT_NAME", "spritz-ui"),
			Title:   envOrDefault("SPRITZ_ACP_CLIENT_TITLE", "Spritz ACP UI"),
			Version: envOrDefault("SPRITZ_ACP_CLIENT_VERSION", "1.0.0"),
		},
	}
}

func (a acpConfig) allowOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if len(a.allowedOrigins) == 0 {
		if origin == "" {
			return false
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(parsed.Host, r.Host)
	}
	if origin == "" {
		return false
	}
	return hasSetValue(a.allowedOrigins, origin)
}

func (a acpConfig) shouldProbe(status *spritzv1.SpritzACPStatus) bool {
	if !a.enabled {
		return false
	}
	if status == nil || status.LastProbeAt == nil {
		return true
	}
	if status.State == "" || status.State == "probing" {
		return true
	}
	return time.Since(status.LastProbeAt.Time) >= a.probeCacheTTL
}

func (s *server) listACPAgents(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	namespace := s.requestNamespace(c)

	list := &spritzv1.SpritzList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if s.auth.enabled() && !principal.IsAdmin {
		opts = append(opts, client.MatchingLabels{ownerLabelKey: ownerLabelValue(principal.ID)})
	}
	if err := s.client.List(c.Request().Context(), list, opts...); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	filtered := make([]spritzv1.Spritz, 0, len(list.Items))
	for _, item := range list.Items {
		if s.auth.enabled() && !principal.IsAdmin && item.Spec.Owner.ID != principal.ID {
			continue
		}
		if item.Status.Phase != "Ready" {
			continue
		}
		resolved, err := s.ensureACPStatus(c.Request().Context(), &item, false)
		if err != nil {
			continue
		}
		if resolved.Status.ACP == nil || resolved.Status.ACP.State != "ready" {
			continue
		}
		filtered = append(filtered, *resolved)
	}

	conversations := map[string]*spritzv1.SpritzConversation{}
	if len(filtered) > 0 {
		conversationList := &spritzv1.SpritzConversationList{}
		conversationOpts := []client.ListOption{}
		if namespace != "" {
			conversationOpts = append(conversationOpts, client.InNamespace(namespace))
		}
		if s.auth.enabled() && !principal.IsAdmin {
			conversationOpts = append(conversationOpts, client.MatchingLabels{ownerLabelKey: ownerLabelValue(principal.ID)})
		}
		if err := s.client.List(c.Request().Context(), conversationList, conversationOpts...); err == nil {
			for i := range conversationList.Items {
				item := conversationList.Items[i]
				if s.auth.enabled() && !principal.IsAdmin && item.Spec.Owner.ID != principal.ID {
					continue
				}
				conversations[conversationKey(item.Namespace, item.Spec.SpritzName)] = item.DeepCopy()
			}
		}
	}

	records := make([]acpAgentResponse, 0, len(filtered))
	for _, item := range filtered {
		records = append(records, acpAgentResponse{
			Spritz:       item,
			Conversation: conversations[conversationKey(item.Namespace, item.Name)],
		})
	}
	sort.Slice(records, func(i, j int) bool {
		left := displayAgentName(&records[i].Spritz)
		right := displayAgentName(&records[j].Spritz)
		if left == right {
			return records[i].Spritz.Name < records[j].Spritz.Name
		}
		return left < right
	})

	return writeJSON(c, http.StatusOK, map[string]any{"items": records})
}

func (s *server) getACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusNotFound, "not found")
	}
	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}

	conversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(c.Request().Context(), clientKey(namespace, name), conversation); err != nil {
		return writeError(c, http.StatusNotFound, "conversation not found")
	}
	if s.auth.enabled() && !principal.IsAdmin && conversation.Spec.Owner.ID != principal.ID {
		return writeError(c, http.StatusForbidden, "forbidden")
	}
	return writeJSON(c, http.StatusOK, conversation)
}

func (s *server) ensureACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}
	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}

	body := ensureACPConversationRequest{}
	if c.Request().Body != nil && c.Request().ContentLength != 0 {
		if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
			return writeError(c, http.StatusBadRequest, "invalid json")
		}
	}

	spritz, err := s.getAuthorizedSpritz(c.Request().Context(), principal, namespace, name)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}
	spritz, err = s.ensureACPStatus(c.Request().Context(), spritz, true)
	if err != nil {
		return writeError(c, http.StatusConflict, "acp unavailable")
	}
	if spritz.Status.ACP == nil || spritz.Status.ACP.State != "ready" {
		return writeError(c, http.StatusConflict, "acp unavailable")
	}

	conversation, err := s.ensureConversationResource(c.Request().Context(), spritz, body.CWD)
	if err != nil {
		if apierrors.IsConflict(err) {
			return writeError(c, http.StatusConflict, err.Error())
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, conversation)
}

func (s *server) updateACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}
	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}

	var body updateACPConversationRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}

	spritz, err := s.getAuthorizedSpritz(c.Request().Context(), principal, namespace, name)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}

	conversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(c.Request().Context(), clientKey(namespace, name), conversation); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "conversation not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if s.auth.enabled() && !principal.IsAdmin && conversation.Spec.Owner.ID != principal.ID {
		return writeError(c, http.StatusForbidden, "forbidden")
	}
	if conversation.Spec.SpritzName != spritz.Name {
		return writeError(c, http.StatusConflict, "conversation does not match spritz")
	}

	changed := false
	if body.Title != nil && conversation.Spec.Title != strings.TrimSpace(*body.Title) {
		conversation.Spec.Title = strings.TrimSpace(*body.Title)
		changed = true
	}
	if body.SessionID != nil && conversation.Spec.SessionID != strings.TrimSpace(*body.SessionID) {
		conversation.Spec.SessionID = strings.TrimSpace(*body.SessionID)
		changed = true
	}
	if body.CWD != nil && conversation.Spec.CWD != normalizeConversationCWD(*body.CWD) {
		conversation.Spec.CWD = normalizeConversationCWD(*body.CWD)
		changed = true
	}
	if changed {
		if err := s.client.Update(c.Request().Context(), conversation); err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
	}
	return writeJSON(c, http.StatusOK, conversation)
}

func (s *server) openACPConnection(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}
	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}

	spritz, err := s.getAuthorizedSpritz(c.Request().Context(), principal, namespace, name)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}
	spritz, err = s.ensureACPStatus(c.Request().Context(), spritz, true)
	if err != nil {
		return writeError(c, http.StatusConflict, "acp unavailable")
	}
	if spritz.Status.ACP == nil || spritz.Status.ACP.State != "ready" {
		return writeError(c, http.StatusConflict, "acp unavailable")
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: s.acp.allowOrigin,
	}
	browserConn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = browserConn.Close()
	}()

	workspaceURL := s.acpWorkspaceURL(spritz.Namespace, spritz.Name)
	workspaceConn, _, err := websocket.DefaultDialer.DialContext(c.Request().Context(), workspaceURL, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = workspaceConn.Close()
	}()

	return proxyWebSockets(browserConn, workspaceConn)
}

func (s *server) ensureACPStatus(ctx context.Context, spritz *spritzv1.Spritz, force bool) (*spritzv1.Spritz, error) {
	if !s.acp.enabled {
		return spritz, errors.New("acp disabled")
	}
	if spritz == nil {
		return nil, errors.New("spritz required")
	}
	if spritz.Status.Phase != "Ready" {
		return spritz, errors.New("spritz not ready")
	}
	if !force && !s.acp.shouldProbe(spritz.Status.ACP) {
		return spritz, nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.acp.probeTimeout)
	defer cancel()

	status, err := s.probeACP(ctx, spritz.Namespace, spritz.Name)
	if persistErr := s.persistACPStatus(ctx, spritz.Namespace, spritz.Name, status); persistErr != nil && err == nil {
		err = persistErr
	}
	resolved := spritz.DeepCopy()
	resolved.Status.ACP = status
	if err != nil && status.State != "ready" {
		return resolved, err
	}
	return resolved, nil
}

func (s *server) probeACP(ctx context.Context, namespace, name string) (*spritzv1.SpritzACPStatus, error) {
	status := &spritzv1.SpritzACPStatus{
		State: "probing",
		Endpoint: &spritzv1.SpritzACPEndpoint{
			Port: s.acp.port,
			Path: s.acp.path,
		},
	}
	now := metav1.Now()
	status.LastProbeAt = &now

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.acpWorkspaceURL(namespace, name), nil)
	if err != nil {
		status.State = "unavailable"
		status.LastError = err.Error()
		return status, err
	}
	defer func() {
		_ = conn.Close()
	}()

	_ = conn.SetWriteDeadline(time.Now().Add(s.acp.probeTimeout))
	requestID := "spritz-initialize"
	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "initialize",
		"params": acpInitializeRequest{
			ProtocolVersion:    1,
			ClientCapabilities: map[string]any{},
			ClientInfo:         s.acp.clientInfo,
		},
	}); err != nil {
		status.State = "unavailable"
		status.LastError = err.Error()
		return status, err
	}

	_ = conn.SetReadDeadline(time.Now().Add(s.acp.probeTimeout))
	for {
		var message acpJSONRPCMessage
		if err := conn.ReadJSON(&message); err != nil {
			status.State = "unavailable"
			status.LastError = err.Error()
			return status, err
		}
		if fmt.Sprint(message.ID) != requestID {
			continue
		}
		if message.Error != nil {
			status.State = "error"
			status.LastError = message.Error.Message
			return status, errors.New(message.Error.Message)
		}
		var result acpInitializeResult
		if err := json.Unmarshal(message.Result, &result); err != nil {
			status.State = "error"
			status.LastError = err.Error()
			return status, err
		}
		status.State = "ready"
		status.ProtocolVersion = result.ProtocolVersion
		status.AgentInfo = &spritzv1.SpritzACPAgentInfo{
			Name:    result.AgentInfo.Name,
			Title:   result.AgentInfo.Title,
			Version: result.AgentInfo.Version,
		}
		status.Capabilities = &spritzv1.SpritzACPCapabilities{
			LoadSession: result.AgentCapabilities.LoadSession,
		}
		if result.AgentCapabilities.PromptCapabilities != nil {
			status.Capabilities.Prompt = &spritzv1.SpritzACPPromptCapabilities{
				Image:           result.AgentCapabilities.PromptCapabilities.Image,
				Audio:           result.AgentCapabilities.PromptCapabilities.Audio,
				EmbeddedContext: result.AgentCapabilities.PromptCapabilities.EmbeddedContext,
			}
		}
		if result.AgentCapabilities.MCP != nil {
			status.Capabilities.MCP = &spritzv1.SpritzACPMCPTransportCapabilities{
				HTTP: result.AgentCapabilities.MCP.HTTP,
				SSE:  result.AgentCapabilities.MCP.SSE,
			}
		}
		if len(result.AuthMethods) > 0 {
			status.AuthMethods = append([]string(nil), result.AuthMethods...)
		}
		status.LastError = ""
		return status, nil
	}
}

func (s *server) persistACPStatus(ctx context.Context, namespace, name string, status *spritzv1.SpritzACPStatus) error {
	if status == nil {
		return nil
	}
	var lastErr error
	for range 3 {
		latest := &spritzv1.Spritz{}
		if err := s.client.Get(ctx, clientKey(namespace, name), latest); err != nil {
			return err
		}
		if acpStatusesEqual(latest.Status.ACP, status) {
			return nil
		}
		latest.Status.ACP = deepCopyACPStatus(status)
		if err := s.client.Status().Update(ctx, latest); err != nil {
			if apierrors.IsConflict(err) {
				lastErr = err
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func deepCopyACPStatus(status *spritzv1.SpritzACPStatus) *spritzv1.SpritzACPStatus {
	if status == nil {
		return nil
	}
	out := &spritzv1.SpritzACPStatus{}
	status.DeepCopyInto(out)
	return out
}

func acpStatusesEqual(left, right *spritzv1.SpritzACPStatus) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func (s *server) ensureConversationResource(ctx context.Context, spritz *spritzv1.Spritz, requestedCWD string) (*spritzv1.SpritzConversation, error) {
	name := spritz.Name
	namespace := spritz.Namespace
	conversation := &spritzv1.SpritzConversation{}
	err := s.client.Get(ctx, clientKey(namespace, name), conversation)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if apierrors.IsNotFound(err) {
		resource := &spritzv1.SpritzConversation{
			TypeMeta: metav1.TypeMeta{
				APIVersion: spritzv1.GroupVersion.String(),
				Kind:       "SpritzConversation",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels: map[string]string{
					ownerLabelKey:           ownerLabelValue(spritz.Spec.Owner.ID),
					nameLabelKey:            spritz.Name,
					acpConversationLabelKey: acpConversationLabelValue,
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: spritzv1.GroupVersion.String(),
						Kind:       "Spritz",
						Name:       spritz.Name,
						UID:        spritz.UID,
					},
				},
			},
			Spec: spritzv1.SpritzConversationSpec{
				SpritzName:   spritz.Name,
				Owner:        spritz.Spec.Owner,
				Title:        defaultConversationTitle(spritz),
				CWD:          normalizeConversationCWD(requestedCWD),
				AgentInfo:    normalizeConversationAgentInfo(spritz.Status.ACP),
				Capabilities: normalizeConversationCapabilities(spritz.Status.ACP),
			},
		}
		if resource.Spec.CWD == "" {
			resource.Spec.CWD = defaultACPCWD
		}
		if err := s.client.Create(ctx, resource); err != nil {
			if apierrors.IsAlreadyExists(err) {
				if err := s.client.Get(ctx, clientKey(namespace, name), conversation); err != nil {
					return nil, err
				}
				return conversation, nil
			}
			return nil, err
		}
		return resource, nil
	}

	changed := false
	if conversation.Spec.SpritzName != spritz.Name {
		return nil, fmt.Errorf("conversation %s does not belong to spritz %s", conversation.Name, spritz.Name)
	}
	if conversation.Spec.Owner.ID != spritz.Spec.Owner.ID {
		conversation.Spec.Owner = spritz.Spec.Owner
		changed = true
	}
	title := defaultConversationTitle(spritz)
	if strings.TrimSpace(conversation.Spec.Title) == "" && title != "" {
		conversation.Spec.Title = title
		changed = true
	}
	if normalized := normalizeConversationCWD(requestedCWD); normalized != "" && conversation.Spec.CWD != normalized {
		conversation.Spec.CWD = normalized
		changed = true
	}
	if conversation.Spec.CWD == "" {
		conversation.Spec.CWD = defaultACPCWD
		changed = true
	}
	if !agentInfoEqual(conversation.Spec.AgentInfo, spritz.Status.ACP) {
		conversation.Spec.AgentInfo = normalizeConversationAgentInfo(spritz.Status.ACP)
		changed = true
	}
	if !capabilitiesEqual(conversation.Spec.Capabilities, spritz.Status.ACP) {
		conversation.Spec.Capabilities = normalizeConversationCapabilities(spritz.Status.ACP)
		changed = true
	}
	if changed {
		if err := s.client.Update(ctx, conversation); err != nil {
			return nil, err
		}
	}
	return conversation, nil
}

func agentInfoEqual(info *spritzv1.SpritzACPAgentInfo, status *spritzv1.SpritzACPStatus) bool {
	expected := normalizeConversationAgentInfo(status)
	if info == nil && expected == nil {
		return true
	}
	if info == nil || expected == nil {
		return false
	}
	return *info == *expected
}

func capabilitiesEqual(capabilities *spritzv1.SpritzACPCapabilities, status *spritzv1.SpritzACPStatus) bool {
	expected := normalizeConversationCapabilities(status)
	leftJSON, _ := json.Marshal(capabilities)
	rightJSON, _ := json.Marshal(expected)
	return string(leftJSON) == string(rightJSON)
}

func normalizeConversationAgentInfo(status *spritzv1.SpritzACPStatus) *spritzv1.SpritzACPAgentInfo {
	if status == nil || status.AgentInfo == nil {
		return nil
	}
	return &spritzv1.SpritzACPAgentInfo{
		Name:    status.AgentInfo.Name,
		Title:   status.AgentInfo.Title,
		Version: status.AgentInfo.Version,
	}
}

func normalizeConversationCapabilities(status *spritzv1.SpritzACPStatus) *spritzv1.SpritzACPCapabilities {
	if status == nil || status.Capabilities == nil {
		return nil
	}
	capabilities := &spritzv1.SpritzACPCapabilities{}
	status.Capabilities.DeepCopyInto(capabilities)
	return capabilities
}

func normalizeConversationCWD(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func defaultConversationTitle(spritz *spritzv1.Spritz) string {
	title := displayAgentName(spritz)
	if title != "" {
		return title
	}
	return spritz.Name
}

func displayAgentName(spritz *spritzv1.Spritz) string {
	if spritz == nil || spritz.Status.ACP == nil || spritz.Status.ACP.AgentInfo == nil {
		if spritz == nil {
			return ""
		}
		return spritz.Name
	}
	info := spritz.Status.ACP.AgentInfo
	if strings.TrimSpace(info.Title) != "" {
		return strings.TrimSpace(info.Title)
	}
	if strings.TrimSpace(info.Name) != "" {
		return strings.TrimSpace(info.Name)
	}
	return spritz.Name
}

func (s *server) getAuthorizedSpritz(ctx context.Context, principal principal, namespace, name string) (*spritzv1.Spritz, error) {
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, name), spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, err
	}
	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
		return nil, errForbidden
	}
	return spritz, nil
}

func (s *server) requestNamespace(c echo.Context) string {
	namespace := s.namespace
	if namespace == "" {
		namespace = strings.TrimSpace(c.QueryParam("namespace"))
	}
	return namespace
}

func (s *server) acpWorkspaceURL(namespace, name string) string {
	if s.acp.workspaceURL != nil {
		return s.acp.workspaceURL(namespace, name)
	}
	return (&url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", name, namespace, s.acp.port),
		Path:   s.acp.path,
	}).String()
}

func (s *server) writeACPResourceError(c echo.Context, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return writeError(c, http.StatusNotFound, "spritz not found")
	case errors.Is(err, errForbidden):
		return writeError(c, http.StatusForbidden, "forbidden")
	default:
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
}

func proxyWebSockets(left, right *websocket.Conn) error {
	errCh := make(chan error, 2)
	closeOnce := sync.Once{}
	closeBoth := func() {
		_ = left.Close()
		_ = right.Close()
	}

	go proxyWebSocketDirection(left, right, errCh)
	go proxyWebSocketDirection(right, left, errCh)

	err := <-errCh
	closeOnce.Do(closeBoth)
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return nil
	}
	return err
}

func proxyWebSocketDirection(src, dst *websocket.Conn, errCh chan<- error) {
	for {
		msgType, payload, err := src.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if err := dst.WriteMessage(msgType, payload); err != nil {
			errCh <- err
			return
		}
	}
}

func conversationKey(namespace, spritzName string) string {
	return namespace + "/" + spritzName
}
