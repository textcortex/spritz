package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

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
	spritz, ok := data["spritz"].(map[string]any)
	if !ok {
		t.Fatalf("expected spritz object in response, got %#v", data["spritz"])
	}
	spec, ok := spritz["spec"].(map[string]any)
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
	if data["accessUrl"] != "http://tidal-ember.spritz-test.svc.cluster.local:8080/c/tidal-ember" {
		t.Fatalf("expected accessUrl to prefer chat url, got %#v", data["accessUrl"])
	}
	if data["chatUrl"] != "http://tidal-ember.spritz-test.svc.cluster.local:8080/c/tidal-ember" {
		t.Fatalf("expected chatUrl in response, got %#v", data["chatUrl"])
	}
	if data["instanceUrl"] != "http://tidal-ember.spritz-test.svc.cluster.local:8080" {
		t.Fatalf("expected instanceUrl in response, got %#v", data["instanceUrl"])
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

func TestSuggestSpritzNameRejectsDisallowedProvisionerTargets(t *testing.T) {
	testCases := []struct {
		name       string
		configure  func(*server)
		body       []byte
		wantStatus int
		wantBody   string
	}{
		{
			name: "preset allowlist",
			configure: func(s *server) {
				configureProvisionerTestServer(s)
				s.presets = presetCatalog{
					byID: []runtimePreset{
						{ID: "openclaw", Name: "OpenClaw", Image: "example.com/spritz-openclaw:latest"},
						{ID: "claude-code", Name: "Claude Code", Image: "example.com/spritz-claude-code:latest"},
					},
				}
			},
			body:       []byte(`{"presetId":"claude-code"}`),
			wantStatus: http.StatusBadRequest,
			wantBody:   "preset is not allowed",
		},
		{
			name: "namespace override",
			configure: func(s *server) {
				configureProvisionerTestServer(s)
				s.namespace = ""
				s.controlNamespace = "spritz-system"
				s.provisioners.allowNamespaceOverride = true
				s.provisioners.allowedNamespaces = map[string]struct{}{"team-a": {}}
			},
			body:       []byte(`{"presetId":"openclaw","namespace":"team-b"}`),
			wantStatus: http.StatusBadRequest,
			wantBody:   "namespace is not allowed",
		},
		{
			name: "custom image",
			configure: func(s *server) {
				configureProvisionerTestServer(s)
			},
			body:       []byte(`{"image":"example.com/spritz-claude-code:latest"}`),
			wantStatus: http.StatusBadRequest,
			wantBody:   "custom image is not allowed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newCreateSpritzTestServer(t)
			tc.configure(s)
			e := echo.New()
			secured := e.Group("", s.authMiddleware())
			secured.POST("/api/spritzes/suggest-name", s.suggestSpritzName)

			req := httptest.NewRequest(http.MethodPost, "/api/spritzes/suggest-name", bytes.NewReader(tc.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			req.Header.Set("X-Spritz-User-Id", "zenobot")
			req.Header.Set("X-Spritz-Principal-Type", "service")
			req.Header.Set("X-Spritz-Principal-Scopes", scopeInstancesSuggestName)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("expected response body %q, got %s", tc.wantBody, rec.Body.String())
			}
		})
	}
}

func TestSuggestSpritzNameAllowsSameNamespaceWithoutTreatingItAsOverride(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes/suggest-name", s.suggestSpritzName)

	body := []byte(`{"presetId":"openclaw","namespace":"spritz-test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes/suggest-name", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeInstancesSuggestName)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSuggestSpritzNameUsesProvisionerDefaultPreset(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.defaultPresetID = "openclaw"
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes/suggest-name", s.suggestSpritzName)

	req := httptest.NewRequest(http.MethodPost, "/api/spritzes/suggest-name", bytes.NewReader([]byte(`{}`)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeInstancesSuggestName)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "openclaw-") {
		t.Fatalf("expected generated openclaw-prefixed suggestion, got %s", rec.Body.String())
	}
}

func TestSuggestSpritzNamePreservesPresetNamePrefix(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:         "openclaw",
			Name:       "OpenClaw",
			Image:      "example.com/spritz-openclaw:latest",
			NamePrefix: "discord-claw",
		}},
	}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes/suggest-name", s.suggestSpritzName)

	req := httptest.NewRequest(http.MethodPost, "/api/spritzes/suggest-name", bytes.NewReader([]byte(`{"presetId":"openclaw"}`)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeInstancesSuggestName)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "discord-claw-") {
		t.Fatalf("expected generated discord-claw-prefixed suggestion, got %s", rec.Body.String())
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
	spritz, ok := data["spritz"].(map[string]any)
	if !ok {
		t.Fatalf("expected spritz object in response, got %#v", data["spritz"])
	}
	name, _ := spritz["metadata"].(map[string]any)["name"].(string)
	if name == "" {
		t.Fatal("expected generated metadata.name")
	}
	if !strings.HasPrefix(name, "claude-code-") {
		t.Fatalf("expected generated name to start with %q, got %q", "claude-code-", name)
	}
}

func TestCreateSpritzAllowsProvisionerToAssignOwnerOnce(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-1","source":"discord","requestId":"cmd-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data := payload["data"].(map[string]any)
	if data["ownerId"] != "user-123" {
		t.Fatalf("expected ownerId user-123, got %#v", data["ownerId"])
	}
	if data["actorType"] != string(principalTypeService) {
		t.Fatalf("expected actorType service, got %#v", data["actorType"])
	}
	if data["presetId"] != "openclaw" {
		t.Fatalf("expected presetId openclaw, got %#v", data["presetId"])
	}

	spritzData := data["spritz"].(map[string]any)
	annotations := spritzData["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations[actorIDAnnotationKey] != "zenobot" {
		t.Fatalf("expected actor annotation, got %#v", annotations[actorIDAnnotationKey])
	}
	if annotations[idempotencyKeyAnnotationKey] != "discord-1" {
		t.Fatalf("expected idempotency annotation, got %#v", annotations[idempotencyKeyAnnotationKey])
	}
}

func TestCreateSpritzRejectsProvisionerWithoutOwnerID(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","idempotencyKey":"discord-missing-owner"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ownerId is required") {
		t.Fatalf("expected ownerId validation error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsHeaderScopeSpoofingForOwnerAssignment(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"name":"spoofed-owner","ownerId":"user-999","idempotencyKey":"discord-spoof","spec":{"image":"example.com/spritz-openclaw:latest"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-123")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSpritzRejectsProvisionerConflictingOwnerFields(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-owner-conflict","spec":{"owner":{"id":"user-999"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ownerId conflicts with spec.owner.id") {
		t.Fatalf("expected conflicting owner validation error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzReplaysIdempotentProvisionerRequest(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-2"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var firstPayload map[string]any
	if err := json.Unmarshal(rec1.Body.Bytes(), &firstPayload); err != nil {
		t.Fatalf("failed to decode first response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode replay response: %v", err)
	}
	firstName := firstPayload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["name"]
	replayedName := payload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["name"]
	if firstName != replayedName {
		t.Fatalf("expected idempotent replay to keep the same name, got first=%#v replay=%#v", firstName, replayedName)
	}
	data := payload["data"].(map[string]any)
	if replayed, _ := data["replayed"].(bool); !replayed {
		t.Fatalf("expected replayed response, got %#v", data["replayed"])
	}
}

func TestCreateSpritzReplaysIdempotentProvisionerRequestWhenRequestIDChanges(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	first := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-request-id","requestId":"discord-delivery-1"}`)
	second := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-request-id","requestId":"discord-delivery-2"}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(first))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(second))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateSpritzRejectsIdempotentProvisionerPayloadMismatch(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	first := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-3"}`)
	second := []byte(`{"presetId":"openclaw","ownerId":"user-999","idempotencyKey":"discord-3"}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(first))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(second))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected conflict status 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateSpritzRejectsIdempotentProvisionerRequestWhenNamePrefixChanges(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	first := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-prefix","namePrefix":"claude-code"}`)
	second := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-prefix","namePrefix":"openclaw"}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(first))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(second))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected conflict status 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateSpritzReplaysEquivalentProvisionerNamePrefixFormatting(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	first := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-format","namePrefix":"Claude Code"}`)
	second := []byte(`{"presetId":"OPENCLAW","ownerId":"user-123","idempotencyKey":"discord-format","namePrefix":"claude-code"}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(first))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(second))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateSpritzReplaysIdempotentProvisionerRequestBeforeQuotaCheck(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.maxActivePerOwner = 1
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-quota"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateSpritzRetriesGeneratedServiceNameAfterAlreadyExists(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	baseClient := s.client
	s.client = &createInterceptClient{
		Client: baseClient,
		onCreate: func(_ context.Context, obj client.Object) error {
			spritz, ok := obj.(*spritzv1.Spritz)
			if !ok {
				return nil
			}
			if spritz.Name == "openclaw-first" {
				return apierrors.NewAlreadyExists(schema.GroupResource{
					Group:    spritzv1.GroupVersion.Group,
					Resource: "spritzes",
				}, spritz.Name)
			}
			return nil
		},
	}
	s.nameGeneratorFactory = func(context.Context, string, string) (func() string, error) {
		names := []string{"openclaw-first", "openclaw-second"}
		index := 0
		return func() string {
			name := names[index]
			if index < len(names)-1 {
				index++
			}
			return name
		}, nil
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-race"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 after autogenerated name retry, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	name := payload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["name"]
	if name != "openclaw-second" {
		t.Fatalf("expected second generated name after race, got %#v", name)
	}
}

func TestCreateSpritzRejectsProvisionerLowLevelSpecFields(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"presetId":"openclaw",
		"ownerId":"user-123",
		"idempotencyKey":"discord-low-level",
		"spec":{
			"env":[{"name":"SHOULD_NOT_PASS","value":"1"}]
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spec.env is not allowed") {
		t.Fatalf("expected low-level spec validation error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsHumanProvidedServiceAccountName(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"tidal-ember",
		"spec":{
			"image":"example.com/spritz:latest",
			"serviceAccountName":"zeno-agent-ag-123"
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spec.serviceAccountName is reserved") {
		t.Fatalf("expected reserved service account error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsHumanReservedControlPlaneAnnotations(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"tidal-ember",
		"annotations":{
			"spritz.sh/preset-id":"zeno"
		},
		"spec":{
			"image":"example.com/spritz:latest"
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reserved control-plane keys") {
		t.Fatalf("expected reserved annotation error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzAllowsProvisionerServiceAccountNameAndCreatesServiceAccount(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"presetId":"openclaw",
		"ownerId":"user-123",
		"idempotencyKey":"discord-service-account",
		"spec":{
			"serviceAccountName":"zeno-agent-ag-123"
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
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
	spritzPayload, ok := data["spritz"].(map[string]any)
	if !ok {
		t.Fatalf("expected spritz object in response, got %#v", data["spritz"])
	}
	metadata, ok := spritzPayload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected spritz metadata object in response, got %#v", spritzPayload["metadata"])
	}
	spritzName, _ := metadata["name"].(string)
	if spritzName == "" {
		t.Fatalf("expected response spritz metadata.name, got %#v", metadata)
	}

	storedSpritz := &spritzv1.Spritz{}
	if err := s.client.Get(
		context.Background(),
		client.ObjectKey{Name: spritzName, Namespace: s.namespace},
		storedSpritz,
	); err != nil {
		t.Fatalf("expected created spritz resource: %v", err)
	}
	if storedSpritz.Spec.ServiceAccountName != "zeno-agent-ag-123" {
		t.Fatalf("expected spritz spec service account name, got %q", storedSpritz.Spec.ServiceAccountName)
	}

	serviceAccount := &corev1.ServiceAccount{}
	if err := s.client.Get(
		context.Background(),
		client.ObjectKey{Name: "zeno-agent-ag-123", Namespace: s.namespace},
		serviceAccount,
	); err != nil {
		t.Fatalf("expected created service account: %v", err)
	}
}

func TestCreateSpritzRejectsProvisionerUserConfigOverrides(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"presetId":"openclaw",
		"ownerId":"user-123",
		"idempotencyKey":"discord-user-config",
		"userConfig":{
			"repo":{"url":"https://example.com/private.git"}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "userConfig is not allowed") {
		t.Fatalf("expected service userConfig validation error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzAllowsProvisionerPresetWithInjectedEnv(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.presets.byID[0].Env = []corev1.EnvVar{{Name: "OPENCLAW_CONFIG_JSON", Value: "{}"}}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-preset-env"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected preset-backed service create to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListPresetsOmitsPresetEnvValues(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.presets.byID[0].Env = []corev1.EnvVar{{Name: "OPENCLAW_CONFIG_JSON", Value: `{"secret":"value"}`}}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/presets", s.listPresets)

	req := httptest.NewRequest(http.MethodGet, "/api/presets", nil)
	req.Header.Set("X-Spritz-User-Id", "user-123")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "OPENCLAW_CONFIG_JSON") || strings.Contains(rec.Body.String(), `"secret":"value"`) {
		t.Fatalf("expected preset response to omit env values, got %s", rec.Body.String())
	}
}

func TestListPresetsFiltersProvisionerAllowlistForServicePrincipal(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.presets.byID = append(s.presets.byID, runtimePreset{
		ID:         "claude-code",
		Name:       "Claude Code",
		Image:      "example.com/spritz-claude-code:latest",
		NamePrefix: "claude-code",
	})
	s.provisioners.allowedPresetIDs = map[string]struct{}{"openclaw": {}}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/presets", s.listPresets)

	req := httptest.NewRequest(http.MethodGet, "/api/presets", nil)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.presets.read")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "claude-code") {
		t.Fatalf("expected service preset list to exclude disallowed presets, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "openclaw") {
		t.Fatalf("expected service preset list to include allowed preset, got %s", rec.Body.String())
	}
}

func TestCreateSpritzUsesProvisionerDefaultPresetWhenPresetOmitted(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.defaultPresetID = "openclaw"
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"ownerId":"user-123","idempotencyKey":"discord-default-preset"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data := payload["data"].(map[string]any)
	if presetID := data["presetId"]; presetID != "openclaw" {
		t.Fatalf("expected presetId openclaw, got %#v", presetID)
	}
	spritz := data["spritz"].(map[string]any)
	metadata := spritz["metadata"].(map[string]any)
	if name, _ := metadata["name"].(string); !strings.HasPrefix(name, "openclaw-") {
		t.Fatalf("expected default preset name prefix, got %#v", name)
	}
	spec := spritz["spec"].(map[string]any)
	if image := spec["image"]; image != "example.com/spritz-openclaw:latest" {
		t.Fatalf("expected preset image, got %#v", image)
	}
}

func TestCreateSpritzReplaysIdempotentProvisionerRequestAfterDefaultPresetChanges(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.defaultPresetID = "openclaw"
	s.presets = presetCatalog{
		byID: []runtimePreset{
			{
				ID:         "openclaw",
				Name:       "OpenClaw",
				Image:      "example.com/spritz-openclaw:latest",
				NamePrefix: "openclaw",
			},
			{
				ID:         "claude-code",
				Name:       "Claude Code",
				Image:      "example.com/spritz-claude-code:latest",
				NamePrefix: "claude-code",
			},
		},
	}
	s.provisioners.allowedPresetIDs = map[string]struct{}{"openclaw": {}, "claude-code": {}}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"ownerId":"user-123","idempotencyKey":"discord-default-shift"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	s.provisioners.defaultPresetID = "claude-code"

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200 after default preset change, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "\"presetId\":\"openclaw\"") {
		t.Fatalf("expected replay to return the original openclaw spritz, got %s", rec2.Body.String())
	}
}

func TestCreateSpritzRejectsLegacyCompletedProvisionerRequestAfterDefaultPresetChanges(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.defaultPresetID = "openclaw"
	s.presets = presetCatalog{
		byID: []runtimePreset{
			{ID: "openclaw", Name: "OpenClaw", Image: "example.com/spritz-openclaw:latest", NamePrefix: "openclaw"},
			{ID: "claude-code", Name: "Claude Code", Image: "example.com/spritz-claude-code:latest", NamePrefix: "claude-code"},
		},
	}
	s.provisioners.allowedPresetIDs = map[string]struct{}{"openclaw": {}, "claude-code": {}}

	requestBody := createRequest{OwnerID: "user-123", IdempotencyKey: "discord-legacy-completed"}
	applyTopLevelCreateFields(&requestBody)
	owner, err := normalizeCreateOwner(&requestBody, principal{ID: "zenobot", Type: principalTypeService}, s.auth.enabled())
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	requestBody.Spec.Owner = owner
	s.applyProvisionerDefaultPreset(&requestBody, principal{ID: "zenobot", Type: principalTypeService})
	if _, err := s.applyCreatePreset(&requestBody); err != nil {
		t.Fatalf("applyCreatePreset failed: %v", err)
	}
	if err := resolveCreateLifetimes(&requestBody.Spec, s.provisioners, true); err != nil {
		t.Fatalf("resolveCreateLifetimes failed: %v", err)
	}
	legacyFingerprint, err := s.resolvedCreateFingerprint(requestBody, s.namespace, "", nil)
	if err != nil {
		t.Fatalf("resolvedCreateFingerprint failed: %v", err)
	}

	if err := s.client.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName("zenobot", "discord-legacy-completed"),
			Namespace: s.namespace,
		},
		Data: map[string]string{
			idempotencyReservationHashKey: legacyFingerprint,
			idempotencyReservationNameKey: "openclaw-legacy",
			idempotencyReservationDoneKey: "true",
		},
	}); err != nil {
		t.Fatalf("failed to seed legacy reservation: %v", err)
	}
	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-legacy",
			Namespace: s.namespace,
			Annotations: map[string]string{
				idempotencyHashAnnotationKey: legacyFingerprint,
				idempotencyKeyAnnotationKey:  "discord-legacy-completed",
				actorIDAnnotationKey:         "zenobot",
				presetIDAnnotationKey:        "openclaw",
				sourceAnnotationKey:          "external",
			},
		},
		Spec: requestBody.Spec,
	}); err != nil {
		t.Fatalf("failed to seed legacy spritz: %v", err)
	}

	s.provisioners.defaultPresetID = "claude-code"

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"ownerId":"user-123","idempotencyKey":"discord-legacy-completed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected strict-cutover conflict for legacy completed reservation, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "idempotencyKey already used with a different request") {
		t.Fatalf("expected legacy completed reservation to fail closed, got %s", rec.Body.String())
	}
}

func TestCreateSpritzUsesPendingReservationPayloadAfterDefaultPresetChanges(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.defaultPresetID = "openclaw"
	s.nameGeneratorFactory = func(_ context.Context, _ string, prefix string) (func() string, error) {
		names := []string{prefix + "-retry-one", prefix + "-retry-two"}
		index := 0
		return func() string {
			name := names[index]
			if index < len(names)-1 {
				index++
			}
			return name
		}, nil
	}
	s.presets = presetCatalog{
		byID: []runtimePreset{
			{
				ID:         "openclaw",
				Name:       "OpenClaw",
				Image:      "example.com/spritz-openclaw:latest",
				NamePrefix: "openclaw",
			},
			{
				ID:         "claude-code",
				Name:       "Claude Code",
				Image:      "example.com/spritz-claude-code:latest",
				NamePrefix: "claude-code",
			},
		},
	}
	s.provisioners.allowedPresetIDs = map[string]struct{}{"openclaw": {}, "claude-code": {}}

	requestBody := createRequest{
		OwnerID:        "user-123",
		IdempotencyKey: "discord-pending-default-shift",
	}
	applyTopLevelCreateFields(&requestBody)
	owner, err := normalizeCreateOwner(&requestBody, principal{ID: "zenobot", Type: principalTypeService}, s.auth.enabled())
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	requestBody.Spec.Owner = owner
	requestFingerprintBody := requestBody
	s.applyProvisionerDefaultPreset(&requestBody, principal{ID: "zenobot", Type: principalTypeService})
	if _, err := s.applyCreatePreset(&requestBody); err != nil {
		t.Fatalf("applyCreatePreset failed: %v", err)
	}
	if err := resolveCreateLifetimes(&requestBody.Spec, s.provisioners, true); err != nil {
		t.Fatalf("resolveCreateLifetimes failed: %v", err)
	}
	fingerprint, err := createRequestFingerprint(requestFingerprintBody, s.namespace, "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	resolvedPayload, err := createResolvedProvisionerPayload(requestBody, s.resolvedCreateNamePrefix(requestBody, requestFingerprintBody.NamePrefix), nil)
	if err != nil {
		t.Fatalf("createResolvedProvisionerPayload failed: %v", err)
	}
	if err := s.client.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName("zenobot", requestBody.IdempotencyKey),
			Namespace: s.namespace,
		},
		Data: map[string]string{
			idempotencyReservationHashKey: fingerprint,
			idempotencyReservationNameKey: "openclaw-pending-default",
			idempotencyReservationDoneKey: "false",
			idempotencyReservationBodyKey: resolvedPayload,
		},
	}); err != nil {
		t.Fatalf("failed to seed reservation: %v", err)
	}
	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-pending-default",
			Namespace: s.namespace,
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-other:latest",
			Owner: spritzv1.SpritzOwner{ID: "someone-else"},
		},
	}); err != nil {
		t.Fatalf("failed to seed conflicting pending name: %v", err)
	}

	s.provisioners.defaultPresetID = "claude-code"
	s.presets.byID[0].NamePrefix = "newprefix"
	s.provisioners.maxTTL = 24 * time.Hour

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"ownerId":"user-123","idempotencyKey":"discord-pending-default-shift"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"presetId\":\"openclaw\"") {
		t.Fatalf("expected pending replay to keep the original openclaw preset, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"image\":\"example.com/spritz-openclaw:latest\"") {
		t.Fatalf("expected pending replay to create the original openclaw image, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"name\":\"openclaw-retry-one\"") {
		t.Fatalf("expected pending replay to keep the original openclaw name prefix, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"ttl\":\"168h0m0s\"") {
		t.Fatalf("expected pending replay to keep the original ttl despite stricter current policy, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsPendingReservationWithoutPayload(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.defaultPresetID = "openclaw"
	s.presets = presetCatalog{
		byID: []runtimePreset{
			{ID: "openclaw", Name: "OpenClaw", Image: "example.com/spritz-openclaw:latest", NamePrefix: "openclaw"},
			{ID: "claude-code", Name: "Claude Code", Image: "example.com/spritz-claude-code:latest", NamePrefix: "claude-code"},
		},
	}
	s.provisioners.allowedPresetIDs = map[string]struct{}{"openclaw": {}, "claude-code": {}}

	requestBody := createRequest{
		OwnerID:        "user-123",
		IdempotencyKey: "discord-pending-without-payload",
	}
	applyTopLevelCreateFields(&requestBody)
	owner, err := normalizeCreateOwner(&requestBody, principal{ID: "zenobot", Type: principalTypeService}, s.auth.enabled())
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	requestBody.Spec.Owner = owner
	requestFingerprintBody := requestBody
	currentFingerprint, err := createRequestFingerprint(requestFingerprintBody, s.namespace, "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	if err := s.client.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName("zenobot", requestBody.IdempotencyKey),
			Namespace: s.namespace,
		},
		Data: map[string]string{
			idempotencyReservationHashKey: currentFingerprint,
			idempotencyReservationNameKey: "openclaw-cutover",
			idempotencyReservationDoneKey: "false",
		},
	}); err != nil {
		t.Fatalf("failed to seed reservation: %v", err)
	}

	s.provisioners.defaultPresetID = "claude-code"

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"ownerId":"user-123","idempotencyKey":"discord-pending-without-payload"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected strict-cutover conflict for legacy pending reservation, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "idempotencyKey already used by an incompatible pending request") {
		t.Fatalf("expected legacy pending reservation to fail closed, got %s", rec.Body.String())
	}
}

func TestCreateSpritzAllowsProvisionerCurrentNamespaceWithoutOverride(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-current-ns","namespace":"spritz-test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSpritzKeepsResponseMetadataIndependentFromHumanAnnotations(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"tidal-ember",
		"annotations":{
			"spritz.sh/source":"spoofed-source",
			"spritz.sh/idempotency-key":"spoofed-key"
		},
		"spec":{"image":"example.com/spritz:latest"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	responseBody := rec.Body.String()
	if strings.Contains(responseBody, "\"source\":\"spoofed-source\"") {
		t.Fatalf("expected response source to ignore caller annotations, got %s", responseBody)
	}
	if strings.Contains(responseBody, "\"idempotencyKey\":\"spoofed-key\"") {
		t.Fatalf("expected response idempotencyKey to ignore caller annotations, got %s", responseBody)
	}
}

func TestCreateSpritzRejectsExplicitNamespaceForProvisionerWhenOverrideDisabled(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.namespace = ""
	s.controlNamespace = "spritz-system"
	s.provisioners.allowedNamespaces = map[string]struct{}{"team-a": {}}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-ns-override","namespace":"team-a"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "namespace override is not allowed") {
		t.Fatalf("expected namespace override error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsUnboundedNamespaceOverrideWhenQuotaEnforced(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.namespace = ""
	s.controlNamespace = "spritz-system"
	s.provisioners.allowNamespaceOverride = true
	s.provisioners.allowedNamespaces = nil
	s.provisioners.maxActivePerOwner = 1

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-global-override","namespace":"team-b"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "quota enforcement requires allowed namespaces when namespace override is enabled") {
		t.Fatalf("expected unbounded override quota error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsProvisionerIdempotencyReuseAcrossNamespaces(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.namespace = ""
	s.controlNamespace = "spritz-system"
	s.provisioners.allowNamespaceOverride = true
	s.provisioners.allowedNamespaces = map[string]struct{}{"team-a": {}, "team-b": {}}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	first := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-cross-ns","namespace":"team-a"}`)
	second := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-cross-ns","namespace":"team-b"}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(first))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create to succeed, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(second))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "idempotencyKey already used with a different request") {
		t.Fatalf("expected idempotency conflict, got %s", rec2.Body.String())
	}
}

func TestCreateSpritzEnforcesProvisionerActiveQuotaAcrossAllowedNamespaces(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.namespace = ""
	s.controlNamespace = "spritz-system"
	s.provisioners.allowNamespaceOverride = true
	s.provisioners.allowedNamespaces = map[string]struct{}{"team-a": {}, "team-b": {}}
	s.provisioners.maxActivePerOwner = 1

	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-existing",
			Namespace: "team-a",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}); err != nil {
		t.Fatalf("failed to seed existing spritz: %v", err)
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-cross-ns-active","namespace":"team-b"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "owner active instance limit reached") {
		t.Fatalf("expected active quota error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzIgnoresOtherNamespacesWhenOverrideDisabled(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.namespace = ""
	s.controlNamespace = "spritz-system"
	s.provisioners.allowNamespaceOverride = false
	s.provisioners.maxActivePerOwner = 1

	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-other-namespace",
			Namespace: "team-a",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}); err != nil {
		t.Fatalf("failed to seed unrelated namespace spritz: %v", err)
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-default-ns"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSpritzEnforcesProvisionerActorRateAcrossAllowedNamespaces(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.namespace = ""
	s.controlNamespace = "spritz-system"
	s.provisioners.allowNamespaceOverride = true
	s.provisioners.allowedNamespaces = map[string]struct{}{"team-a": {}, "team-b": {}}
	s.provisioners.maxCreatesPerActor = 1

	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "openclaw-actor-existing",
			Namespace:         "team-a",
			CreationTimestamp: metav1.NewTime(time.Now()),
			Annotations: map[string]string{
				actorIDAnnotationKey: "zenobot",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-456"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}); err != nil {
		t.Fatalf("failed to seed actor-rate spritz: %v", err)
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-cross-ns-actor","namespace":"team-b"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "actor create rate limit reached") {
		t.Fatalf("expected actor rate error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRetriesPendingIdempotencyReservationWithConflictingOccupant(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.nameGeneratorFactory = func(context.Context, string, string) (func() string, error) {
		return func() string {
			return "openclaw-fresh-name"
		}, nil
	}

	body := createRequest{
		OwnerID:        "user-123",
		IdempotencyKey: "discord-pending",
		PresetID:       "openclaw",
	}
	applyTopLevelCreateFields(&body)
	owner, err := normalizeCreateOwner(&body, principal{ID: "zenobot", Type: principalTypeService}, s.auth.enabled())
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	body.Spec.Owner = owner
	requestFingerprintBody := body
	if _, err := s.applyCreatePreset(&body); err != nil {
		t.Fatalf("applyCreatePreset failed: %v", err)
	}
	if err := resolveCreateLifetimes(&body.Spec, s.provisioners, true); err != nil {
		t.Fatalf("resolveCreateLifetimes failed: %v", err)
	}
	fingerprint, err := createRequestFingerprint(requestFingerprintBody, s.namespace, "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	resolvedPayload, err := createResolvedProvisionerPayload(body, s.resolvedCreateNamePrefix(body, requestFingerprintBody.NamePrefix), nil)
	if err != nil {
		t.Fatalf("createResolvedProvisionerPayload failed: %v", err)
	}

	conflictingName := "openclaw-blocked-name"
	if err := s.client.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName("zenobot", body.IdempotencyKey),
			Namespace: s.namespace,
		},
		Data: map[string]string{
			idempotencyReservationHashKey: fingerprint,
			idempotencyReservationNameKey: conflictingName,
			idempotencyReservationDoneKey: "false",
			idempotencyReservationBodyKey: resolvedPayload,
		},
	}); err != nil {
		t.Fatalf("failed to seed reservation: %v", err)
	}
	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      conflictingName,
			Namespace: s.namespace,
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-other:latest",
			Owner: spritzv1.SpritzOwner{ID: "someone-else"},
		},
	}); err != nil {
		t.Fatalf("failed to seed conflicting spritz: %v", err)
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	reqBody := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-pending"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(reqBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	spritz := payload["data"].(map[string]any)["spritz"].(map[string]any)
	metadata := spritz["metadata"].(map[string]any)
	if name := metadata["name"]; name == conflictingName {
		t.Fatalf("expected create to move past the poisoned reservation name, got %#v", name)
	}
}

func TestCreateSpritzReplaysPendingIdempotentCreateBeforeQuotaCheck(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.provisioners.maxActivePerOwner = 1
	s.nameGeneratorFactory = func(context.Context, string, string) (func() string, error) {
		return func() string {
			return "openclaw-fixed"
		}, nil
	}

	body := createRequest{
		OwnerID:        "user-123",
		IdempotencyKey: "discord-pending",
		PresetID:       "openclaw",
	}
	applyTopLevelCreateFields(&body)
	owner, err := normalizeCreateOwner(&body, principal{ID: "zenobot", Type: principalTypeService}, s.auth.enabled())
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	body.Spec.Owner = owner
	requestFingerprintBody := body
	if _, err := s.applyCreatePreset(&body); err != nil {
		t.Fatalf("applyCreatePreset failed: %v", err)
	}
	if err := resolveCreateLifetimes(&body.Spec, s.provisioners, true); err != nil {
		t.Fatalf("resolveCreateLifetimes failed: %v", err)
	}
	fingerprint, err := createRequestFingerprint(requestFingerprintBody, s.namespace, "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	resolvedPayload, err := createResolvedProvisionerPayload(body, s.resolvedCreateNamePrefix(body, requestFingerprintBody.NamePrefix), nil)
	if err != nil {
		t.Fatalf("createResolvedProvisionerPayload failed: %v", err)
	}

	if err := s.client.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName("zenobot", body.IdempotencyKey),
			Namespace: s.namespace,
		},
		Data: map[string]string{
			idempotencyReservationHashKey: fingerprint,
			idempotencyReservationNameKey: "openclaw-fixed",
			idempotencyReservationDoneKey: "false",
			idempotencyReservationBodyKey: resolvedPayload,
		},
	}); err != nil {
		t.Fatalf("failed to seed reservation: %v", err)
	}
	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-fixed",
			Namespace: s.namespace,
			Annotations: map[string]string{
				idempotencyHashAnnotationKey: fingerprint,
				idempotencyKeyAnnotationKey:  body.IdempotencyKey,
				actorIDAnnotationKey:         "zenobot",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}); err != nil {
		t.Fatalf("failed to seed spritz: %v", err)
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	reqBody := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-pending"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(reqBody))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 replay, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data := payload["data"].(map[string]any)
	if replayed, _ := data["replayed"].(bool); !replayed {
		t.Fatalf("expected replayed response, got %#v", data["replayed"])
	}
	spritz := data["spritz"].(map[string]any)
	metadata := spritz["metadata"].(map[string]any)
	if metadata["name"] != "openclaw-fixed" {
		t.Fatalf("expected replay to return existing spritz, got %#v", metadata["name"])
	}
}

func TestSetIdempotencyReservationNameKeepsSinglePendingCandidate(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)

	body := createRequest{
		OwnerID:        "user-123",
		IdempotencyKey: "discord-pending-single-name",
		PresetID:       "openclaw",
	}
	applyTopLevelCreateFields(&body)
	owner, err := normalizeCreateOwner(&body, principal{ID: "zenobot", Type: principalTypeService}, s.auth.enabled())
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	body.Spec.Owner = owner
	requestFingerprintBody := body
	if _, err := s.applyCreatePreset(&body); err != nil {
		t.Fatalf("applyCreatePreset failed: %v", err)
	}
	if err := resolveCreateLifetimes(&body.Spec, s.provisioners, true); err != nil {
		t.Fatalf("resolveCreateLifetimes failed: %v", err)
	}
	fingerprint, err := createRequestFingerprint(requestFingerprintBody, s.namespace, "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	state := provisionerIdempotencyState{
		canonicalFingerprint: fingerprint,
		resolvedPayload:      "payload",
	}

	if err := s.client.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyReservationName("zenobot", body.IdempotencyKey),
			Namespace: s.namespace,
		},
		Data: map[string]string{
			idempotencyReservationHashKey: fingerprint,
			idempotencyReservationNameKey: "openclaw-blocked",
			idempotencyReservationDoneKey: "false",
			idempotencyReservationBodyKey: "payload",
		},
	}); err != nil {
		t.Fatalf("failed to seed reservation: %v", err)
	}
	if err := s.client.Create(context.Background(), &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-blocked",
			Namespace: s.namespace,
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-other:latest",
			Owner: spritzv1.SpritzOwner{ID: "someone-else"},
		},
	}); err != nil {
		t.Fatalf("failed to seed conflicting spritz: %v", err)
	}

	firstName, done, _, err := s.setIdempotencyReservationName(context.Background(), "zenobot", body.IdempotencyKey, "openclaw-blocked", "openclaw-alpha", state)
	if err != nil {
		t.Fatalf("first reservation update failed: %v", err)
	}
	if done {
		t.Fatal("expected reservation to remain pending")
	}
	if firstName != "openclaw-alpha" {
		t.Fatalf("expected first replacement name %q, got %q", "openclaw-alpha", firstName)
	}

	secondName, done, _, err := s.setIdempotencyReservationName(context.Background(), "zenobot", body.IdempotencyKey, "openclaw-blocked", "openclaw-beta", state)
	if err != nil {
		t.Fatalf("second reservation update failed: %v", err)
	}
	if done {
		t.Fatal("expected reservation to remain pending")
	}
	if secondName != "openclaw-alpha" {
		t.Fatalf("expected second caller to reuse %q, got %q", "openclaw-alpha", secondName)
	}

	reservation := &corev1.ConfigMap{}
	if err := s.client.Get(context.Background(), clientKey(s.namespace, idempotencyReservationName("zenobot", body.IdempotencyKey)), reservation); err != nil {
		t.Fatalf("failed to reload reservation: %v", err)
	}
	if got := strings.TrimSpace(reservation.Data[idempotencyReservationNameKey]); got != "openclaw-alpha" {
		t.Fatalf("expected reservation to stay on %q, got %q", "openclaw-alpha", got)
	}
}

func TestCreateSpritzDoesNotReplayDifferentActorOrKeyForSameNamedInstance(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	first := []byte(`{"name":"openclaw-fixed","presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-a"}`)
	second := []byte(`{"name":"openclaw-fixed","presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-b"}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(first))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot-a")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create to succeed, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(second))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot-b")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestCreateSpritzReplaysGeneratedNameAfterCompletionFailure(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	s.nameGeneratorFactory = func(context.Context, string, string) (func() string, error) {
		names := []string{"openclaw-replayable"}
		index := 0
		return func() string {
			if index >= len(names) {
				return names[len(names)-1]
			}
			value := names[index]
			index++
			return value
		}, nil
	}

	baseClient := s.client
	failComplete := true
	s.client = &createInterceptClient{
		Client: baseClient,
		onUpdate: func(_ context.Context, obj client.Object) error {
			configMap, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return nil
			}
			if strings.HasPrefix(configMap.Name, idempotencyReservationPrefix) &&
				failComplete &&
				strings.EqualFold(strings.TrimSpace(configMap.Data[idempotencyReservationDoneKey]), "true") {
				failComplete = false
				return errors.New("forced completion failure")
			}
			return nil
		},
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	reqBody := []byte(`{"presetId":"openclaw","ownerId":"user-123","idempotencyKey":"discord-generated-replay"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(reqBody))
	req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req1.Header.Set("X-Spritz-User-Id", "zenobot")
	req1.Header.Set("X-Spritz-Principal-Type", "service")
	req1.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec1 := httptest.NewRecorder()
	e.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusInternalServerError {
		t.Fatalf("expected first create to fail after instance creation, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(reqBody))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req2.Header.Set("X-Spritz-User-Id", "zenobot")
	req2.Header.Set("X-Spritz-Principal-Type", "service")
	req2.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected retry to replay created instance, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data := payload["data"].(map[string]any)
	if replayed, _ := data["replayed"].(bool); !replayed {
		t.Fatalf("expected replayed response, got %#v", data["replayed"])
	}
}
