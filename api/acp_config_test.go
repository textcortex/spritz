package main

import (
	"net/http/httptest"
	"testing"
)

func TestACPAllowOriginAllowsEmptyOriginForBearerClients(t *testing.T) {
	cfg := acpConfig{
		allowedOrigins: map[string]struct{}{
			"https://console.example.test": {},
		},
	}
	req := httptest.NewRequest("GET", "https://api.example.test/api/acp/conversations/conv-1/connect", nil)
	req.Header.Set("Authorization", "Bearer service-token")

	if !cfg.allowOrigin(req) {
		t.Fatal("expected empty origin to be allowed for bearer-authenticated service clients")
	}
}

func TestACPAllowOriginRejectsEmptyOriginWithoutBearer(t *testing.T) {
	cfg := acpConfig{
		allowedOrigins: map[string]struct{}{
			"https://console.example.test": {},
		},
	}
	req := httptest.NewRequest("GET", "https://api.example.test/api/acp/conversations/conv-1/connect", nil)

	if cfg.allowOrigin(req) {
		t.Fatal("expected empty origin without bearer auth to be rejected")
	}
}
