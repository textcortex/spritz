package main

import (
	"net/http"
	"testing"
)

func TestClassifyInstallUpsertErrorPreservesTypedBackendAvailabilityCodes(t *testing.T) {
	err := &httpStatusError{
		method:     http.MethodPost,
		endpoint:   "/internal/installations/upsert",
		statusCode: http.StatusServiceUnavailable,
		body:       `{"error":"runtime_binding_unavailable"}`,
	}

	if got := classifyInstallUpsertError(err); got != installResultCodeRuntimeUnavailable {
		t.Fatalf("expected runtime binding unavailable, got %q", got)
	}
}

func TestClassifyInstallUpsertErrorMapsLegacyOwnerRefUnresolvedPayloads(t *testing.T) {
	err := &httpStatusError{
		method:     http.MethodPost,
		endpoint:   "/internal/installations/upsert",
		statusCode: http.StatusConflict,
		body:       `{"status":"unresolved","field":"ownerRef"}`,
	}

	if got := classifyInstallUpsertError(err); got != installResultCodeIdentityUnresolved {
		t.Fatalf("expected external identity unresolved, got %q", got)
	}
}
