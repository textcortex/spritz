package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestInternalUpsertBindingCreatesAndFetchesBinding(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"desiredRevision": "sha256:rev-1",
		"disconnected": false,
		"attributes": {
			"provider": "slack",
			"externalTenantId": "T_workspace_1"
		},
		"principal": {"id": "channel-gateway"},
		"request": {
			"presetId": "zeno",
			"ownerId": "user-123",
			"requestId": "binding-upsert-1",
			"source": "channel-gateway",
			"spec": {}
		}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/internal/v1/bindings/channel-installation-binding-1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bindingKey":"channel-installation-binding-1"`) {
		t.Fatalf("expected binding key in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"desiredRevision":"sha256:rev-1"`) {
		t.Fatalf("expected desired revision in response, got %s", rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/internal/v1/bindings/channel-installation-binding-1", nil)
	getReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	getRec := httptest.NewRecorder()
	e.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"bindingKey":"channel-installation-binding-1"`) {
		t.Fatalf("expected fetched binding key in response, got %s", getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"presetId":"zeno"`) {
		t.Fatalf("expected fetched preset id in response, got %s", getRec.Body.String())
	}
}

func TestInternalReplaceSpritzUsesBindingLifecycleWhenRuntimeIsOwnedByBinding(t *testing.T) {
	targetRevision := "sha256:rev-2"
	binding := &spritzv1.SpritzBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "channel-installation-binding-1",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzBindingSpec{
			BindingKey:      "channel-installation-binding-1",
			DesiredRevision: "sha256:rev-1",
		},
		Status: spritzv1.SpritzBindingStatus{
			ObservedRevision: "sha256:rev-1",
			ActiveInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-acme",
				Revision:  "sha256:rev-1",
				Phase:     "Ready",
			},
			CandidateInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-replacement",
				Revision:  targetRevision,
				Phase:     "Provisioning",
			},
		},
	}
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: spritzv1.GroupVersion.String(),
				Kind:       "SpritzBinding",
				Name:       binding.Name,
			}},
		},
	}
	s := newInternalSpritzesTestServer(t, source)
	if err := s.client.Create(context.Background(), binding); err != nil {
		t.Fatalf("failed to create binding: %v", err)
	}
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:rev-2",
		"idempotencyKey": "replace-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"presetId": "zeno",
				"ownerId": "user-123",
				"requestId": "replace-1",
				"source": "channel-gateway",
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
	var payload struct {
		Data internalReplaceSpritzResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode replace response: %v", err)
	}
	if payload.Data.Replacement.InstanceID != "zeno-replacement" {
		t.Fatalf("expected candidate replacement in response, got %#v", payload.Data)
	}
	if payload.Data.Replacement.TargetRevision != targetRevision {
		t.Fatalf("expected target revision %q, got %#v", targetRevision, payload.Data)
	}
	if payload.Data.Replayed {
		t.Fatalf("expected first binding replace to be non-replayed")
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(context.Background(), client.ObjectKey{Namespace: binding.Namespace, Name: binding.Name}, &stored); err != nil {
		t.Fatalf("failed to reload binding: %v", err)
	}
	if stored.Spec.DesiredRevision != targetRevision {
		t.Fatalf("expected binding desired revision to be updated, got %#v", stored.Spec)
	}
	if strings.TrimSpace(stored.Annotations[spritzv1.BindingReconcileRequestedAtAnnotationKey]) == "" {
		t.Fatalf("expected reconcile annotation to be set, got %#v", stored.Annotations)
	}
}
