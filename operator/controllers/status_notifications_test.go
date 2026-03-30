package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestSetStatusDoesNotBlockOnLifecycleNotificationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "notify unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "steady-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Provisioning"},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&spritzv1.Spritz{}).
		WithObjects(spritz).
		Build()
	reconciler := &SpritzReconciler{
		Client: k8sClient,
		Scheme: scheme,
		LifecycleNotifications: LifecycleNotificationConfig{
			URL:     server.URL,
			Timeout: time.Second,
			Client:  server.Client(),
		},
	}

	if err := reconciler.setStatus(
		context.Background(),
		spritz,
		"Ready",
		"https://spritz.example.test",
		nil,
		"Ready",
		"spritz ready",
		nil,
	); err != nil {
		t.Fatalf("setStatus returned error: %v", err)
	}

	stored := &spritzv1.Spritz{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(spritz), stored); err != nil {
		t.Fatalf("failed to load updated spritz: %v", err)
	}
	if stored.Status.Phase != "Ready" {
		t.Fatalf("expected phase to be updated despite notification failure, got %q", stored.Status.Phase)
	}
}

func TestSetStatusNotifiesAfterPersistedPhaseUpdate(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
		Status: spritzv1.SpritzStatus{Phase: "Provisioning"},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&spritzv1.Spritz{}).
		WithObjects(spritz).
		Build()

	var observedStoredPhase atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stored := &spritzv1.Spritz{}
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(spritz), stored); err != nil {
			t.Fatalf("failed to load persisted spritz during notification: %v", err)
		}
		observedStoredPhase.Store(stored.Status.Phase)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	reconciler := &SpritzReconciler{
		Client: k8sClient,
		Scheme: scheme,
		LifecycleNotifications: LifecycleNotificationConfig{
			URL:     server.URL,
			Timeout: time.Second,
			Client:  server.Client(),
		},
	}

	if err := reconciler.setStatus(
		context.Background(),
		spritz,
		"Ready",
		"https://spritz.example.test",
		nil,
		"Ready",
		"spritz ready",
		nil,
	); err != nil {
		t.Fatalf("setStatus returned error: %v", err)
	}

	if got, _ := observedStoredPhase.Load().(string); got != "Ready" {
		t.Fatalf("expected notification to observe persisted Ready phase, got %q", got)
	}
}
