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

func configurePresetResolverTestServer(s *server, resolverURL, profileResolverURL string) {
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
	if strings.TrimSpace(resolverURL) != "" || strings.TrimSpace(profileResolverURL) != "" {
		resolvers := make([]configuredResolver, 0, 2)
		if strings.TrimSpace(resolverURL) != "" {
			resolvers = append(resolvers, configuredResolver{
				id:            "runtime-binding",
				extensionType: extensionTypeResolver,
				operation:     extensionOperationPresetCreateResolve,
				match: extensionMatchRule{
					presetIDs: map[string]struct{}{"zeno": {}},
				},
				transport: configuredHTTPTransport{
					url:     resolverURL,
					timeout: time.Second,
				},
			})
		}
		if strings.TrimSpace(profileResolverURL) != "" {
			resolvers = append(resolvers, configuredResolver{
				id:            "agent-profile",
				extensionType: extensionTypeResolver,
				operation:     extensionOperationAgentProfileSync,
				match: extensionMatchRule{
					presetIDs: map[string]struct{}{"zeno": {}},
				},
				transport: configuredHTTPTransport{
					url:     profileResolverURL,
					timeout: time.Second,
				},
			})
		}
		s.extensions = extensionRegistry{resolvers: resolvers}
	}
}

func TestCreateSpritzAppliesPresetCreateResolverForHumanCaller(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	var presetReceived map[string]any
	var profileReceived map[string]any
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var received map[string]any
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode resolver request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch received["operation"] {
		case string(extensionOperationPresetCreateResolve):
			presetReceived = received
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "resolved",
				"mutations": map[string]any{
					"spec": map[string]any{
						"serviceAccountName": "zeno-agent-ag-123",
						"agentRef": map[string]string{
							"type":     "external",
							"provider": "example-agent-catalog",
							"id":       "ag-123",
						},
					},
					"annotations": map[string]string{
						"spritz.sh/resolved-agent-id": "ag-123",
					},
				},
			})
		case string(extensionOperationAgentProfileSync):
			profileReceived = received
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "resolved",
				"output": map[string]any{
					"profile": map[string]string{
						"name":     "Helpful Lake Agent",
						"imageUrl": "https://example.com/agent.png",
					},
				},
			})
		default:
			t.Fatalf("unexpected resolver operation %#v", received["operation"])
		}
	}))
	defer resolver.Close()
	configurePresetResolverTestServer(s, resolver.URL, resolver.URL)

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
	if presetReceived["operation"] != string(extensionOperationPresetCreateResolve) {
		t.Fatalf("expected preset create operation, got %#v", presetReceived["operation"])
	}
	contextPayload, ok := presetReceived["context"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolver context payload, got %#v", presetReceived["context"])
	}
	if contextPayload["presetId"] != "zeno" {
		t.Fatalf("expected resolver presetId zeno, got %#v", contextPayload["presetId"])
	}
	inputPayload, ok := presetReceived["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolver input payload, got %#v", presetReceived["input"])
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
	if stored.Spec.AgentRef == nil {
		t.Fatalf("expected resolved agentRef to be stored")
	}
	if stored.Spec.AgentRef.Type != "external" || stored.Spec.AgentRef.Provider != "example-agent-catalog" || stored.Spec.AgentRef.ID != "ag-123" {
		t.Fatalf("expected resolved agentRef, got %#v", stored.Spec.AgentRef)
	}
	if stored.Status.Profile == nil {
		t.Fatalf("expected synced profile to be stored in status")
	}
	if stored.Status.Profile.Name != "Helpful Lake Agent" {
		t.Fatalf("expected synced profile name, got %#v", stored.Status.Profile.Name)
	}
	if stored.Status.Profile.ImageURL != "https://example.com/agent.png" {
		t.Fatalf("expected synced profile image URL, got %#v", stored.Status.Profile.ImageURL)
	}
	if stored.Status.Profile.Source != "synced" {
		t.Fatalf("expected synced profile source, got %#v", stored.Status.Profile.Source)
	}
	if stored.Status.Profile.Syncer != "agent-profile" {
		t.Fatalf("expected synced profile syncer id, got %#v", stored.Status.Profile.Syncer)
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
	profileInput, ok := profileReceived["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected profile sync input payload, got %#v", profileReceived["input"])
	}
	agentRef, ok := profileInput["agentRef"].(map[string]any)
	if !ok {
		t.Fatalf("expected profile sync agentRef payload, got %#v", profileInput["agentRef"])
	}
	if agentRef["type"] != "external" || agentRef["provider"] != "example-agent-catalog" || agentRef["id"] != "ag-123" {
		t.Fatalf("expected synced agentRef payload, got %#v", agentRef)
	}
}

func TestCreateSpritzRejectsPresetInputsWithoutMatchingResolver(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configurePresetResolverTestServer(s, "", "")
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
			id:            "runtime-binding",
			extensionType: extensionTypeResolver,
			operation:     extensionOperationPresetCreateResolve,
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
			id:            "runtime-binding",
			extensionType: extensionTypeResolver,
			operation:     extensionOperationPresetCreateResolve,
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

func TestNormalizePresetInputsCanonicalizesNestedObjects(t *testing.T) {
	first, err := normalizePresetInputs(json.RawMessage(`{"cfg":{"a":1,"b":2}}`))
	if err != nil {
		t.Fatalf("normalizePresetInputs failed: %v", err)
	}
	second, err := normalizePresetInputs(json.RawMessage(`{"cfg":{"b":2,"a":1}}`))
	if err != nil {
		t.Fatalf("normalizePresetInputs failed: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("expected nested objects to canonicalize equally, got %s vs %s", string(first), string(second))
	}
}

func TestPresetCreateResolverIgnoresOwnerMutation(t *testing.T) {
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
	configurePresetResolverTestServer(s, resolver.URL, "")

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

func TestCreateSpritzProvisionerRejectsManualServiceAccountForResolverRequiredField(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
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

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)

	body := []byte(`{
		"presetId":"zeno",
		"ownerId":"user-123",
		"idempotencyKey":"manual-service-account",
		"spec":{"serviceAccountName":"manual-sa"}
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
	if !strings.Contains(rec.Body.String(), "resolver-produced field") {
		t.Fatalf("expected resolver-produced field error, got %s", rec.Body.String())
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
