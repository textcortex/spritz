package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

type internalAuthConfig struct {
	enabled bool
	token   string
}

const internalTokenHeader = "X-Spritz-Internal-Token"

const (
	canonicalPrincipalIDHeader    = "X-Spritz-User-Id"
	canonicalPrincipalEmailHeader = "X-Spritz-User-Email"
	canonicalPrincipalTeamsHeader = "X-Spritz-User-Teams"
	canonicalPrincipalTypeHeader  = "X-Spritz-Principal-Type"
	canonicalPrincipalScopeHeader = "X-Spritz-Principal-Scopes"
)

func newInternalAuthConfig() internalAuthConfig {
	token := strings.TrimSpace(os.Getenv("SPRITZ_INTERNAL_TOKEN"))
	return internalAuthConfig{enabled: token != "", token: token}
}

func (s *server) internalAuthMiddleware() echo.MiddlewareFunc {
	return s.internalAuthMiddlewareWithBearerFallback(true)
}

func (s *server) internalAuthHeaderMiddleware() echo.MiddlewareFunc {
	return s.internalAuthMiddlewareWithBearerFallback(false)
}

func (s *server) internalAuthMiddlewareWithBearerFallback(allowBearerFallback bool) echo.MiddlewareFunc {
	if !s.internalAuth.enabled {
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				return next(c)
			}
		}
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			token := strings.TrimSpace(c.Request().Header.Get(internalTokenHeader))
			if token == "" && allowBearerFallback {
				value := c.Request().Header.Get("Authorization")
				if strings.HasPrefix(value, "Bearer ") {
					token = strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
				}
			}
			if token == "" || token != s.internalAuth.token {
				return writeError(c, http.StatusUnauthorized, "unauthorized")
			}
			s.bridgeCanonicalPrincipalHeaders(c.Request())
			return next(c)
		}
	}
}

// bridgeCanonicalPrincipalHeaders lets internal callers keep using the stable
// Spritz principal headers even when a deployment is configured to trust
// different proxy header names at the edge.
func (s *server) bridgeCanonicalPrincipalHeaders(r *http.Request) {
	if r == nil {
		return
	}
	bridgePrincipalHeader(r.Header, s.auth.headerID, canonicalPrincipalIDHeader)
	bridgePrincipalHeader(r.Header, s.auth.headerEmail, canonicalPrincipalEmailHeader)
	bridgePrincipalHeader(r.Header, s.auth.headerTeams, canonicalPrincipalTeamsHeader)
	bridgePrincipalHeader(r.Header, s.auth.headerType, canonicalPrincipalTypeHeader)
	bridgePrincipalHeader(r.Header, s.auth.headerScopes, canonicalPrincipalScopeHeader)
}

func bridgePrincipalHeader(headers http.Header, targetHeader, canonicalHeader string) {
	targetHeader = strings.TrimSpace(targetHeader)
	if targetHeader == "" || strings.EqualFold(targetHeader, canonicalHeader) {
		return
	}
	if strings.TrimSpace(headers.Get(targetHeader)) != "" {
		return
	}
	value := strings.TrimSpace(headers.Get(canonicalHeader))
	if value == "" {
		return
	}
	headers.Set(targetHeader, value)
}
