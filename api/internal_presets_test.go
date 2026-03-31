package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
)

func newInternalPresetsTestServer() *server {
	return &server{
		auth:         authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{enabled: true, token: "spritz-internal-token"},
		terminal:     terminalConfig{enabled: false},
		presets: presetCatalog{byID: []runtimePreset{
			{
				ID:            "zeno-concierge",
				Name:          "Zeno Concierge",
				Description:   "Shared concierge runtime",
				Image:         "ghcr.io/example/zeno@sha256:live-image",
				NamePrefix:    "zeno",
				InstanceClass: "personal-agent",
				Hidden:        true,
				Env: []corev1.EnvVar{
					{Name: "OPENCLAW_CONFIG_JSON", Value: "{}"},
				},
			},
		}},
	}
}

func TestGetInternalPresetReturnsRenderedPresetMetadata(t *testing.T) {
	s := newInternalPresetsTestServer()
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/presets/zeno-concierge", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected internal preset lookup to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	expectedFragments := []string{
		`"id":"zeno-concierge"`,
		`"image":"ghcr.io/example/zeno@sha256:live-image"`,
		`"instanceClass":"personal-agent"`,
		`"env":[{"name":"OPENCLAW_CONFIG_JSON","value":"{}"}]`,
	}
	for _, fragment := range expectedFragments {
		if !strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("expected response body to contain %q, got %s", fragment, rec.Body.String())
		}
	}
}

func TestGetInternalPresetReturnsNotFoundForUnknownPreset(t *testing.T) {
	s := newInternalPresetsTestServer()
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/v1/presets/missing", nil)
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected missing preset lookup to return 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
