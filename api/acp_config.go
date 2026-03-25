package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	defaultACPCWD                 = "/home/dev"
	defaultACPConversationTitle   = "New conversation"
	acpConversationLabelKey       = "spritz.sh/acp-conversation"
	acpConversationLabelValue     = "true"
	acpConversationSpritzLabelKey = "spritz.sh/spritz-name"
	acpConversationOwnerLabelKey  = ownerLabelKey
)

type acpConfig struct {
	enabled              bool
	port                 int32
	path                 string
	allowedOrigins       map[string]struct{}
	instanceURL          func(namespace, name string) string
	clientInfo           acpBootstrapClientInfo
	clientCapabilities   map[string]any
	bootstrapDialTimeout time.Duration
	promptTimeout        time.Duration
	promptSettleTimeout  time.Duration
}

func defaultACPClientCapabilities() map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"terminal": true,
			"_meta": map[string]any{
				"gateway": true,
			},
		},
		"_meta": map[string]any{
			"terminal-auth":   true,
			"terminal_output": true,
		},
	}
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
		allowedOrigins: splitSet(os.Getenv("SPRITZ_ACP_ORIGINS")),
		clientInfo: acpBootstrapClientInfo{
			Name:    envOrDefault("SPRITZ_ACP_CLIENT_NAME", "spritz-api"),
			Title:   envOrDefault("SPRITZ_ACP_CLIENT_TITLE", "Spritz ACP API"),
			Version: envOrDefault("SPRITZ_ACP_CLIENT_VERSION", "1.0.0"),
		},
		clientCapabilities:   defaultACPClientCapabilities(),
		bootstrapDialTimeout: parseDurationEnv("SPRITZ_ACP_BOOTSTRAP_DIAL_TIMEOUT", 5*time.Second),
		promptTimeout:        parseDurationEnv("SPRITZ_ACP_PROMPT_TIMEOUT", 90*time.Second),
		promptSettleTimeout:  parseDurationEnv("SPRITZ_ACP_PROMPT_SETTLE_TIMEOUT", 750*time.Millisecond),
	}
}

func (a acpConfig) allowOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Non-browser ACP clients authenticate with bearer tokens and do not rely
		// on browser-origin semantics.
		return strings.TrimSpace(r.Header.Get("Authorization")) != ""
	}
	if len(a.allowedOrigins) == 0 {
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
