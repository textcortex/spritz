package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestAuthMiddlewareRequiresHeader(t *testing.T) {
	t.Setenv("SPRITZ_AUTH_MODE", "header")
	t.Setenv("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id")

	s := &server{auth: newAuthConfig()}
	e := echo.New()

	handled := false
	secured := e.Group("", s.authMiddleware())
	secured.GET("/spritzes", func(c echo.Context) error {
		handled = true
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/spritzes", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if handled {
		t.Fatalf("handler should not run when unauthenticated")
	}
	if !strings.Contains(rec.Body.String(), "unauthenticated") {
		t.Fatalf("expected unauthenticated message, got %q", rec.Body.String())
	}
}

func TestAuthMiddlewareSetsPrincipal(t *testing.T) {
	t.Setenv("SPRITZ_AUTH_MODE", "header")
	t.Setenv("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id")
	t.Setenv("SPRITZ_AUTH_HEADER_EMAIL", "X-Spritz-User-Email")

	s := &server{auth: newAuthConfig()}
	e := echo.New()

	secured := e.Group("", s.authMiddleware())
	secured.GET("/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"id":    p.ID,
			"email": p.Email,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/spritzes", nil)
	req.Header.Set("X-Spritz-User-Id", "user-123")
	req.Header.Set("X-Spritz-User-Email", "user@example.com")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	payload := map[string]string{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["id"] != "user-123" {
		t.Fatalf("expected id to be user-123, got %q", payload["id"])
	}
	if payload["email"] != "user@example.com" {
		t.Fatalf("expected email to be user@example.com, got %q", payload["email"])
	}
}
