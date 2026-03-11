package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newTestSpritzScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register spritz scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register core scheme: %v", err)
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
			mode:              authModeHeader,
			headerID:          "X-Spritz-User-Id",
			headerEmail:       "X-Spritz-User-Email",
			headerType:        "X-Spritz-Principal-Type",
			headerScopes:      "X-Spritz-Principal-Scopes",
			headerDefaultType: principalTypeHuman,
		},
		internalAuth:     internalAuthConfig{enabled: false},
		userConfigPolicy: userConfigPolicy{},
	}
}

type createInterceptClient struct {
	client.Client
	onCreate func(context.Context, client.Object) error
}

func (c *createInterceptClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.onCreate != nil {
		if err := c.onCreate(ctx, obj); err != nil {
			return err
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func configureProvisionerTestServer(s *server) {
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:         "openclaw",
			Name:       "OpenClaw",
			Image:      "example.com/spritz-openclaw:latest",
			NamePrefix: "openclaw",
		}},
	}
	s.provisioners = provisionerPolicy{
		allowedPresetIDs: map[string]struct{}{"openclaw": {}},
		defaultIdleTTL:   24 * time.Hour,
		maxIdleTTL:       24 * time.Hour,
		defaultTTL:       168 * time.Hour,
		maxTTL:           168 * time.Hour,
		rateWindow:       time.Hour,
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
	if _, err := s.applyCreatePreset(&body); err != nil {
		t.Fatalf("applyCreatePreset failed: %v", err)
	}
	if err := resolveCreateLifetimes(&body.Spec, s.provisioners, true); err != nil {
		t.Fatalf("resolveCreateLifetimes failed: %v", err)
	}
	fingerprint, err := createFingerprint(body.Spec.Owner.ID, body.PresetID, "", s.namespace, provisionerSource(&body), body.Spec, nil)
	if err != nil {
		t.Fatalf("createFingerprint failed: %v", err)
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
