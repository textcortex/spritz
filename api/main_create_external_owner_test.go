package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type fakeExternalOwnerResolver struct {
	resolve func(context.Context, externalOwnerPolicy, principal, ownerRef, string) (externalOwnerResolution, error)
}

func (f fakeExternalOwnerResolver) ResolveExternalOwner(ctx context.Context, policy externalOwnerPolicy, principal principal, ref ownerRef, requestID string) (externalOwnerResolution, error) {
	return f.resolve(ctx, policy, principal, ref, requestID)
}

func configureExternalOwnerTestServer(s *server, resolver externalOwnerResolver) {
	s.externalOwners = externalOwnerConfig{
		subjectHashKey: []byte("test-external-owner-secret"),
		policies: map[string]externalOwnerPolicy{
			"zenobot": {
				PrincipalID: "zenobot",
				Issuer:      "zenobot",
				URL:         "http://resolver.example.com/v1/external-owners/resolve",
				AllowedProviders: map[string]struct{}{
					"msteams": {},
				},
				AllowedTenants: map[string]struct{}{
					"72f988bf-86f1-41af-91ab-2d7cd011db47": {},
				},
				Timeout: defaultExternalOwnerResolverTimeout,
			},
		},
		resolver: resolver,
	}
}

func newCreateSpritzAPI(t *testing.T, s *server) *echo.Echo {
	t.Helper()
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes", s.createSpritz)
	return e
}

func newServiceCreateRequest(body []byte) (*http.Request, *httptest.ResponseRecorder) {
	return newServiceCreateRequestWithScopes(body,
		scopeInstancesCreate,
		scopeInstancesAssignOwner,
		scopeExternalResolveViaCreate,
	)
}

func newServiceCreateRequestWithScopes(body []byte, scopes ...string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "zenobot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", strings.Join(scopes, ","))
	return req, httptest.NewRecorder()
}

func TestCreateSpritzResolvesExternalOwnerForProvisioner(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, policy externalOwnerPolicy, principal principal, ref ownerRef, requestID string) (externalOwnerResolution, error) {
			if policy.issuer() != "zenobot" {
				t.Fatalf("expected issuer zenobot, got %q", policy.issuer())
			}
			if principal.ID != "zenobot" {
				t.Fatalf("expected principal zenobot, got %q", principal.ID)
			}
			if requestID != "interaction-1" {
				t.Fatalf("expected requestID interaction-1, got %q", requestID)
			}
			if ref.Provider != "msteams" {
				t.Fatalf("expected provider msteams, got %q", ref.Provider)
			}
			return externalOwnerResolution{
				Status:  externalOwnerResolved,
				OwnerID: "user-123",
			}, nil
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-1","requestId":"interaction-1"}`)
	req, rec := newServiceCreateRequest(body)
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f") {
		t.Fatalf("expected raw external subject to be absent from response, got %s", rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	data := payload["data"].(map[string]any)
	if _, exists := data["ownerId"]; exists {
		t.Fatalf("expected ownerId to be omitted for external owner create, got %#v", data["ownerId"])
	}
	spritzData := data["spritz"].(map[string]any)
	spec := spritzData["spec"].(map[string]any)
	owner := spec["owner"].(map[string]any)
	if owner["id"] != "" {
		t.Fatalf("expected nested owner.id to be redacted, got %#v", owner["id"])
	}
	labels := spritzData["metadata"].(map[string]any)["labels"].(map[string]any)
	if _, exists := labels[ownerLabelKey]; exists {
		t.Fatalf("expected owner label to be omitted for external owner create response")
	}
	annotations := spritzData["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations[externalOwnerIssuerAnnotationKey] != "zenobot" {
		t.Fatalf("expected external issuer annotation, got %#v", annotations[externalOwnerIssuerAnnotationKey])
	}
	if annotations[externalOwnerProviderAnnotationKey] != "msteams" {
		t.Fatalf("expected external provider annotation, got %#v", annotations[externalOwnerProviderAnnotationKey])
	}
	if annotations[externalOwnerTenantAnnotationKey] != "72f988bf-86f1-41af-91ab-2d7cd011db47" {
		t.Fatalf("expected external tenant annotation, got %#v", annotations[externalOwnerTenantAnnotationKey])
	}
	if strings.TrimSpace(annotations[externalOwnerSubjectHashAnnotationKey].(string)) == "" {
		t.Fatalf("expected external subject hash annotation, got %#v", annotations[externalOwnerSubjectHashAnnotationKey])
	}
}

func TestCreateSpritzReturnsTypedFailureWhenExternalOwnerIsUnresolved(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
			return externalOwnerResolution{Status: externalOwnerUnresolved}, nil
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-unresolved"}`)
	req, rec := newServiceCreateRequest(body)
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response json: %v", err)
	}
	if payload["status"] != "fail" {
		t.Fatalf("expected jsend fail status, got %#v", payload["status"])
	}
	data := payload["data"].(map[string]any)
	if data["error"] != "external_identity_unresolved" {
		t.Fatalf("expected unresolved error code, got %#v", data["error"])
	}
	identity := data["identity"].(map[string]any)
	if identity["provider"] != "msteams" {
		t.Fatalf("expected provider msteams, got %#v", identity["provider"])
	}

	list := &spritzv1.SpritzList{}
	if err := s.client.List(context.Background(), list, client.InNamespace("spritz-test")); err != nil {
		t.Fatalf("failed to list spritz resources: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected no spritz resources after unresolved owner, got %d", len(list.Items))
	}
}

func TestCreateSpritzReplaysExternalOwnerProvisioningAfterResolverMappingChanges(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	resolveOwnerID := "user-123"
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
			return externalOwnerResolution{
				Status:  externalOwnerResolved,
				OwnerID: resolveOwnerID,
			}, nil
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-replay"}`)

	req1, rec1 := newServiceCreateRequest(body)
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	resolveOwnerID = "user-999"

	req2, rec2 := newServiceCreateRequest(body)
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var firstPayload map[string]any
	if err := json.Unmarshal(rec1.Body.Bytes(), &firstPayload); err != nil {
		t.Fatalf("failed to decode first response: %v", err)
	}
	var replayPayload map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &replayPayload); err != nil {
		t.Fatalf("failed to decode replay response: %v", err)
	}
	firstName := firstPayload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["name"]
	replayedName := replayPayload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["name"]
	if firstName != replayedName {
		t.Fatalf("expected replayed workspace name to match, got first=%#v replay=%#v", firstName, replayedName)
	}
	if _, exists := replayPayload["data"].(map[string]any)["ownerId"]; exists {
		t.Fatalf("expected replayed external-owner response to omit ownerId")
	}
	replayedLabels := replayPayload["data"].(map[string]any)["spritz"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
	if _, exists := replayedLabels[ownerLabelKey]; exists {
		t.Fatalf("expected replayed external-owner response to omit owner label")
	}

	stored := &spritzv1.Spritz{}
	if err := s.client.Get(context.Background(), client.ObjectKey{Namespace: "spritz-test", Name: firstName.(string)}, stored); err != nil {
		t.Fatalf("failed to load stored spritz: %v", err)
	}
	if stored.Spec.Owner.ID != "user-123" {
		t.Fatalf("expected stored owner to remain the original resolved owner, got %q", stored.Spec.Owner.ID)
	}
}

func TestCreateSpritzReplaysExternalOwnerProvisioningWhenResolverBecomesUnavailable(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	resolverCalls := 0
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
			resolverCalls++
			if resolverCalls == 1 {
				return externalOwnerResolution{
					Status:  externalOwnerResolved,
					OwnerID: "user-123",
				}, nil
			}
			return externalOwnerResolution{}, context.DeadlineExceeded
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-replay-unavailable"}`)

	req1, rec1 := newServiceCreateRequest(body)
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2, rec2 := newServiceCreateRequest(body)
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected replay status 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if resolverCalls != 1 {
		t.Fatalf("expected replay to avoid a second resolver call, got %d calls", resolverCalls)
	}
}

func TestCreateSpritzRejectsExternalOwnerResolutionWithoutCreateScopes(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	resolverCalls := 0
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
			resolverCalls++
			return externalOwnerResolution{Status: externalOwnerUnresolved}, nil
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-no-create-scope"}`)

	req, rec := newServiceCreateRequestWithScopes(body, scopeExternalResolveViaCreate)
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if resolverCalls != 0 {
		t.Fatalf("expected resolver to be skipped when create scopes are missing, got %d calls", resolverCalls)
	}
}

func TestCreateSpritzRejectsExternalOwnerForAdminCallers(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	resolverCalls := 0
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
			resolverCalls++
			return externalOwnerResolution{
				Status:  externalOwnerResolved,
				OwnerID: "user-123",
			}, nil
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/spritzes", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "admin-1")
	req.Header.Set("X-Spritz-Principal-Type", "admin")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if resolverCalls != 0 {
		t.Fatalf("expected resolver to be skipped for admin callers, got %d calls", resolverCalls)
	}
}

func TestCreateSpritzReplayRequiresExternalResolveScope(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	configureProvisionerTestServer(s)
	configureExternalOwnerTestServer(s, fakeExternalOwnerResolver{
		resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
			return externalOwnerResolution{
				Status:  externalOwnerResolved,
				OwnerID: "user-123",
			}, nil
		},
	})
	e := newCreateSpritzAPI(t, s)

	body := []byte(`{"presetId":"openclaw","ownerRef":{"type":"external","provider":"msteams","tenant":"72f988bf-86f1-41af-91ab-2d7cd011db47","subject":"6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f"},"idempotencyKey":"teams-scope-bypass"}`)

	req1, rec1 := newServiceCreateRequest(body)
	e.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected first create status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	req2, rec2 := newServiceCreateRequestWithScopes(body, scopeInstancesCreate, scopeInstancesAssignOwner)
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestExternalOwnerResolveRequiresTenantWhenTenantAllowlistIsConfigured(t *testing.T) {
	config := externalOwnerConfig{
		subjectHashKey: []byte("test-external-owner-secret"),
		policies: map[string]externalOwnerPolicy{
			"zenobot": {
				PrincipalID: "zenobot",
				Issuer:      "zenobot",
				URL:         "http://resolver.example.com/v1/external-owners/resolve",
				AllowedProviders: map[string]struct{}{
					"slack": {},
				},
				AllowedTenants: map[string]struct{}{
					"enterprise-1": {},
				},
				TenantRequired: map[string]struct{}{
					"slack": {},
				},
			},
		},
		resolver: fakeExternalOwnerResolver{
			resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
				t.Fatal("expected resolver call to be blocked when tenant is missing")
				return externalOwnerResolution{}, nil
			},
		},
	}

	_, err := config.resolve(context.Background(), principal{ID: "zenobot", Type: principalTypeService}, ownerRef{
		Type:     "external",
		Provider: "slack",
		Subject:  "U123456",
	}, "")
	if err == nil {
		t.Fatal("expected resolve to fail when tenant allowlist is configured but tenant is missing")
	}
	var resolutionErr externalOwnerResolutionError
	if !errors.As(err, &resolutionErr) {
		t.Fatalf("expected externalOwnerResolutionError, got %T", err)
	}
	if resolutionErr.code != "external_identity_forbidden" {
		t.Fatalf("expected external_identity_forbidden, got %q", resolutionErr.code)
	}
}

func TestExternalOwnerResolveAllowsTenantlessProviderWhenTenantIsNotRequired(t *testing.T) {
	config := externalOwnerConfig{
		subjectHashKey: []byte("test-external-owner-secret"),
		policies: map[string]externalOwnerPolicy{
			"zenobot": {
				PrincipalID: "zenobot",
				Issuer:      "zenobot",
				URL:         "http://resolver.example.com/v1/external-owners/resolve",
				AllowedProviders: map[string]struct{}{
					"slack":   {},
					"msteams": {},
				},
				AllowedTenants: map[string]struct{}{
					"72f988bf-86f1-41af-91ab-2d7cd011db47": {},
				},
			},
		},
		resolver: fakeExternalOwnerResolver{
			resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, ref ownerRef, _ string) (externalOwnerResolution, error) {
				if ref.Provider != "slack" {
					t.Fatalf("expected provider slack, got %q", ref.Provider)
				}
				return externalOwnerResolution{
					Status:  externalOwnerResolved,
					OwnerID: "user-123",
				}, nil
			},
		},
	}

	resolution, err := config.resolve(context.Background(), principal{ID: "zenobot", Type: principalTypeService}, ownerRef{
		Type:     "external",
		Provider: "slack",
		Subject:  "U123456",
	}, "")
	if err != nil {
		t.Fatalf("expected tenantless slack identity to resolve when tenant is not required, got %v", err)
	}
	if resolution.OwnerID != "user-123" {
		t.Fatalf("expected ownerId user-123, got %q", resolution.OwnerID)
	}
}

func TestExternalOwnerResolveRequiresTenantWithoutAllowlistWhenConfigured(t *testing.T) {
	config := externalOwnerConfig{
		subjectHashKey: []byte("test-external-owner-secret"),
		policies: map[string]externalOwnerPolicy{
			"zenobot": {
				PrincipalID: "zenobot",
				Issuer:      "zenobot",
				URL:         "http://resolver.example.com/v1/external-owners/resolve",
				AllowedProviders: map[string]struct{}{
					"slack": {},
				},
				TenantRequired: map[string]struct{}{
					"slack": {},
				},
			},
		},
		resolver: fakeExternalOwnerResolver{
			resolve: func(_ context.Context, _ externalOwnerPolicy, _ principal, _ ownerRef, _ string) (externalOwnerResolution, error) {
				t.Fatal("expected resolver call to be blocked when tenant is required but missing")
				return externalOwnerResolution{}, nil
			},
		},
	}

	_, err := config.resolve(context.Background(), principal{ID: "zenobot", Type: principalTypeService}, ownerRef{
		Type:     "external",
		Provider: "slack",
		Subject:  "U123456",
	}, "")
	if err == nil {
		t.Fatal("expected resolve to fail when tenant is required but missing")
	}
	var resolutionErr externalOwnerResolutionError
	if !errors.As(err, &resolutionErr) {
		t.Fatalf("expected externalOwnerResolutionError, got %T", err)
	}
	if resolutionErr.code != "external_identity_forbidden" {
		t.Fatalf("expected external_identity_forbidden, got %q", resolutionErr.code)
	}
}

func TestNewExternalOwnerConfigNormalizesTenantAllowlistUUIDs(t *testing.T) {
	t.Setenv("SPRITZ_EXTERNAL_OWNER_SUBJECT_HASH_KEY", "test-external-owner-secret")
	t.Setenv("SPRITZ_EXTERNAL_OWNER_POLICIES_JSON", `[{"principalId":"zenobot","url":"http://resolver.example.com/v1/external-owners/resolve","allowedProviders":["msteams"],"allowedTenants":["72F988BF-86F1-41AF-91AB-2D7CD011DB47"]}]`)

	config, err := newExternalOwnerConfig()
	if err != nil {
		t.Fatalf("newExternalOwnerConfig failed: %v", err)
	}

	policy, ok := config.policyForPrincipal(principal{ID: "zenobot", Type: principalTypeService})
	if !ok {
		t.Fatal("expected policy for principal zenobot")
	}
	if _, ok := policy.AllowedTenants["72f988bf-86f1-41af-91ab-2d7cd011db47"]; !ok {
		t.Fatalf("expected canonical lowercase tenant UUID in allowlist, got %#v", policy.AllowedTenants)
	}
}

func TestNewExternalOwnerConfigResolvesAuthHeaderFromEnv(t *testing.T) {
	t.Setenv("SPRITZ_EXTERNAL_OWNER_SUBJECT_HASH_KEY", "test-external-owner-secret")
	t.Setenv("SPRITZ_INTERNAL_TOKEN", "spritz-internal-token")
	t.Setenv("SPRITZ_EXTERNAL_OWNER_POLICIES_JSON", `[{"principalId":"zenobot","url":"http://resolver.example.com/v1/external-owners/resolve","authHeaderEnv":"SPRITZ_INTERNAL_TOKEN","allowedProviders":["discord"]}]`)

	config, err := newExternalOwnerConfig()
	if err != nil {
		t.Fatalf("newExternalOwnerConfig failed: %v", err)
	}

	policy, ok := config.policyForPrincipal(principal{ID: "zenobot", Type: principalTypeService})
	if !ok {
		t.Fatal("expected policy for principal zenobot")
	}
	if policy.AuthHeader != "Bearer spritz-internal-token" {
		t.Fatalf("expected bearer auth header from env token, got %q", policy.AuthHeader)
	}
}

func TestNewExternalOwnerConfigRejectsEmptyAuthHeaderEnv(t *testing.T) {
	t.Setenv("SPRITZ_EXTERNAL_OWNER_SUBJECT_HASH_KEY", "test-external-owner-secret")
	t.Setenv("SPRITZ_EXTERNAL_OWNER_POLICIES_JSON", `[{"principalId":"zenobot","url":"http://resolver.example.com/v1/external-owners/resolve","authHeaderEnv":"SPRITZ_INTERNAL_TOKEN","allowedProviders":["discord"]}]`)

	_, err := newExternalOwnerConfig()
	if err == nil {
		t.Fatal("expected newExternalOwnerConfig to reject empty authHeaderEnv")
	}
	if !strings.Contains(err.Error(), `authHeaderEnv "SPRITZ_INTERNAL_TOKEN" is empty`) {
		t.Fatalf("expected authHeaderEnv validation error, got %v", err)
	}
}

func TestCreateRequestFingerprintCanonicalizesEquivalentOwnerInputs(t *testing.T) {
	directFingerprint, err := createRequestFingerprint(createRequest{
		OwnerID: "user-123",
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed for direct owner: %v", err)
	}

	ownerRefFingerprint, err := createRequestFingerprint(createRequest{
		OwnerRef: &ownerRef{
			Type: "owner",
			ID:   "user-123",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed for ownerRef owner: %v", err)
	}

	if directFingerprint != ownerRefFingerprint {
		t.Fatalf("expected equivalent direct and ownerRef owner inputs to share a fingerprint")
	}

	lowerFingerprint, err := createRequestFingerprint(createRequest{
		OwnerRef: &ownerRef{
			Type:     "external",
			Provider: "msteams",
			Tenant:   "72f988bf-86f1-41af-91ab-2d7cd011db47",
			Subject:  "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed for lowercase msteams identity: %v", err)
	}

	upperFingerprint, err := createRequestFingerprint(createRequest{
		OwnerRef: &ownerRef{
			Type:     "external",
			Provider: "msteams",
			Tenant:   "72F988BF-86F1-41AF-91AB-2D7CD011DB47",
			Subject:  "6F0F9D4F-9B0E-4D52-8C3A-EF0FD64B9B9F",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed for uppercase msteams identity: %v", err)
	}

	if lowerFingerprint != upperFingerprint {
		t.Fatalf("expected equivalent msteams UUID casing to share a fingerprint")
	}
}

func TestCreateRequestFingerprintIncludesExternalIssuer(t *testing.T) {
	firstFingerprint, err := createRequestFingerprintWithIssuer(createRequest{
		OwnerRef: &ownerRef{
			Type:     "external",
			Provider: "msteams",
			Tenant:   "72f988bf-86f1-41af-91ab-2d7cd011db47",
			Subject:  "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}, "issuer-a", "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprintWithIssuer failed for issuer-a: %v", err)
	}

	secondFingerprint, err := createRequestFingerprintWithIssuer(createRequest{
		OwnerRef: &ownerRef{
			Type:     "external",
			Provider: "msteams",
			Tenant:   "72f988bf-86f1-41af-91ab-2d7cd011db47",
			Subject:  "6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f",
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}, "issuer-b", "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprintWithIssuer failed for issuer-b: %v", err)
	}

	if firstFingerprint == secondFingerprint {
		t.Fatalf("expected issuer to affect external owner fingerprint")
	}
}

func TestCreateRequestFingerprintPreservesLegacyDirectOwnerShape(t *testing.T) {
	body := createRequest{
		OwnerID:  "user-123",
		PresetID: "openclaw",
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz-openclaw:latest",
		},
	}

	got, err := createRequestFingerprint(body, "spritz-test", "", "", nil)
	if err != nil {
		t.Fatalf("createRequestFingerprint failed: %v", err)
	}

	specCopy := body.Spec
	specCopy.Annotations = nil
	specCopy.Labels = nil
	legacyPayload := struct {
		OwnerID    string              `json:"ownerId"`
		PresetID   string              `json:"presetId,omitempty"`
		Name       string              `json:"name,omitempty"`
		NamePrefix string              `json:"namePrefix,omitempty"`
		Namespace  string              `json:"namespace,omitempty"`
		Source     string              `json:"source,omitempty"`
		Spec       spritzv1.SpritzSpec `json:"spec"`
		UserConfig json.RawMessage     `json:"userConfig,omitempty"`
	}{
		OwnerID:   "user-123",
		PresetID:  "openclaw",
		Namespace: "spritz-test",
		Source:    provisionerSource(&body),
		Spec:      specCopy,
	}
	encoded, err := json.Marshal(legacyPayload)
	if err != nil {
		t.Fatalf("failed to marshal legacy fingerprint payload: %v", err)
	}
	sum := sha256.Sum256(encoded)
	want := fmt.Sprintf("%x", sum[:])
	if got != want {
		t.Fatalf("expected legacy direct-owner fingerprint %q, got %q", want, got)
	}
}

func TestNormalizeExternalOwnerRefCanonicalizesUUIDTenantForAllProviders(t *testing.T) {
	normalized, err := normalizeExternalOwnerRef(ownerRef{
		Type:     "external",
		Provider: "slack",
		Tenant:   "72F988BF-86F1-41AF-91AB-2D7CD011DB47",
		Subject:  "U123456",
	})
	if err != nil {
		t.Fatalf("normalizeExternalOwnerRef failed: %v", err)
	}
	if normalized.Tenant != "72f988bf-86f1-41af-91ab-2d7cd011db47" {
		t.Fatalf("expected canonical tenant UUID, got %q", normalized.Tenant)
	}
}

func TestNormalizeCreateOwnerSupportsOwnerRefOwner(t *testing.T) {
	body := &createRequest{
		OwnerRef: &ownerRef{Type: "owner", ID: "user-123"},
	}
	owner, err := normalizeCreateOwner(body, principal{ID: "zenobot", Type: principalTypeService}, true)
	if err != nil {
		t.Fatalf("normalizeCreateOwner failed: %v", err)
	}
	if owner.ID != "user-123" {
		t.Fatalf("expected owner id user-123, got %q", owner.ID)
	}
	if body.OwnerID != "user-123" {
		t.Fatalf("expected body ownerId to be populated from ownerRef, got %q", body.OwnerID)
	}
}

func TestNormalizeCreateOwnerRejectsOwnerRefWithoutType(t *testing.T) {
	body := &createRequest{
		OwnerRef: &ownerRef{ID: "user-123"},
	}
	_, err := normalizeCreateOwnerRequest(body, principal{ID: "user-123", Type: principalTypeHuman}, true)
	if err == nil {
		t.Fatal("expected normalizeCreateOwnerRequest to reject ownerRef without type")
	}
	if !strings.Contains(err.Error(), "ownerRef.type is required") {
		t.Fatalf("expected ownerRef.type validation error, got %v", err)
	}
}
