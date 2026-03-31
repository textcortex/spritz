package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func replaceReservationActorIDForTest(namespace, sourceName string) string {
	return "replace:" + strings.TrimSpace(namespace) + ":" + strings.TrimSpace(sourceName)
}

func storeReplaceReservationForTest(
	t *testing.T,
	s *server,
	namespace, sourceName, idempotencyKey, fingerprint, replacementName string,
	completed bool,
) {
	t.Helper()
	err := s.client.Create(
		context.Background(),
		reservationConfigMap(
			s.idempotencyReservationNamespace(),
			replaceReservationActorIDForTest(namespace, sourceName),
			idempotencyKey,
			idempotencyReservationRecord{
				fingerprint: fingerprint,
				name:        replacementName,
				completed:   completed,
			},
		),
	)
	if err != nil {
		t.Fatalf("failed to store replace reservation: %v", err)
	}
}

func mustNormalizeReplaceForTest(
	t *testing.T,
	s *server,
	namespace, sourceName string,
	body string,
) (principal, createRequest, string) {
	t.Helper()
	var requestBody internalReplaceSpritzRequest
	if err := json.Unmarshal([]byte(body), &requestBody); err != nil {
		t.Fatalf("failed to decode replace body: %v", err)
	}
	replacementPrincipal, replacementRequest, fingerprint, err := s.normalizeReplaceRequest(
		context.Background(),
		requestBody,
		namespace,
		sourceName,
	)
	if err != nil {
		t.Fatalf("failed to normalize replace body: %v", err)
	}
	return replacementPrincipal, replacementRequest, fingerprint
}

func mustReplaceReservationFingerprintForTest(
	t *testing.T,
	s *server,
	namespace, sourceName string,
	body string,
) string {
	t.Helper()
	replacementPrincipal, _, fingerprint := mustNormalizeReplaceForTest(
		t,
		s,
		namespace,
		sourceName,
		body,
	)
	var requestBody internalReplaceSpritzRequest
	if err := json.Unmarshal([]byte(body), &requestBody); err != nil {
		t.Fatalf("failed to decode replace body: %v", err)
	}
	return replacementRequestFingerprint(
		replacementPrincipal,
		requestBody.TargetRevision,
		fingerprint,
	)
}

func newInternalSpritzesTestServer(t *testing.T, objects ...*spritzv1.Spritz) *server {
	t.Helper()
	scheme := newTestSpritzScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme)
	runtimeObjects := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		runtimeObjects = append(runtimeObjects, object)
	}
	if len(runtimeObjects) > 0 {
		builder = builder.WithRuntimeObjects(runtimeObjects...)
	}
	return &server{
		client: builder.Build(),
		scheme: scheme,
		auth:   authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{
			enabled: true,
			token:   "spritz-internal-token",
		},
		terminal: terminalConfig{enabled: false},
		presets: presetCatalog{byID: []runtimePreset{
			{
				ID:            "zeno",
				Name:          "Zeno",
				Image:         "ghcr.io/example/zeno:latest",
				NamePrefix:    "zeno",
				InstanceClass: "personal-agent",
			},
		}},
		instanceClasses: instanceClassCatalog{
			byID: map[string]instanceClass{"personal-agent": {
				ID:      "personal-agent",
				Version: "v1",
				Creation: instanceClassCreationPolicy{
					RequireOwner: true,
				},
			}},
		},
		provisioners: provisionerPolicy{
			allowedPresetIDs: map[string]struct{}{"zeno": {}},
			defaultIdleTTL:   defaultProvisionerIdleTTL,
			maxIdleTTL:       defaultProvisionerIdleTTL,
			defaultTTL:       defaultProvisionerMaxTTL,
			maxTTL:           defaultProvisionerMaxTTL,
			rateWindow:       time.Hour,
		},
	}
}

func TestInternalCreateSpritzNormalizesCallerSuppliedPrincipal(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"principal": {
			"id": "forged-human",
			"type": "human",
			"issuer": "forged-human",
			"scopes": ["spritz.instances.create","spritz.instances.assign_owner","spritz.admin"]
		},
		"request": {
			"presetId": "zeno",
			"ownerId": "user-123",
			"idempotencyKey": "route-key-123",
			"requestId": "internal-create-1",
			"source": "channel-gateway",
			"spec": {}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, fragment := range []string{
		`"ownerId":"user-123"`,
		`"actorId":"forged-human"`,
		`"actorType":"service"`,
		`"presetId":"zeno"`,
		`"source":"channel-gateway"`,
	} {
		if !strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("expected response to contain %q, got %s", fragment, rec.Body.String())
		}
	}
}

func TestInternalCreateSpritzRejectsChangedAllowedReplacementAnnotationsOnReplay(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	e := echo.New()
	s.registerRoutes(e)

	firstBody := `{
		"principal": {"id": "channel-gateway"},
		"request": {
			"name": "zeno-replacement",
			"presetId": "zeno",
			"ownerId": "user-123",
			"idempotencyKey": "create-1",
			"annotations": {
				"spritz.sh/target-revision": "sha256:rev-1"
			},
			"spec": {}
		}
	}`
	secondBody := `{
		"principal": {"id": "channel-gateway"},
		"request": {
			"name": "zeno-replacement",
			"presetId": "zeno",
			"ownerId": "user-123",
			"idempotencyKey": "create-1",
			"annotations": {
				"spritz.sh/target-revision": "sha256:rev-2"
			},
			"spec": {}
		}
	}`
	doRequest := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer spritz-internal-token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	first := doRequest(firstBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", first.Code, first.Body.String())
	}

	second := doRequest(secondBody)
	if second.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "different request") {
		t.Fatalf("expected idempotency conflict, got %s", second.Body.String())
	}
}

func TestInternalGetSpritzReturnsSanitizedSummary(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				targetRevisionAnnotationKey: "sha256:revision-1",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
			Env: []corev1.EnvVar{{
				Name:  "DISCORD_BOT_TOKEN",
				Value: "secret-token",
			}},
			Repo: &spritzv1.SpritzRepo{
				URL: "https://example.com/private.git",
				Auth: &spritzv1.SpritzRepoAuth{
					SecretName: "repo-auth-secret",
				},
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, spritz)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/spritzes/spritz-production/zeno-acme", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"phase":"Ready"`) {
		t.Fatalf("expected response to include ready phase, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"targetRevision":"sha256:revision-1"`) {
		t.Fatalf("expected response to include target revision, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"owner":{"id":"user-123"}`) {
		t.Fatalf("expected response to include owner summary, got %s", rec.Body.String())
	}
	for _, fragment := range []string{
		`"env":`,
		`"repo":`,
		`"secretName":"repo-auth-secret"`,
		`"DISCORD_BOT_TOKEN"`,
	} {
		if strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("expected response to omit %q, got %s", fragment, rec.Body.String())
		}
	}
}

func TestInternalGetSpritzRedactsExternallyResolvedOwner(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				externalOwnerIssuerAnnotationKey:      "discord",
				externalOwnerProviderAnnotationKey:    "discord",
				externalOwnerSubjectHashAnnotationKey: "subject-hash",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, spritz)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/spritzes/spritz-production/zeno-acme", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"owner":{"id":"user-123"}`) {
		t.Fatalf("expected externally resolved owner id to be redacted, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"owner":{"id":""}`) {
		t.Fatalf("expected redacted owner summary, got %s", rec.Body.String())
	}
}

func TestInternalDeleteSpritzRemovesInstance(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
	}
	s := newInternalSpritzesTestServer(t, spritz)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodDelete, "/api/internal/v1/spritzes/spritz-production/zeno-acme", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var deleted spritzv1.Spritz
	if err := s.client.Get(context.Background(), client.ObjectKey{
		Namespace: "spritz-production",
		Name:      "zeno-acme",
	}, &deleted); err == nil {
		t.Fatalf("expected spritz to be deleted")
	}
}

func TestInternalReplaceSpritzCreatesReplacementWithLineageMetadata(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, fragment := range []string{
		`"instanceId":"zeno-replacement"`,
		`"targetRevision":"sha256:revision-2"`,
		`"ready":false`,
	} {
		if !strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("expected response to contain %q, got %s", fragment, rec.Body.String())
		}
	}

	var replacement spritzv1.Spritz
	if err := s.client.Get(context.Background(), client.ObjectKey{
		Namespace: "spritz-production",
		Name:      "zeno-replacement",
	}, &replacement); err != nil {
		t.Fatalf("expected replacement spritz to exist: %v", err)
	}
	annotations := replacement.GetAnnotations()
	if annotations[targetRevisionAnnotationKey] != "sha256:revision-2" {
		t.Fatalf("expected target revision annotation, got %#v", annotations)
	}
	if annotations[replacementSourceNSAnnotationKey] != "spritz-production" {
		t.Fatalf("expected source namespace annotation, got %#v", annotations)
	}
	if annotations[replacementSourceNameAnnotationKey] != "zeno-acme" {
		t.Fatalf("expected source name annotation, got %#v", annotations)
	}
	if annotations[replacementIDKeyAnnotationKey] != "rollout-1" {
		t.Fatalf("expected replacement idempotency annotation, got %#v", annotations)
	}
}

func TestInternalReplaceSpritzReplaysExistingReplacement(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	first := httptest.NewRecorder()

	e.ServeHTTP(first, req)

	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first call to create replacement, got %d: %s", first.Code, first.Body.String())
	}

	var replacement spritzv1.Spritz
	if err := s.client.Get(context.Background(), client.ObjectKey{
		Namespace: "spritz-production",
		Name:      "zeno-replacement",
	}, &replacement); err != nil {
		t.Fatalf("expected replacement spritz to exist: %v", err)
	}
	replacement.Status.Phase = "Ready"
	if err := s.client.Update(context.Background(), &replacement); err != nil {
		t.Fatalf("expected to update replacement readiness: %v", err)
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	replayReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	replayReq.Header.Set("Content-Type", "application/json")
	replay := httptest.NewRecorder()

	e.ServeHTTP(replay, replayReq)

	if replay.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", replay.Code, replay.Body.String())
	}
	if !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("expected replayed response, got %s", replay.Body.String())
	}
	if !strings.Contains(replay.Body.String(), `"ready":true`) {
		t.Fatalf("expected ready replacement response, got %s", replay.Body.String())
	}
}

func TestInternalReplaceSpritzReplaysAfterSourceDeletion(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	firstReq := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	firstReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	firstReq.Header.Set("Content-Type", "application/json")
	first := httptest.NewRecorder()
	e.ServeHTTP(first, firstReq)
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", first.Code, first.Body.String())
	}

	if err := s.client.Delete(context.Background(), source); err != nil {
		t.Fatalf("expected source delete to succeed: %v", err)
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	replayReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	replayReq.Header.Set("Content-Type", "application/json")
	replay := httptest.NewRecorder()
	e.ServeHTTP(replay, replayReq)

	if replay.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", replay.Code, replay.Body.String())
	}
	if !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("expected replayed response, got %s", replay.Body.String())
	}
}

func TestInternalReplaceSpritzRejectsConflictingTargetRevision(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	oldBody := `{
		"targetRevision": "sha256:old-revision",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	storeReplaceReservationForTest(
		t,
		s,
		"spritz-production",
		"zeno-acme",
		"rollout-1",
		mustReplaceReservationFingerprintForTest(
			t,
			s,
			"spritz-production",
			"zeno-acme",
			oldBody,
		),
		"zeno-replacement",
		true,
	)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:new-revision",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "different request") {
		t.Fatalf("expected conflict message, got %s", rec.Body.String())
	}
}

func TestInternalReplaceSpritzScopesIdempotencyBySourceInstance(t *testing.T) {
	sourceA := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-a",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	sourceB := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-b",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, sourceA, sourceB)
	e := echo.New()
	s.registerRoutes(e)

	doRequest := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer spritz-internal-token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	bodyA := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-a-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	bodyB := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-b-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`

	first := doRequest("/api/internal/v1/spritzes/spritz-production/zeno-a:replace", bodyA)
	if first.Code != http.StatusAccepted {
		t.Fatalf("expected first replace to succeed, got %d: %s", first.Code, first.Body.String())
	}
	second := doRequest("/api/internal/v1/spritzes/spritz-production/zeno-b:replace", bodyB)
	if second.Code != http.StatusAccepted {
		t.Fatalf("expected second replace to succeed, got %d: %s", second.Code, second.Body.String())
	}
}

func TestInternalReplaceSpritzRejectsChangedReplacementRequestOnReplay(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	originalBody := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	storeReplaceReservationForTest(
		t,
		s,
		"spritz-production",
		"zeno-acme",
		"rollout-1",
		mustReplaceReservationFingerprintForTest(
			t,
			s,
			"spritz-production",
			"zeno-acme",
			originalBody,
		),
		"zeno-replacement",
		true,
	)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-other-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "different request") {
		t.Fatalf("expected conflict message, got %s", rec.Body.String())
	}
}

func TestInternalReplaceSpritzIgnoresSpoofedOrdinarySpritz(t *testing.T) {
	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-real-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	probeServer := newInternalSpritzesTestServer(t)
	replacementPrincipal, replacementRequest, fingerprint := mustNormalizeReplaceForTest(
		t,
		probeServer,
		"spritz-production",
		"zeno-acme",
		body,
	)

	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	spoofed := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-spoofed",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				targetRevisionAnnotationKey:        "sha256:revision-2",
				replacementSourceNSAnnotationKey:   "spritz-production",
				replacementSourceNameAnnotationKey: "zeno-acme",
				replacementIDKeyAnnotationKey:      "rollout-1",
				idempotencyKeyAnnotationKey: replacementCreateIdempotencyKey(
					"spritz-production",
					"zeno-acme",
					"rollout-1",
				),
				idempotencyHashAnnotationKey: fingerprint,
				actorIDAnnotationKey:         replacementPrincipal.ID,
				actorTypeAnnotationKey:       string(replacementPrincipal.Type),
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source, spoofed)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/internal/v1/spritzes/spritz-production/zeno-acme:replace",
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"instanceId":"zeno-real-replacement"`) {
		t.Fatalf("expected a new replacement, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"instanceId":"zeno-spoofed"`) {
		t.Fatalf("expected spoofed spritz to be ignored, got %s", rec.Body.String())
	}
	if replacementRequest.Name != "zeno-real-replacement" {
		t.Fatalf("expected normalized replacement name, got %q", replacementRequest.Name)
	}
}

func TestInternalReplaceSpritzRejectsConflictingOuterReservation(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	storeReplaceReservationForTest(
		t,
		s,
		"spritz-production",
		"zeno-acme",
		"rollout-1",
		"different-request",
		"",
		false,
	)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/internal/v1/spritzes/spritz-production/zeno-acme:replace",
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "different request") {
		t.Fatalf("expected conflict message, got %s", rec.Body.String())
	}
}

func TestInternalReplaceSpritzRejectsReusedReservedNameForUnrelatedSpritz(t *testing.T) {
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	unrelated := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-replacement",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "ghcr.io/example/zeno:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	body := `{
		"targetRevision": "sha256:revision-2",
		"idempotencyKey": "rollout-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"name": "zeno-replacement",
				"presetId": "zeno",
				"ownerId": "user-123",
				"spec": {}
			}
		}
	}`
	s := newInternalSpritzesTestServer(t, source, unrelated)
	storeReplaceReservationForTest(
		t,
		s,
		"spritz-production",
		"zeno-acme",
		"rollout-1",
		mustReplaceReservationFingerprintForTest(
			t,
			s,
			"spritz-production",
			"zeno-acme",
			body,
		),
		"zeno-replacement",
		true,
	)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/internal/v1/spritzes/spritz-production/zeno-acme:replace",
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"replayed":true`) {
		t.Fatalf("expected reused name not to replay, got %s", rec.Body.String())
	}
}
