package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"

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
	enabled        bool
	port           int32
	path           string
	allowedOrigins map[string]struct{}
	workspaceURL   func(namespace, name string) string
	clientInfo     acpBootstrapClientInfo
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
