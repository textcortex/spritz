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

	rootReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rootRec := httptest.NewRecorder()
	e.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("expected /healthz to return 200, got %d", rootRec.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	apiRec := httptest.NewRecorder()
	e.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("expected /api/healthz to return 200, got %d", apiRec.Code)
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

	rootReq := httptest.NewRequest(http.MethodGet, "/spritzes", nil)
	rootRec := httptest.NewRecorder()
	e.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /spritzes to return 401 without auth, got %d", rootRec.Code)
	}
	if !strings.Contains(rootRec.Body.String(), "unauthenticated") {
		t.Fatalf("expected /spritzes response to mention unauthenticated, got %q", rootRec.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	apiRec := httptest.NewRecorder()
	e.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /api/spritzes to return 401 without auth, got %d", apiRec.Code)
	}
	if !strings.Contains(apiRec.Body.String(), "unauthenticated") {
		t.Fatalf("expected /api/spritzes response to mention unauthenticated, got %q", apiRec.Body.String())
	}
}
