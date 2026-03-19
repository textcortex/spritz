package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newRuntimeBindingsTestServer(t *testing.T, objects ...*spritzv1.Spritz) *server {
	t.Helper()
	scheme := newTestSpritzScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme)
	clientObjects := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		clientObjects = append(clientObjects, object)
	}
	if len(clientObjects) > 0 {
		builder = builder.WithRuntimeObjects(clientObjects...)
	}
	return &server{
		client:       builder.Build(),
		scheme:       scheme,
		auth:         authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{enabled: true, token: "spritz-internal-token"},
		terminal:     terminalConfig{enabled: false},
	}
}

func TestGetRuntimeBindingReturnsCanonicalFacts(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-delta-breeze",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				presetIDAnnotationKey:      "zeno",
				instanceClassAnnotationKey: "personal-agent",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Owner:              spritzv1.SpritzOwner{ID: "user-123"},
			ServiceAccountName: "zeno-agent-abcd1234",
		},
	}
	s := newRuntimeBindingsTestServer(t, spritz)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/runtime-bindings/spritz-production/zeno-delta-breeze", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected runtime binding lookup to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got == "" {
		t.Fatalf("expected response body, got empty")
	}
	expectedFragments := []string{
		`"instanceId":"zeno-delta-breeze"`,
		`"namespace":"spritz-production"`,
		`"id":"user-123"`,
		`"authnMode":"workload_identity"`,
		`"serviceAccountName":"zeno-agent-abcd1234"`,
		`"presetId":"zeno"`,
		`"instanceClassId":"personal-agent"`,
	}
	for _, fragment := range expectedFragments {
		if !strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("expected response body to contain %q, got %s", fragment, rec.Body.String())
		}
	}
}

func TestGetRuntimeBindingUsesDefaultServiceAccountWhenUnset(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openclaw-morning-sky",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				presetIDAnnotationKey:      "openclaw",
				instanceClassAnnotationKey: "assistant-runtime",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
	}
	s := newRuntimeBindingsTestServer(t, spritz)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/runtime-bindings/spritz-production/openclaw-morning-sky", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected runtime binding lookup to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"serviceAccountName":"default"`) {
		t.Fatalf("expected response body to use default service account, got %s", rec.Body.String())
	}
}

func TestGetRuntimeBindingRejectsIncompleteBinding(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-delta-breeze",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				presetIDAnnotationKey: "zeno",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
	}
	s := newRuntimeBindingsTestServer(t, spritz)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/runtime-bindings/spritz-production/zeno-delta-breeze", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected incomplete runtime binding to return 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRuntimeBindingRejectsNamespaceOutsideServerScope(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-delta-breeze",
			Namespace: "spritz-production",
			Annotations: map[string]string{
				presetIDAnnotationKey:      "zeno",
				instanceClassAnnotationKey: "personal-agent",
			},
		},
		Spec: spritzv1.SpritzSpec{
			Owner:              spritzv1.SpritzOwner{ID: "user-123"},
			ServiceAccountName: "zeno-agent-abcd1234",
		},
	}
	s := newRuntimeBindingsTestServer(t, spritz)
	s.namespace = "spritz-production"
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/runtime-bindings/spritz-staging/zeno-delta-breeze", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected namespace mismatch to return 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRuntimeBindingRouteIsNotRegisteredWithoutInternalAuth(t *testing.T) {
	s := &server{
		auth:         authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
	}
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/internal/v1/runtime-bindings/spritz-production/zeno-delta-breeze",
		nil,
	)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf(
			"expected runtime binding route to be unavailable without internal auth, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}
}
