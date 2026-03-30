package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type interceptStatusClient struct {
	client.Client
	writer client.SubResourceWriter
}

func (c *interceptStatusClient) Status() client.SubResourceWriter {
	return c.writer
}

type conflictOnceStatusWriter struct {
	client.SubResourceWriter
	onConflict func(context.Context) error
	conflicted bool
}

func (w *conflictOnceStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if !w.conflicted {
		w.conflicted = true
		if w.onConflict != nil {
			if err := w.onConflict(ctx); err != nil {
				return err
			}
		}
		return apierrors.NewConflict(
			schema.GroupResource{Group: spritzv1.GroupVersion.Group, Resource: "spritzes"},
			obj.GetName(),
			errors.New("status updated"),
		)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestApplyResolvedAgentProfileStatusRetriesConflicts(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	ctx := context.Background()
	created := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tidy-otter",
			Namespace: s.namespace,
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
	}
	if err := s.client.Create(ctx, created); err != nil {
		t.Fatalf("expected spritz create to succeed: %v", err)
	}

	objectKey := client.ObjectKeyFromObject(created)
	baseClient := s.client
	s.client = &interceptStatusClient{
		Client: baseClient,
		writer: &conflictOnceStatusWriter{
			SubResourceWriter: baseClient.Status(),
			onConflict: func(ctx context.Context) error {
				latest := &spritzv1.Spritz{}
				if err := baseClient.Get(ctx, objectKey, latest); err != nil {
					return err
				}
				latest.Status.Phase = "Provisioning"
				return baseClient.Status().Update(ctx, latest)
			},
		},
	}

	now := metav1.Now()
	updated, err := s.applyResolvedAgentProfileStatus(ctx, created, &resolvedAgentProfile{
		profile: &spritzv1.SpritzAgentProfile{
			Name:     "Helpful Otter",
			ImageURL: "https://example.com/otter.png",
		},
		syncer:   "agent-profile",
		syncedAt: &now,
	})
	if err != nil {
		t.Fatalf("expected profile status update to retry conflicts: %v", err)
	}
	if updated.Status.Phase != "Provisioning" {
		t.Fatalf("expected retry to keep latest status fields, got phase %q", updated.Status.Phase)
	}
	if updated.Status.Profile == nil {
		t.Fatalf("expected profile status to be written after retry")
	}
	if updated.Status.Profile.Name != "Helpful Otter" {
		t.Fatalf("expected synced profile name, got %#v", updated.Status.Profile.Name)
	}
	if updated.Status.Profile.ImageURL != "https://example.com/otter.png" {
		t.Fatalf("expected synced profile image, got %#v", updated.Status.Profile.ImageURL)
	}
	if updated.Status.Profile.Syncer != "agent-profile" {
		t.Fatalf("expected syncer id, got %#v", updated.Status.Profile.Syncer)
	}

	stored := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, objectKey, stored); err != nil {
		t.Fatalf("expected stored spritz to be readable: %v", err)
	}
	if stored.Status.Phase != "Provisioning" {
		t.Fatalf("expected stored phase to come from the latest status object, got %q", stored.Status.Phase)
	}
	if stored.Status.Profile == nil || stored.Status.Profile.Name != "Helpful Otter" {
		t.Fatalf("expected stored profile to be preserved after retry, got %#v", stored.Status.Profile)
	}
}

func TestResolveAgentProfileSkipsSyncWhenOverridesAreComplete(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	called := false
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "resolved",
			"output": map[string]any{
				"profile": map[string]string{
					"name":     "Synced Otter",
					"imageUrl": "https://example.com/synced.png",
				},
			},
		})
	}))
	defer resolver.Close()
	configurePresetResolverTestServer(s, "", resolver.URL)

	body := &createRequest{
		RequestID: "req-1",
		PresetID:  "zeno",
		Annotations: map[string]string{
			instanceClassAnnotationKey: "personal-agent",
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
			AgentRef: &spritzv1.SpritzAgentRef{
				Type:     "external",
				Provider: "example-agent-catalog",
				ID:       "ag-123",
			},
			ProfileOverrides: &spritzv1.SpritzAgentProfile{
				Name:     "Helpful Otter",
				ImageURL: "https://example.com/override.png",
			},
		},
	}

	resolved := s.resolveAgentProfile(
		context.Background(),
		principal{ID: "user-1", Type: principalTypeHuman},
		s.namespace,
		body,
	)

	if called {
		t.Fatalf("expected complete overrides to skip profile sync")
	}
	if resolved == nil {
		t.Fatalf("expected resolved placeholder result")
	}
	if resolved.profile != nil {
		t.Fatalf("expected overrides-only flow to avoid synced profile payload, got %#v", resolved.profile)
	}
	if resolved.lastError != "" {
		t.Fatalf("expected overrides-only flow to avoid sync errors, got %q", resolved.lastError)
	}
}
