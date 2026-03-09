package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	xwebsocket "golang.org/x/net/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	defaultACPProbeTimeout    = 3 * time.Second
	defaultACPRefreshInterval = 30 * time.Second
)

type ACPProbeConfig struct {
	Enabled         bool
	Port            int32
	Path            string
	ProbeTimeout    time.Duration
	RefreshInterval time.Duration
	ClientInfo      acpImplementationInfo
	WorkspaceURL    func(namespace, name string) string
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

func NewACPProbeConfigFromEnv() ACPProbeConfig {
	path := strings.TrimSpace(os.Getenv("SPRITZ_ACP_PATH"))
	if path == "" {
		path = spritzv1.DefaultACPPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return ACPProbeConfig{
		Enabled:         parseBoolEnv("SPRITZ_ACP_ENABLED", true),
		Port:            int32(parseIntEnv("SPRITZ_ACP_PORT", int(spritzv1.DefaultACPPort))),
		Path:            path,
		ProbeTimeout:    parseDurationEnv("SPRITZ_ACP_PROBE_TIMEOUT", defaultACPProbeTimeout),
		RefreshInterval: parseDurationEnv("SPRITZ_ACP_REFRESH_INTERVAL", defaultACPRefreshInterval),
		ClientInfo: acpImplementationInfo{
			Name:    envOrDefault("SPRITZ_ACP_CLIENT_NAME", "spritz-operator"),
			Title:   envOrDefault("SPRITZ_ACP_CLIENT_TITLE", "Spritz ACP Operator"),
			Version: envOrDefault("SPRITZ_ACP_CLIENT_VERSION", "1.0.0"),
		},
	}
}

func (c ACPProbeConfig) shouldProbe(status *spritzv1.SpritzACPStatus) bool {
	if !c.Enabled {
		return false
	}
	if status == nil || status.LastProbeAt == nil {
		return true
	}
	if status.State == "" || status.State == "probing" {
		return true
	}
	return time.Since(status.LastProbeAt.Time) >= c.RefreshInterval
}

func (c ACPProbeConfig) workspaceURL(namespace, name string) string {
	if c.WorkspaceURL != nil {
		return c.WorkspaceURL(namespace, name)
	}
	return (&url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", name, namespace, c.Port),
		Path:   c.Path,
	}).String()
}

func (r *SpritzReconciler) reconcileACPStatus(ctx context.Context, spritz *spritzv1.Spritz, workloadReady bool) (*spritzv1.SpritzACPStatus, *time.Duration, error) {
	if !r.ACP.Enabled {
		return nil, nil, nil
	}
	if !workloadReady {
		status := baseACPStatus(r.ACP)
		status.State = "unknown"
		status.LastError = ""
		return status, nil, nil
	}
	if !r.ACP.shouldProbe(spritz.Status.ACP) {
		return deepCopyACPStatus(spritz.Status.ACP), durationPtr(time.Until(spritz.Status.ACP.LastProbeAt.Time.Add(r.ACP.RefreshInterval))), nil
	}
	status, err := r.probeACP(ctx, spritz.Namespace, spritz.Name)
	return status, durationPtr(r.ACP.RefreshInterval), err
}

func (r *SpritzReconciler) probeACP(ctx context.Context, namespace, name string) (*spritzv1.SpritzACPStatus, error) {
	status := baseACPStatus(r.ACP)
	now := metav1.Now()
	status.State = "probing"
	status.LastProbeAt = &now

	conn, err := dialACP(ctx, r.ACP, namespace, name)
	if err != nil {
		status.State = "unavailable"
		status.LastError = err.Error()
		return status, err
	}
	defer func() {
		_ = conn.Close()
	}()
	_ = conn.SetDeadline(time.Now().Add(r.ACP.ProbeTimeout))

	requestID := "spritz-initialize"
	if err := xwebsocket.JSON.Send(conn, map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "initialize",
		"params": acpInitializeRequest{
			ProtocolVersion:    1,
			ClientCapabilities: map[string]any{},
			ClientInfo:         r.ACP.ClientInfo,
		},
	}); err != nil {
		status.State = "unavailable"
		status.LastError = err.Error()
		return status, err
	}

	for {
		var message acpJSONRPCMessage
		if err := xwebsocket.JSON.Receive(conn, &message); err != nil {
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

func dialACP(ctx context.Context, cfg ACPProbeConfig, namespace, name string) (*xwebsocket.Conn, error) {
	wsURL := cfg.workspaceURL(namespace, name)
	config, err := xwebsocket.NewConfig(wsURL, "http://spritz-operator.local")
	if err != nil {
		return nil, err
	}
	config.Dialer = &net.Dialer{Timeout: cfg.ProbeTimeout}
	type dialResult struct {
		conn *xwebsocket.Conn
		err  error
	}
	resultCh := make(chan dialResult, 1)
	go func() {
		conn, err := xwebsocket.DialConfig(config)
		resultCh <- dialResult{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.conn, result.err
	}
}

func baseACPStatus(cfg ACPProbeConfig) *spritzv1.SpritzACPStatus {
	return &spritzv1.SpritzACPStatus{
		Endpoint: &spritzv1.SpritzACPEndpoint{
			Port: cfg.Port,
			Path: cfg.Path,
		},
	}
}

func deepCopyACPStatus(status *spritzv1.SpritzACPStatus) *spritzv1.SpritzACPStatus {
	if status == nil {
		return nil
	}
	out := &spritzv1.SpritzACPStatus{}
	status.DeepCopyInto(out)
	return out
}

func durationPtr(value time.Duration) *time.Duration {
	if value <= 0 {
		return nil
	}
	copy := value
	return &copy
}

func parseBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseIntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
