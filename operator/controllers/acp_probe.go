package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	defaultACPProbeTimeout            = 3 * time.Second
	defaultACPRefreshInterval         = 30 * time.Second
	defaultACPMetadataRefreshInterval = 5 * time.Minute
	defaultACPHealthPath              = "/healthz"
	defaultACPMetadataPath            = "/.well-known/spritz-acp"
)

type ACPProbeConfig struct {
	Enabled                 bool
	Port                    int32
	Path                    string
	HealthPath              string
	MetadataPath            string
	ProbeTimeout            time.Duration
	RefreshInterval         time.Duration
	MetadataRefreshInterval time.Duration
	ClientInfo              acpImplementationInfo
	InstanceURL             func(namespace, name string) string
}

type acpImplementationInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
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
	LoadSession bool                         `json:"loadSession,omitempty"`
	Prompt      *acpPromptCapabilities       `json:"promptCapabilities,omitempty"`
	MCP         *acpMCPTransportCapabilities `json:"mcp,omitempty"`
}

type acpMetadataResponse struct {
	ProtocolVersion   int32                 `json:"protocolVersion"`
	AgentCapabilities acpAgentCapabilities  `json:"agentCapabilities,omitempty"`
	AgentInfo         acpImplementationInfo `json:"agentInfo,omitempty"`
	AuthMethods       []string              `json:"authMethods,omitempty"`
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
		Enabled:                 parseBoolEnv("SPRITZ_ACP_ENABLED", true),
		Port:                    int32(parseIntEnv("SPRITZ_ACP_PORT", int(spritzv1.DefaultACPPort))),
		Path:                    path,
		HealthPath:              normalizeACPPath(envOrDefault("SPRITZ_ACP_HEALTH_PATH", defaultACPHealthPath), defaultACPHealthPath),
		MetadataPath:            normalizeACPPath(envOrDefault("SPRITZ_ACP_METADATA_PATH", defaultACPMetadataPath), defaultACPMetadataPath),
		ProbeTimeout:            parseDurationEnv("SPRITZ_ACP_PROBE_TIMEOUT", defaultACPProbeTimeout),
		RefreshInterval:         parseDurationEnv("SPRITZ_ACP_REFRESH_INTERVAL", defaultACPRefreshInterval),
		MetadataRefreshInterval: parseDurationEnv("SPRITZ_ACP_METADATA_REFRESH_INTERVAL", defaultACPMetadataRefreshInterval),
		ClientInfo: acpImplementationInfo{
			Name:    envOrDefault("SPRITZ_ACP_CLIENT_NAME", "spritz-operator"),
			Title:   envOrDefault("SPRITZ_ACP_CLIENT_TITLE", "Spritz ACP Operator"),
			Version: envOrDefault("SPRITZ_ACP_CLIENT_VERSION", "1.0.0"),
		},
	}
}

func normalizeACPPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func (c ACPProbeConfig) shouldCheckHealth(status *spritzv1.SpritzACPStatus) bool {
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

func (c ACPProbeConfig) shouldRefreshMetadata(status *spritzv1.SpritzACPStatus) bool {
	if !c.Enabled {
		return false
	}
	if status == nil || status.LastMetadataAt == nil {
		return true
	}
	if status.AgentInfo == nil || status.Capabilities == nil {
		return true
	}
	return time.Since(status.LastMetadataAt.Time) >= c.MetadataRefreshInterval
}

func (c ACPProbeConfig) instanceBaseURL(namespace, name string) string {
	if c.InstanceURL != nil {
		return c.InstanceURL(namespace, name)
	}
	return (&url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", name, namespace, c.Port),
	}).String()
}

func (c ACPProbeConfig) endpointURL(namespace, name, requestPath string) string {
	base, err := url.Parse(c.instanceBaseURL(namespace, name))
	if err != nil {
		return ""
	}
	base.Path = requestPath
	return base.String()
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

	currentStatus := deepCopyACPStatus(spritz.Status.ACP)
	if currentStatus == nil {
		currentStatus = baseACPStatus(r.ACP)
	}

	healthNeedsCheck := r.ACP.shouldCheckHealth(currentStatus)
	metadataNeedsRefresh := r.ACP.shouldRefreshMetadata(currentStatus)
	if !healthNeedsCheck && !metadataNeedsRefresh {
		next := minDurationPtr(
			durationPtr(time.Until(currentStatus.LastProbeAt.Time.Add(r.ACP.RefreshInterval))),
			durationPtr(time.Until(currentStatus.LastMetadataAt.Time.Add(r.ACP.MetadataRefreshInterval))),
		)
		return currentStatus, next, nil
	}

	status, err := r.fetchACPStatus(ctx, spritz.Namespace, spritz.Name, currentStatus, metadataNeedsRefresh)
	next := minDurationPtr(durationPtr(r.ACP.RefreshInterval), durationPtr(r.ACP.MetadataRefreshInterval))
	return status, next, err
}

func (r *SpritzReconciler) fetchACPStatus(
	ctx context.Context,
	namespace, name string,
	currentStatus *spritzv1.SpritzACPStatus,
	refreshMetadata bool,
) (*spritzv1.SpritzACPStatus, error) {
	status := baseACPStatus(r.ACP)
	if currentStatus != nil {
		currentStatus.DeepCopyInto(status)
	}
	now := metav1.Now()
	status.State = "probing"
	status.LastProbeAt = &now

	if err := checkACPHealth(ctx, r.ACP, namespace, name); err != nil {
		status.State = "unavailable"
		status.LastError = err.Error()
		return status, err
	}

	if !refreshMetadata {
		status.State = "ready"
		status.LastError = ""
		return status, nil
	}

	metadata, err := fetchACPMetadata(ctx, r.ACP, namespace, name)
	if err != nil {
		status.State = "error"
		status.LastError = err.Error()
		return status, err
	}

	status.State = "ready"
	status.ProtocolVersion = metadata.ProtocolVersion
	status.AgentInfo = &spritzv1.SpritzACPAgentInfo{
		Name:    metadata.AgentInfo.Name,
		Title:   metadata.AgentInfo.Title,
		Version: metadata.AgentInfo.Version,
	}
	status.Capabilities = &spritzv1.SpritzACPCapabilities{
		LoadSession: metadata.AgentCapabilities.LoadSession,
	}
	if metadata.AgentCapabilities.Prompt != nil {
		status.Capabilities.Prompt = &spritzv1.SpritzACPPromptCapabilities{
			Image:           metadata.AgentCapabilities.Prompt.Image,
			Audio:           metadata.AgentCapabilities.Prompt.Audio,
			EmbeddedContext: metadata.AgentCapabilities.Prompt.EmbeddedContext,
		}
	}
	if metadata.AgentCapabilities.MCP != nil {
		status.Capabilities.MCP = &spritzv1.SpritzACPMCPTransportCapabilities{
			HTTP: metadata.AgentCapabilities.MCP.HTTP,
			SSE:  metadata.AgentCapabilities.MCP.SSE,
		}
	}
	status.AuthMethods = append([]string(nil), metadata.AuthMethods...)
	status.LastMetadataAt = &now
	status.LastError = ""
	return status, nil
}

func checkACPHealth(ctx context.Context, cfg ACPProbeConfig, namespace, name string) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		cfg.endpointURL(namespace, name, cfg.HealthPath),
		nil,
	)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: cfg.ProbeTimeout}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ACP health returned %d", response.StatusCode)
	}
	return nil
}

func fetchACPMetadata(ctx context.Context, cfg ACPProbeConfig, namespace, name string) (*acpMetadataResponse, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		cfg.endpointURL(namespace, name, cfg.MetadataPath),
		nil,
	)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: cfg.ProbeTimeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ACP metadata returned %d", response.StatusCode)
	}
	var metadata acpMetadataResponse
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
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

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
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

func parseBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
