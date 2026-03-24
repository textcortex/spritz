package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

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

func TestInternalGetSpritzReturnsSanitizedSummary(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
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
