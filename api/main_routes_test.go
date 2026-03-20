package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestRegisterRoutesExposesHealthzUnderRootAndAPI(t *testing.T) {
	s := &server{
		auth:         authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
	}
	e := echo.New()
	s.registerRoutes(e)

	apiReq := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	apiRec := httptest.NewRecorder()
	e.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("expected /api/healthz to return 200, got %d", apiRec.Code)
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rootRec := httptest.NewRecorder()
	e.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusNotFound {
		t.Fatalf("expected /healthz to return 404, got %d", rootRec.Code)
	}
}

func TestRegisterRoutesAppliesAuthToRootAndAPIPrefix(t *testing.T) {
	s := &server{
		auth: authConfig{
			mode:     authModeHeader,
			headerID: "X-Spritz-User-Id",
		},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
	}
	e := echo.New()
	s.registerRoutes(e)

	apiReq := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	apiRec := httptest.NewRecorder()
	e.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /api/spritzes to return 401 without auth, got %d", apiRec.Code)
	}
	if !strings.Contains(apiRec.Body.String(), "unauthenticated") {
		t.Fatalf("expected /api/spritzes response to mention unauthenticated, got %q", apiRec.Body.String())
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/spritzes", nil)
	rootRec := httptest.NewRecorder()
	e.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusNotFound {
		t.Fatalf("expected /spritzes to return 404, got %d", rootRec.Code)
	}
}

func TestRegisterRoutesAppliesAuthToInstanceProxyPrefix(t *testing.T) {
	s := &server{
		auth: authConfig{
			mode:     authModeHeader,
			headerID: "X-Spritz-User-Id",
		},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
		routeModel:   spritzRouteModelFromEnv(),
		instanceProxy: instanceProxyConfig{
			enabled:     true,
			stripPrefix: true,
		},
	}
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/i/openclaw-tide-wind", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /i/openclaw-tide-wind to return 401 without auth, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthenticated") {
		t.Fatalf("expected /i/openclaw-tide-wind response to mention unauthenticated, got %q", rec.Body.String())
	}
}

func TestRegisterRoutesUsesConfiguredAPIPrefix(t *testing.T) {
	t.Setenv("SPRITZ_ROUTE_API_PATH_PREFIX", "/control-api")

	s := &server{
		auth:         authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
		routeModel:   spritzRouteModelFromEnv(),
	}
	e := echo.New()
	s.registerRoutes(e)

	customReq := httptest.NewRequest(http.MethodGet, "/control-api/healthz", nil)
	customRec := httptest.NewRecorder()
	e.ServeHTTP(customRec, customReq)
	if customRec.Code != http.StatusOK {
		t.Fatalf("expected configured api prefix to return 200, got %d", customRec.Code)
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	legacyRec := httptest.NewRecorder()
	e.ServeHTTP(legacyRec, legacyReq)
	if legacyRec.Code != http.StatusNotFound {
		t.Fatalf("expected legacy /api/healthz to return 404 when a custom prefix is configured, got %d", legacyRec.Code)
	}
}
