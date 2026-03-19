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
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

func configurePresetResolverTestServer(s *server, resolverURL string) {
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:            "zeno",
			Name:          "Zeno",
			Image:         "example.com/zeno:latest",
			NamePrefix:    "zeno",
			InstanceClass: "personal-agent",
		}},
	}
	s.instanceClasses = instanceClassCatalog{
		byID: map[string]instanceClass{
			"personal-agent": {
				ID:      "personal-agent",
				Version: "v1",
				Creation: instanceClassCreationPolicy{
					RequireOwner:           true,
					RequiredResolvedFields: []string{requiredResolvedFieldServiceAccountName},
				},
			},
		},
	}
	if strings.TrimSpace(resolverURL) != "" {
		s.extensions = extensionRegistry{
			resolvers: []configuredResolver{{
				id:        "runtime-binding",
				kind:      extensionKindResolver,
				operation: extensionOperationPresetCreateResolve,
				match: extensionMatchRule{
					presetIDs: map[string]struct{}{"zeno": {}},
				},
				transport: configuredHTTPTransport{
					url:     resolverURL,
					timeout: time.Second,
				},
			}},
		}
	}
}

func TestCreateSpritzAppliesPresetCreateResolverForHumanCaller(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	var received map[string]any
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode resolver request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "resolved",
			"mutations": map[string]any{
				"spec": map[string]any{
					"serviceAccountName": "zeno-agent-ag-123",
				},
				"annotations": map[string]string{
					"spritz.sh/resolved-agent-id": "ag-123",
				},
			},
		})
	}))
	defer resolver.Close()
	configurePresetResolverTestServer(s, resolver.URL)

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"zeno-lake",
		"presetId":"zeno",
		"presetInputs":{"agentId":"ag-123"},
		"spec":{}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if received["operation"] != string(extensionOperationPresetCreateResolve) {
		t.Fatalf("expected preset create operation, got %#v", received["operation"])
	}
	contextPayload, ok := received["context"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolver context payload, got %#v", received["context"])
	}
	if contextPayload["presetId"] != "zeno" {
		t.Fatalf("expected resolver presetId zeno, got %#v", contextPayload["presetId"])
	}
	inputPayload, ok := received["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolver input payload, got %#v", received["input"])
	}
	presetInputs, ok := inputPayload["presetInputs"].(map[string]any)
	if !ok {
		t.Fatalf("expected preset inputs payload, got %#v", inputPayload["presetInputs"])
	}
	if presetInputs["agentId"] != "ag-123" {
		t.Fatalf("expected preset input agentId ag-123, got %#v", presetInputs["agentId"])
	}

	stored := &spritzv1.Spritz{}
	if err := s.client.Get(context.Background(), client.ObjectKey{Name: "zeno-lake", Namespace: s.namespace}, stored); err != nil {
		t.Fatalf("expected created spritz resource: %v", err)
	}
	if stored.Spec.ServiceAccountName != "zeno-agent-ag-123" {
		t.Fatalf("expected resolved service account name, got %q", stored.Spec.ServiceAccountName)
	}
	if stored.Annotations["spritz.sh/resolved-agent-id"] != "ag-123" {
		t.Fatalf("expected resolver annotation, got %#v", stored.Annotations["spritz.sh/resolved-agent-id"])
	}
	if stored.Annotations[instanceClassAnnotationKey] != "personal-agent" {
		t.Fatalf("expected instance class annotation, got %#v", stored.Annotations[instanceClassAnnotationKey])
	}
	if stored.Annotations[instanceClassVersionAnnotationKey] != "v1" {
		t.Fatalf("expected instance class version annotation, got %#v", stored.Annotations[instanceClassVersionAnnotationKey])
	}
	serviceAccount := &corev1.ServiceAccount{}
	if err := s.client.Get(context.Background(), client.ObjectKey{Name: "zeno-agent-ag-123", Namespace: s.namespace}, serviceAccount); err != nil {
		t.Fatalf("expected created service account: %v", err)
	}
}

func TestCreateSpritzRejectsPresetInputsWithoutMatchingResolver(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configurePresetResolverTestServer(s, "")
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"zeno-lake",
		"presetId":"zeno",
		"presetInputs":{"agentId":"ag-123"},
		"spec":{}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "matching preset create resolver") {
		t.Fatalf("expected preset resolver error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzRejectsMissingRequiredResolvedFieldFromInstanceClass(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:            "zeno",
			Name:          "Zeno",
			Image:         "example.com/zeno:latest",
			NamePrefix:    "zeno",
			InstanceClass: "personal-agent",
		}},
	}
	s.instanceClasses = instanceClassCatalog{
		byID: map[string]instanceClass{
			"personal-agent": {
				ID:      "personal-agent",
				Version: "v1",
				Creation: instanceClassCreationPolicy{
					RequireOwner:           true,
					RequiredResolvedFields: []string{requiredResolvedFieldServiceAccountName},
				},
			},
		},
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"zeno-lake",
		"presetId":"zeno",
		"spec":{}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), requiredResolvedFieldServiceAccountName) {
		t.Fatalf("expected required resolved field error, got %s", rec.Body.String())
	}
}

func TestCreateSpritzProvisionerPresetResolverReplaysWithResolvedBinding(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	var requestCount int
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "resolved",
			"mutations": map[string]any{
				"spec": map[string]any{
					"serviceAccountName": "zeno-agent-ag-123",
				},
				"annotations": map[string]string{
					"spritz.sh/resolved-agent-id": "ag-123",
				},
			},
		})
	}))
	defer resolver.Close()
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:            "zeno",
			Name:          "Zeno",
			Image:         "example.com/zeno:latest",
			NamePrefix:    "zeno",
			InstanceClass: "personal-agent",
		}},
	}
	s.provisioners.allowedPresetIDs = map[string]struct{}{"zeno": {}}
	s.instanceClasses = instanceClassCatalog{
		byID: map[string]instanceClass{
			"personal-agent": {
				ID:      "personal-agent",
				Version: "v1",
				Creation: instanceClassCreationPolicy{
					RequireOwner:           true,
					RequiredResolvedFields: []string{requiredResolvedFieldServiceAccountName},
				},
			},
		},
	}
	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{{
			id:        "runtime-binding",
			kind:      extensionKindResolver,
			operation: extensionOperationPresetCreateResolve,
			match: extensionMatchRule{
				presetIDs: map[string]struct{}{"zeno": {}},
			},
			transport: configuredHTTPTransport{
				url:     resolver.URL,
				timeout: time.Second,
			},
		}},
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"presetId":"zeno",
		"presetInputs":{"agentId":"ag-123"},
		"ownerId":"user-123",
		"idempotencyKey":"discord-preset-resolver",
		"spec":{}
	}`)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set("X-Spritz-User-Id", "zenobot")
		req.Header.Set("X-Spritz-Principal-Type", "service")
		req.Header.Set("X-Spritz-Principal-Scopes", "spritz.instances.create,spritz.instances.assign_owner")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	first := request()
	if first.Code != http.StatusCreated {
		t.Fatalf("expected status 201 on first create, got %d: %s", first.Code, first.Body.String())
	}
	second := request()
	if second.Code != http.StatusOK {
		t.Fatalf("expected status 200 on replay, got %d: %s", second.Code, second.Body.String())
	}
	if requestCount != 1 {
		t.Fatalf("expected resolver to run once across idempotent replay, got %d", requestCount)
	}
	var payload map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode first response: %v", err)
	}
	name := payload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["name"].(string)
	stored := &spritzv1.Spritz{}
	if err := s.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: s.namespace}, stored); err != nil {
		t.Fatalf("expected created spritz resource: %v", err)
	}
	if stored.Spec.ServiceAccountName != "zeno-agent-ag-123" {
		t.Fatalf("expected resolved service account name, got %q", stored.Spec.ServiceAccountName)
	}
	if stored.Annotations["spritz.sh/resolved-agent-id"] != "ag-123" {
		t.Fatalf("expected resolver annotation, got %#v", stored.Annotations["spritz.sh/resolved-agent-id"])
	}
}

func TestCreateSpritzProvisionerRejectsDisallowedPresetBeforeResolver(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	var requestCount int
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		defer r.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	defer resolver.Close()
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:            "zeno",
			Name:          "Zeno",
			Image:         "example.com/zeno:latest",
			NamePrefix:    "zeno",
			InstanceClass: "personal-agent",
		}},
	}
	s.provisioners.allowedPresetIDs = map[string]struct{}{"openclaw": {}}
	s.instanceClasses = instanceClassCatalog{
		byID: map[string]instanceClass{
			"personal-agent": {
				ID:      "personal-agent",
				Version: "v1",
				Creation: instanceClassCreationPolicy{
					RequireOwner:           true,
					RequiredResolvedFields: []string{requiredResolvedFieldServiceAccountName},
				},
			},
		},
	}
	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{{
			id:        "runtime-binding",
			kind:      extensionKindResolver,
			operation: extensionOperationPresetCreateResolve,
			match: extensionMatchRule{
				presetIDs: map[string]struct{}{"zeno": {}},
			},
			transport: configuredHTTPTransport{
				url:     resolver.URL,
				timeout: time.Second,
			},
		}},
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"presetId":"zeno",
		"presetInputs":{"agentId":"ag-123"},
		"ownerId":"user-123",
		"idempotencyKey":"disallowed-preset",
		"spec":{}
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
	if requestCount != 0 {
		t.Fatalf("expected resolver not to run for disallowed preset, got %d requests", requestCount)
	}
}

func TestCreateRequestFingerprintIncludesPresetInputs(t *testing.T) {
	base := createRequest{
		OwnerID:      "user-123",
		PresetID:     "zeno",
		PresetInputs: json.RawMessage(`{"agentId":"ag-123"}`),
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
	}
	first, err := createRequestFingerprint(base, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	base.PresetInputs = json.RawMessage(`{"agentId":"ag-456"}`)
	second, err := createRequestFingerprint(base, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}
	if first == second {
		t.Fatal("expected presetInputs to affect create fingerprint")
	}
}

func TestNormalizePresetInputsPreservesLargeIntegers(t *testing.T) {
	normalized, err := normalizePresetInputs(json.RawMessage(`{"agentId":123456789012345678}`))
	if err != nil {
		t.Fatalf("normalizePresetInputs failed: %v", err)
	}
	if string(normalized) != `{"agentId":123456789012345678}` {
		t.Fatalf("expected large integer precision to be preserved, got %s", string(normalized))
	}
}

func TestPresetCreateResolverMayNotMutateOwner(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	var received map[string]any
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode resolver request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "resolved",
			"mutations": map[string]any{
				"ownerId": "user-999",
				"spec": map[string]any{
					"serviceAccountName": "zeno-agent-ag-123",
				},
			},
		})
	}))
	defer resolver.Close()
	configurePresetResolverTestServer(s, resolver.URL)

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"name":"zeno-lake",
		"presetId":"zeno",
		"presetInputs":{"agentId":"ag-123"},
		"spec":{}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	stored := &spritzv1.Spritz{}
	if err := s.client.Get(context.Background(), client.ObjectKey{Name: "zeno-lake", Namespace: s.namespace}, stored); err != nil {
		t.Fatalf("expected created spritz resource: %v", err)
	}
	if stored.Spec.Owner.ID != "user-1" {
		t.Fatalf("expected resolver owner mutation to be ignored, got %q", stored.Spec.Owner.ID)
	}
}

func TestProvisionerRestoreStoredPayloadRestoresResolverMetadata(t *testing.T) {
	tx := &provisionerCreateTransaction{body: &createRequest{}}
	raw, err := createResolvedProvisionerPayload(createRequest{
		PresetID:    "zeno",
		NamePrefix:  "zeno",
		RequestID:   "req-1",
		Labels:      map[string]string{"spritz.sh/preset": "zeno"},
		Annotations: map[string]string{"spritz.sh/resolved-agent-id": "ag-123"},
		Spec: spritzv1.SpritzSpec{
			Image:              "example.com/zeno:latest",
			ServiceAccountName: "zeno-agent-ag-123",
			Owner:              spritzv1.SpritzOwner{ID: "user-123"},
		},
	}, "zeno", nil)
	if err != nil {
		t.Fatalf("createResolvedProvisionerPayload failed: %v", err)
	}
	if err := tx.restoreStoredPayload(raw); err != nil {
		t.Fatalf("restoreStoredPayload failed: %v", err)
	}
	if tx.body.Annotations["spritz.sh/resolved-agent-id"] != "ag-123" {
		t.Fatalf("expected restored annotation, got %#v", tx.body.Annotations)
	}
	if tx.body.Labels["spritz.sh/preset"] != "zeno" {
		t.Fatalf("expected restored label, got %#v", tx.body.Labels)
	}
}
