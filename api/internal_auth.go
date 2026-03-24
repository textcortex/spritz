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

func newInternalAuthConfig() internalAuthConfig {
	token := strings.TrimSpace(os.Getenv("SPRITZ_INTERNAL_TOKEN"))
	return internalAuthConfig{enabled: token != "", token: token}
}

func (s *server) internalAuthMiddleware() echo.MiddlewareFunc {
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
			if token == "" {
				value := c.Request().Header.Get("Authorization")
				if strings.HasPrefix(value, "Bearer ") {
					token = strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
				}
			}
			if token == "" || token != s.internalAuth.token {
				return writeError(c, http.StatusUnauthorized, "unauthorized")
			}
			return next(c)
		}
	}
}
