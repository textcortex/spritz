package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type config struct {
	Addr                 string
	PublicURL            string
	SlackClientID        string
	SlackClientSecret    string
	SlackSigningSecret   string
	OAuthStateSecret     string
	SlackAPIBaseURL      string
	SlackBotScopes       []string
	PresetID             string
	BackendBaseURL       string
	BackendInternalToken string
	SpritzBaseURL        string
	SpritzServiceToken   string
	PrincipalID          string
	HTTPTimeout          time.Duration
	DedupeTTL            time.Duration
	ProcessingTimeout    time.Duration
	SessionRetryInterval time.Duration
	StatusMessageDelay   time.Duration
	RecoveryTimeout      time.Duration
	PromptRetryInitial   time.Duration
	PromptRetryMax       time.Duration
	PromptRetryTimeout   time.Duration
}

func loadConfig() (config, error) {
	cfg := config{
		Addr:                 envOrDefault("SPRITZ_SLACK_GATEWAY_ADDR", ":8080"),
		PublicURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_GATEWAY_PUBLIC_URL")), "/"),
		SlackClientID:        strings.TrimSpace(os.Getenv("SPRITZ_SLACK_CLIENT_ID")),
		SlackClientSecret:    strings.TrimSpace(os.Getenv("SPRITZ_SLACK_CLIENT_SECRET")),
		SlackSigningSecret:   strings.TrimSpace(os.Getenv("SPRITZ_SLACK_SIGNING_SECRET")),
		OAuthStateSecret:     strings.TrimSpace(os.Getenv("SPRITZ_SLACK_OAUTH_STATE_SECRET")),
		SlackAPIBaseURL:      strings.TrimRight(envOrDefault("SPRITZ_SLACK_API_BASE_URL", "https://slack.com/api"), "/"),
		SlackBotScopes:       splitCSV(envOrDefault("SPRITZ_SLACK_BOT_SCOPES", "app_mentions:read,channels:history,chat:write,im:history,mpim:history")),
		PresetID:             strings.TrimSpace(envOrDefault("SPRITZ_SLACK_PRESET_ID", defaultSlackPresetID)),
		BackendBaseURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_BACKEND_BASE_URL")), "/"),
		BackendInternalToken: strings.TrimSpace(os.Getenv("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN")),
		SpritzBaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("SPRITZ_SLACK_SPRITZ_BASE_URL")), "/"),
		SpritzServiceToken:   strings.TrimSpace(os.Getenv("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN")),
		PrincipalID:          strings.TrimSpace(os.Getenv("SPRITZ_SLACK_PRINCIPAL_ID")),
		HTTPTimeout:          parseDurationEnv("SPRITZ_SLACK_HTTP_TIMEOUT", 15*time.Second),
		DedupeTTL:            parseDurationEnv("SPRITZ_SLACK_DEDUPE_TTL", 10*time.Minute),
		ProcessingTimeout:    parseDurationEnv("SPRITZ_SLACK_PROCESSING_TIMEOUT", 60*time.Second),
		SessionRetryInterval: parseDurationEnv("SPRITZ_SLACK_SESSION_RETRY_INTERVAL", time.Second),
		StatusMessageDelay:   parseDurationEnv("SPRITZ_SLACK_STATUS_MESSAGE_DELAY", 5*time.Second),
		RecoveryTimeout:      parseDurationEnv("SPRITZ_SLACK_RECOVERY_TIMEOUT", 60*time.Second),
		PromptRetryInitial:   parseDurationEnv("SPRITZ_SLACK_PROMPT_RETRY_INITIAL", 250*time.Millisecond),
		PromptRetryMax:       parseDurationEnv("SPRITZ_SLACK_PROMPT_RETRY_MAX", 2*time.Second),
		PromptRetryTimeout:   parseDurationEnv("SPRITZ_SLACK_PROMPT_RETRY_TIMEOUT", 8*time.Second),
	}

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
	if cfg.BackendInternalToken == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN is required")
	}
	if cfg.SpritzBaseURL == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_SPRITZ_BASE_URL is required")
	}
	if cfg.SpritzServiceToken == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN is required")
	}
	if cfg.PrincipalID == "" {
		return config{}, fmt.Errorf("SPRITZ_SLACK_PRINCIPAL_ID is required")
	}
	return cfg, nil
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
