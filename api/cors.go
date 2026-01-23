package main

import (
	"os"
	"strings"
)

type corsConfig struct {
	origins        map[string]struct{}
	allowAnyOrigin bool
	allowHeaders   string
	allowMethods   string
	allowCreds     bool
}

func newCORSConfig() corsConfig {
	origins := splitList(os.Getenv("SPRITZ_CORS_ORIGINS"))
	originSet := map[string]struct{}{}
	allowAny := false
	for _, origin := range origins {
		if origin == "*" {
			allowAny = true
			continue
		}
		originSet[origin] = struct{}{}
	}

	allowHeaders := strings.TrimSpace(os.Getenv("SPRITZ_CORS_ALLOW_HEADERS"))
	if allowHeaders == "" {
		allowHeaders = "Content-Type,X-Spritz-User-Id,X-Spritz-User-Email,X-Spritz-User-Teams"
	}

	allowMethods := strings.TrimSpace(os.Getenv("SPRITZ_CORS_ALLOW_METHODS"))
	if allowMethods == "" {
		allowMethods = "GET,POST,DELETE,OPTIONS"
	}

	allowCreds := parseBool(os.Getenv("SPRITZ_CORS_ALLOW_CREDENTIALS"), true)
	if allowAny && allowCreds {
		allowCreds = false
	}

	return corsConfig{
		origins:        originSet,
		allowAnyOrigin: allowAny,
		allowHeaders:   allowHeaders,
		allowMethods:   allowMethods,
		allowCreds:     allowCreds,
	}
}

func (c corsConfig) enabled() bool {
	return c.allowAnyOrigin || len(c.origins) > 0
}

func (c corsConfig) isAllowedOrigin(origin string) bool {
	if c.allowAnyOrigin {
		return true
	}
	_, ok := c.origins[origin]
	return ok
}

func parseBool(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
