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
	secured.GET("/api/spritzes", func(c echo.Context) error {
		handled = true
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
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
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"id":    p.ID,
			"email": p.Email,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
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

func TestAuthMiddlewareSetsPrincipalTypeAndScopes(t *testing.T) {
	t.Setenv("SPRITZ_AUTH_MODE", "header")
	t.Setenv("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id")
	t.Setenv("SPRITZ_AUTH_HEADER_TYPE", "X-Spritz-Principal-Type")
	t.Setenv("SPRITZ_AUTH_HEADER_SCOPES", "X-Spritz-Principal-Scopes")
	t.Setenv("SPRITZ_AUTH_HEADER_TRUST_TYPE_AND_SCOPES", "true")

	s := &server{auth: newAuthConfig()}
	e := echo.New()

	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"id":     p.ID,
			"type":   p.Type,
			"scopes": p.Scopes,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeService) {
		t.Fatalf("expected service principal type, got %#v", payload["type"])
	}
	scopes, _ := payload["scopes"].([]any)
	if len(scopes) != 2 {
		t.Fatalf("expected two scopes, got %#v", payload["scopes"])
	}
}

func TestAuthMiddlewareIgnoresHeaderTypeAndScopesByDefault(t *testing.T) {
	t.Setenv("SPRITZ_AUTH_MODE", "header")
	t.Setenv("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id")
	t.Setenv("SPRITZ_AUTH_HEADER_TYPE", "X-Spritz-Principal-Type")
	t.Setenv("SPRITZ_AUTH_HEADER_SCOPES", "X-Spritz-Principal-Scopes")

	s := &server{auth: newAuthConfig()}
	e := echo.New()

	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"id":     p.ID,
			"type":   p.Type,
			"scopes": p.Scopes,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeHuman) {
		t.Fatalf("expected header auth to default to human, got %#v", payload["type"])
	}
	scopes, _ := payload["scopes"].([]any)
	if len(scopes) != 0 {
		t.Fatalf("expected no header scopes by default, got %#v", payload["scopes"])
	}
}

func TestAuthMiddlewareDoesNotGrantAdminFromHeaderTypeClaim(t *testing.T) {
	t.Setenv("SPRITZ_AUTH_MODE", "header")
	t.Setenv("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id")
	t.Setenv("SPRITZ_AUTH_HEADER_TYPE", "X-Spritz-Principal-Type")

	s := &server{auth: newAuthConfig()}
	e := echo.New()

	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"type":  p.Type,
			"admin": p.IsAdmin,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("X-Spritz-User-Id", "user-123")
	req.Header.Set("X-Spritz-Principal-Type", "admin")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeHuman) {
		t.Fatalf("expected header admin claim to fall back to human, got %#v", payload["type"])
	}
	if admin, _ := payload["admin"].(bool); admin {
		t.Fatalf("expected header admin claim to remain non-admin")
	}
}

func TestBearerAuthParsesSpaceDelimitedScopes(t *testing.T) {
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub":   "zenobot",
			"type":  "service",
			"scope": "spritz.instances.create spritz.instances.assign_owner",
		})
	}))
	defer introspection.Close()

	t.Setenv("SPRITZ_AUTH_MODE", "bearer")
	t.Setenv("SPRITZ_AUTH_BEARER_INTROSPECTION_URL", introspection.URL)
	t.Setenv("SPRITZ_AUTH_BEARER_ID_PATHS", "sub")
	t.Setenv("SPRITZ_AUTH_BEARER_TYPE_PATHS", "type")
	t.Setenv("SPRITZ_AUTH_BEARER_SCOPES_PATHS", "scope")

	s := &server{auth: newAuthConfig()}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"type":   p.Type,
			"scopes": p.Scopes,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeService) {
		t.Fatalf("expected service principal type, got %#v", payload["type"])
	}
	scopes, _ := payload["scopes"].([]any)
	if len(scopes) != 2 {
		t.Fatalf("expected two scopes, got %#v", payload["scopes"])
	}
}

func TestBearerAuthDefaultsToServiceTypeWithoutTypeClaim(t *testing.T) {
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub":   "zenobot",
			"scope": "spritz.instances.create spritz.instances.assign_owner",
		})
	}))
	defer introspection.Close()

	t.Setenv("SPRITZ_AUTH_MODE", "auto")
	t.Setenv("SPRITZ_AUTH_BEARER_INTROSPECTION_URL", introspection.URL)
	t.Setenv("SPRITZ_AUTH_BEARER_ID_PATHS", "sub")
	t.Setenv("SPRITZ_AUTH_BEARER_SCOPES_PATHS", "scope")

	s := &server{auth: newAuthConfig()}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"type":   p.Type,
			"scopes": p.Scopes,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeService) {
		t.Fatalf("expected default bearer principal type to be service, got %#v", payload["type"])
	}
}

func TestBearerAuthDefaultsToHumanTypeWithoutTypeClaimInBearerMode(t *testing.T) {
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub":   "user-123",
			"email": "user@example.com",
		})
	}))
	defer introspection.Close()

	t.Setenv("SPRITZ_AUTH_MODE", "bearer")
	t.Setenv("SPRITZ_AUTH_BEARER_INTROSPECTION_URL", introspection.URL)
	t.Setenv("SPRITZ_AUTH_BEARER_ID_PATHS", "sub")

	s := &server{auth: newAuthConfig()}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"type": p.Type,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeHuman) {
		t.Fatalf("expected default bearer principal type to stay human in bearer mode, got %#v", payload["type"])
	}
}

func TestBearerAuthDoesNotGrantAdminFromTypeClaim(t *testing.T) {
	introspection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub":   "zenobot",
			"type":  "admin",
			"scope": "spritz.instances.create spritz.instances.assign_owner",
		})
	}))
	defer introspection.Close()

	t.Setenv("SPRITZ_AUTH_MODE", "auto")
	t.Setenv("SPRITZ_AUTH_BEARER_INTROSPECTION_URL", introspection.URL)
	t.Setenv("SPRITZ_AUTH_BEARER_ID_PATHS", "sub")
	t.Setenv("SPRITZ_AUTH_BEARER_TYPE_PATHS", "type")
	t.Setenv("SPRITZ_AUTH_BEARER_SCOPES_PATHS", "scope")

	s := &server{auth: newAuthConfig()}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", func(c echo.Context) error {
		p, ok := principalFromContext(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing principal"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"type":  p.Type,
			"admin": p.IsAdmin,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["type"] != string(principalTypeService) {
		t.Fatalf("expected bearer admin claim to fall back to service, got %#v", payload["type"])
	}
	if admin, _ := payload["admin"].(bool); admin {
		t.Fatalf("expected bearer admin claim to remain non-admin")
	}
}
