package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newTestSpritzScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register spritz scheme: %v", err)
	}
	return scheme
}

func newCreateSpritzTestServer(t *testing.T) *server {
	t.Helper()
	scheme := newTestSpritzScheme(t)
	return &server{
		client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		scheme:    scheme,
		namespace: "spritz-test",
		auth: authConfig{
			mode:        authModeHeader,
			headerID:    "X-Spritz-User-Id",
			headerEmail: "X-Spritz-User-Email",
		},
		internalAuth:     internalAuthConfig{enabled: false},
		userConfigPolicy: userConfigPolicy{},
	}
}

func TestCreateSpritzOwnerUsesIDAndOmitsEmail(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"name":"tidal-ember","spec":{"image":"example.com/spritz:latest"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "bcf6c03e-51a1-4f05-97d8-d616405b42a2")
	req.Header.Set("X-Spritz-User-Email", "bcf6c03e-51a1-4f05-97d8-d616405b42a2")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object in response, got %#v", payload["data"])
	}
	spec, ok := data["spec"].(map[string]any)
	if !ok {
		t.Fatalf("expected spec object in response, got %#v", data["spec"])
	}
	owner, ok := spec["owner"].(map[string]any)
	if !ok {
		t.Fatalf("expected owner object in response, got %#v", spec["owner"])
	}
	if owner["id"] != "bcf6c03e-51a1-4f05-97d8-d616405b42a2" {
		t.Fatalf("expected owner.id to be principal id, got %#v", owner["id"])
	}
	if _, exists := owner["email"]; exists {
		t.Fatalf("expected owner.email to be omitted from response, got %#v", owner["email"])
	}
}

func TestCreateSpritzRejectsOwnerIDMismatchForNonAdmin(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"name":"tidal-ember","spec":{"image":"example.com/spritz:latest","owner":{"id":"someone-else"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "current-user")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSuggestSpritzNameUsesPrefixFromRequest(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes/suggest-name", s.suggestSpritzName)

	body := []byte(`{"image":"example.com/spritz-openclaw:latest","namePrefix":"openclaw"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes/suggest-name", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object in response, got %#v", payload["data"])
	}
	name, _ := data["name"].(string)
	if name == "" {
		t.Fatal("expected generated name")
	}
	if !strings.HasPrefix(name, "openclaw-") {
		t.Fatalf("expected name prefix %q, got %q", "openclaw-", name)
	}
}

func TestCreateSpritzGeneratesPrefixedNameFromImage(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"spec":{"image":"example.com/spritz-claude-code:latest"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object in response, got %#v", payload["data"])
	}
	name, _ := data["metadata"].(map[string]any)["name"].(string)
	if name == "" {
		t.Fatal("expected generated metadata.name")
	}
	if !strings.HasPrefix(name, "claude-code-") {
		t.Fatalf("expected generated name to start with %q, got %q", "claude-code-", name)
	}
}
