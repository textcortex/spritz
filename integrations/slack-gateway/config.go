package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type config struct {
	Addr                       string
	PublicURL                  string
	BrowserAuthHeaderID        string
	BrowserAuthHeaderEmail     string
	SlackClientID              string
	SlackClientSecret          string
	SlackSigningSecret         string
	OAuthStateSecret           string
	SlackAPIBaseURL            string
	SlackBotScopes             []string
	AckReaction                string
	RemoveAckAfterReply        bool
	ChannelActionTokens        []channelActionToken
	PresetID                   string
	BackendBaseURL             string
	BackendFastAPIBaseURL      string
	BackendInternalToken       string
	SpritzBaseURL              string
	ReactBaseURL               string
	SpritzServiceToken         string
	PrincipalID                string
	HTTPTimeout                time.Duration
	DedupeTTL                  time.Duration
	ProcessingTimeout          time.Duration
	SessionRetryInterval       time.Duration
	StatusMessageDelay         time.Duration
	RecoveryTimeout            time.Duration
	PromptRetryInitial         time.Duration
	PromptRetryMax             time.Duration
	PromptRetryTimeout         time.Duration
	InstallationPolicyCacheTTL time.Duration
}

type channelActionToken struct {
	Token  string
	Target channelActionTarget
}

type channelActionTarget struct {
	TeamID    string
	ChannelID string
}

func loadConfig() (config, error) {
	cfg := config{
		Addr:                       envOrDefault("SPRITZ_SLACK_GATEWAY_ADDR", ":8080"),
		PublicURL:                  strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_GATEWAY_PUBLIC_URL")), "/"),
		BrowserAuthHeaderID:        envOrDefault("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id"),
		BrowserAuthHeaderEmail:     envOrDefault("SPRITZ_AUTH_HEADER_EMAIL", "X-Spritz-User-Email"),
		SlackClientID:              strings.TrimSpace(os.Getenv("SPRITZ_SLACK_CLIENT_ID")),
		SlackClientSecret:          strings.TrimSpace(os.Getenv("SPRITZ_SLACK_CLIENT_SECRET")),
		SlackSigningSecret:         strings.TrimSpace(os.Getenv("SPRITZ_SLACK_SIGNING_SECRET")),
		OAuthStateSecret:           strings.TrimSpace(os.Getenv("SPRITZ_SLACK_OAUTH_STATE_SECRET")),
		SlackAPIBaseURL:            strings.TrimRight(envOrDefault("SPRITZ_SLACK_API_BASE_URL", "https://slack.com/api"), "/"),
		SlackBotScopes:             splitCSV(envOrDefault("SPRITZ_SLACK_BOT_SCOPES", "app_mentions:read,channels:history,chat:write,im:history,mpim:history,reactions:write")),
		AckReaction:                normalizeSlackReactionName(envOrDefault("SPRITZ_SLACK_ACK_REACTION", "eyes")),
		RemoveAckAfterReply:        parseBoolEnv("SPRITZ_SLACK_REMOVE_ACK_AFTER_REPLY", true),
		PresetID:                   strings.TrimSpace(envOrDefault("SPRITZ_SLACK_PRESET_ID", defaultSlackPresetID)),
		BackendBaseURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_BACKEND_BASE_URL")), "/"),
		BackendFastAPIBaseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_BACKEND_FASTAPI_BASE_URL")), "/"),
		BackendInternalToken:       strings.TrimSpace(os.Getenv("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN")),
		SpritzBaseURL:              strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_SPRITZ_BASE_URL")), "/"),
		ReactBaseURL:               strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_REACT_BASE_URL")), "/"),
		SpritzServiceToken:         strings.TrimSpace(os.Getenv("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN")),
		PrincipalID:                strings.TrimSpace(os.Getenv("SPRITZ_SLACK_PRINCIPAL_ID")),
		HTTPTimeout:                parseDurationEnv("SPRITZ_SLACK_HTTP_TIMEOUT", 15*time.Second),
		DedupeTTL:                  parseDurationEnv("SPRITZ_SLACK_DEDUPE_TTL", 10*time.Minute),
		ProcessingTimeout:          parseDurationEnv("SPRITZ_SLACK_PROCESSING_TIMEOUT", 120*time.Second),
		SessionRetryInterval:       parseDurationEnv("SPRITZ_SLACK_SESSION_RETRY_INTERVAL", time.Second),
		StatusMessageDelay:         parseDurationEnv("SPRITZ_SLACK_STATUS_MESSAGE_DELAY", 5*time.Second),
		RecoveryTimeout:            parseDurationEnv("SPRITZ_SLACK_RECOVERY_TIMEOUT", 120*time.Second),
		PromptRetryInitial:         parseDurationEnv("SPRITZ_SLACK_PROMPT_RETRY_INITIAL", 250*time.Millisecond),
		PromptRetryMax:             parseDurationEnv("SPRITZ_SLACK_PROMPT_RETRY_MAX", 2*time.Second),
		PromptRetryTimeout:         parseDurationEnv("SPRITZ_SLACK_PROMPT_RETRY_TIMEOUT", 8*time.Second),
		InstallationPolicyCacheTTL: parseDurationEnv("SPRITZ_SLACK_INSTALLATION_POLICY_CACHE_TTL", 10*time.Second),
	}
	channelActionTokens, err := parseChannelActionTokens(
		os.Getenv("SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN"),
		os.Getenv("SPRITZ_SLACK_CHANNEL_ACTIONS_TARGETS"),
		os.Getenv("SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN_BINDINGS"),
	)
	if err != nil {
		return config{}, err
	}
	cfg.ChannelActionTokens = channelActionTokens

	if cfg.PublicURL == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_GATEWAY_PUBLIC_URL is required")
	}
	publicURL, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return config{}, fmt.Errorf("SPRITZ_SLACK_GATEWAY_PUBLIC_URL is invalid: %w", err)
	}
	if strings.TrimSpace(publicURL.Scheme) == "" || strings.TrimSpace(publicURL.Host) == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_GATEWAY_PUBLIC_URL must be an absolute URL")
	}
	if cfg.SlackClientID == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_CLIENT_ID is required")
	}
	if cfg.SlackClientSecret == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_CLIENT_SECRET is required")
	}
	if cfg.SlackSigningSecret == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_SIGNING_SECRET is required")
	}
	if cfg.OAuthStateSecret == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_OAUTH_STATE_SECRET is required")
	}
	if cfg.BackendBaseURL == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_BACKEND_BASE_URL is required")
	}
	if cfg.BackendFastAPIBaseURL == "" {
		cfg.BackendFastAPIBaseURL = cfg.BackendBaseURL
	}
	if cfg.BackendInternalToken == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN is required")
	}
	if cfg.SpritzBaseURL == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_SPRITZ_BASE_URL is required")
	}
	if cfg.ReactBaseURL == "" {
		cfg.ReactBaseURL = defaultReactBaseURL(cfg.PublicURL, cfg.SpritzBaseURL)
	}
	reactURL, err := url.Parse(cfg.ReactBaseURL)
	if err != nil {
		return config{}, fmt.Errorf("SPRITZ_SLACK_REACT_BASE_URL is invalid: %w", err)
	}
	if strings.TrimSpace(reactURL.Scheme) == "" || strings.TrimSpace(reactURL.Host) == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_REACT_BASE_URL must be an absolute URL")
	}
	if cfg.SpritzServiceToken == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN is required")
	}
	if cfg.PrincipalID == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_PRINCIPAL_ID is required")
	}
	return cfg, nil
}

func parseChannelActionTokens(legacyToken, legacyTargets, bindingsRaw string) ([]channelActionToken, error) {
	tokens := make([]channelActionToken, 0)
	legacyToken = strings.TrimSpace(legacyToken)
	for _, rawTarget := range splitCSV(legacyTargets) {
		if legacyToken == "" {
			return nil, fmt.Errorf("SPRITZ_SLACK_CHANNEL_ACTIONS_TARGETS requires SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN")
		}
		target, err := parseChannelActionTarget(rawTarget)
		if err != nil {
			return nil, fmt.Errorf("SPRITZ_SLACK_CHANNEL_ACTIONS_TARGETS is invalid: %w", err)
		}
		tokens = append(tokens, channelActionToken{Token: legacyToken, Target: target})
	}

	bindingsRaw = strings.TrimSpace(bindingsRaw)
	if bindingsRaw == "" {
		return tokens, nil
	}
	var bindings []struct {
		Token   string `json:"token"`
		Targets []struct {
			TeamID    string `json:"teamId"`
			ChannelID string `json:"channelId"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(bindingsRaw), &bindings); err != nil {
		return nil, fmt.Errorf("SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN_BINDINGS is invalid JSON: %w", err)
	}
	for index, binding := range bindings {
		token := strings.TrimSpace(binding.Token)
		if token == "" {
			return nil, fmt.Errorf("SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN_BINDINGS[%d].token is required", index)
		}
		if len(binding.Targets) == 0 {
			return nil, fmt.Errorf("SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN_BINDINGS[%d].targets is required", index)
		}
		for targetIndex, rawTarget := range binding.Targets {
			target := channelActionTarget{
				TeamID:    strings.TrimSpace(rawTarget.TeamID),
				ChannelID: strings.TrimSpace(rawTarget.ChannelID),
			}
			if target.TeamID == "" || target.ChannelID == "" {
				return nil, fmt.Errorf("SPRITZ_SLACK_CHANNEL_ACTIONS_TOKEN_BINDINGS[%d].targets[%d] is invalid", index, targetIndex)
			}
			tokens = append(tokens, channelActionToken{Token: token, Target: target})
		}
	}
	return tokens, nil
}

func parseChannelActionTarget(raw string) (channelActionTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return channelActionTarget{}, fmt.Errorf("target is empty")
	}
	separators := []string{":", "/"}
	for _, separator := range separators {
		teamID, channelID, ok := strings.Cut(raw, separator)
		if ok {
			target := channelActionTarget{
				TeamID:    strings.TrimSpace(teamID),
				ChannelID: strings.TrimSpace(channelID),
			}
			if target.TeamID == "" || target.ChannelID == "" {
				return channelActionTarget{}, fmt.Errorf("target %q must be TEAM%sCHANNEL", raw, separator)
			}
			return target, nil
		}
	}
	return channelActionTarget{}, fmt.Errorf("target %q must be TEAM:CHANNEL or TEAM/CHANNEL", raw)
}

func defaultReactBaseURL(publicURL string, spritzBaseURL string) string {
	spritzBaseURL = strings.TrimRight(strings.TrimSpace(spritzBaseURL), "/")
	if !isPrivateServiceBaseURL(spritzBaseURL) {
		return spritzBaseURL
	}
	if publicReactURL := reactBaseURLFromGatewayPublicURL(publicURL); publicReactURL != "" {
		return publicReactURL
	}
	return spritzBaseURL
}

func reactBaseURLFromGatewayPublicURL(raw string) string {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/slack-gateway") {
		path = strings.TrimSuffix(path, "/slack-gateway")
	} else {
		path = ""
	}
	parsed.RawPath = ""
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func isPrivateServiceBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return strings.HasSuffix(host, ".svc") ||
		strings.Contains(host, ".svc.") ||
		strings.HasSuffix(host, ".cluster.local")
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
