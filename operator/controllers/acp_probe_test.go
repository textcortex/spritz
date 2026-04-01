package controllers

import (
	"context"
	"encoding/json"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"net/http/httptest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newControllerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register spritz scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register core scheme: %v", err)
	}
	return scheme
}

func TestReconcileStatusStoresACPReadyCondition(t *testing.T) {
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/.well-known/spritz-acp":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"loadSession": true,
				},
				"agentInfo": map[string]any{
					"name":    "agent-otter",
					"title":   "Agent Otter",
					"version": "1.2.3",
				},
				"authMethods": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer metadataServer.Close()

	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: 1},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&spritzv1.Spritz{}).
		WithObjects(spritz, deployment).
		Build()
	reconciler := &SpritzReconciler{
		Client: k8sClient,
		Scheme: scheme,
		ACP: ACPProbeConfig{
			Enabled:                 true,
			Port:                    spritzv1.DefaultACPPort,
			Path:                    spritzv1.DefaultACPPath,
			ProbeTimeout:            2 * time.Second,
			RefreshInterval:         30 * time.Second,
			MetadataRefreshInterval: 5 * time.Minute,
			HealthPath:              "/healthz",
			MetadataPath:            "/.well-known/spritz-acp",
			ClientInfo: acpImplementationInfo{
				Name:    "spritz-operator",
				Title:   "Spritz ACP Operator",
				Version: "1.0.0",
			},
			InstanceURL: func(namespace, name string) string {
				return metadataServer.URL
			},
		},
	}

	if _, err := reconciler.reconcileStatus(context.Background(), spritz); err != nil {
		t.Fatalf("reconcileStatus returned error: %v", err)
	}

	stored := &spritzv1.Spritz{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: spritz.Namespace, Name: spritz.Name}, stored); err != nil {
		t.Fatalf("failed to load updated spritz: %v", err)
	}
	if stored.Status.ACP == nil || stored.Status.ACP.State != "ready" {
		t.Fatalf("expected ACP ready status, got %#v", stored.Status.ACP)
	}
	if stored.Status.ACP.AgentInfo == nil || stored.Status.ACP.AgentInfo.Title != "Agent Otter" {
		t.Fatalf("expected agent info from ACP probe, got %#v", stored.Status.ACP.AgentInfo)
	}
	if stored.Status.ACP.LastMetadataAt == nil {
		t.Fatalf("expected ACP last metadata timestamp to be set")
	}
	condition := meta.FindStatusCondition(stored.Status.Conditions, "ACPReady")
	if condition == nil {
		t.Fatalf("expected ACPReady condition to be set")
	}
	if condition.Status != metav1.ConditionTrue {
		t.Fatalf("expected ACPReady condition true, got %s", condition.Status)
	}
}

func TestReconcileStatusMarksACPUnknownWhileProvisioning(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "slow-reef", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&spritzv1.Spritz{}).
		WithObjects(spritz).
		Build()
	reconciler := &SpritzReconciler{
		Client: k8sClient,
		Scheme: scheme,
		ACP: ACPProbeConfig{
			Enabled:                 true,
			Port:                    spritzv1.DefaultACPPort,
			Path:                    spritzv1.DefaultACPPath,
			ProbeTimeout:            2 * time.Second,
			RefreshInterval:         30 * time.Second,
			MetadataRefreshInterval: 5 * time.Minute,
			HealthPath:              "/healthz",
			MetadataPath:            "/.well-known/spritz-acp",
		},
	}

	if _, err := reconciler.reconcileStatus(context.Background(), spritz); err != nil {
		t.Fatalf("reconcileStatus returned error: %v", err)
	}

	stored := &spritzv1.Spritz{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: spritz.Namespace, Name: spritz.Name}, stored); err != nil {
		t.Fatalf("failed to load updated spritz: %v", err)
	}
	if stored.Status.ACP == nil || stored.Status.ACP.State != "unknown" {
		t.Fatalf("expected ACP state unknown while provisioning, got %#v", stored.Status.ACP)
	}
	condition := meta.FindStatusCondition(stored.Status.Conditions, "ACPReady")
	if condition == nil {
		t.Fatalf("expected ACPReady condition to be set")
	}
	if condition.Status != metav1.ConditionFalse || condition.Reason != "Unknown" {
		t.Fatalf("expected ACPReady false/Unknown, got %#v", condition)
	}
}

func TestReconcileStatusUsesHealthChecksBetweenMetadataRefreshes(t *testing.T) {
	var healthRequests int
	var metadataRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			healthRequests++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/.well-known/spritz-acp":
			metadataRequests++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"loadSession": true,
				},
				"agentInfo": map[string]any{
					"name":    "agent-otter",
					"title":   "Agent Otter",
					"version": "1.2.3",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	scheme := newControllerTestScheme(t)
	now := metav1.NewTime(time.Now().Add(-31 * time.Second))
	metadataAt := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "steady-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
		Status: spritzv1.SpritzStatus{
			ACP: &spritzv1.SpritzACPStatus{
				State:          "ready",
				LastProbeAt:    &now,
				LastMetadataAt: &metadataAt,
				AgentInfo: &spritzv1.SpritzACPAgentInfo{
					Name:    "agent-otter",
					Title:   "Agent Otter",
					Version: "1.2.3",
				},
				Capabilities: &spritzv1.SpritzACPCapabilities{LoadSession: true},
			},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "steady-otter", Namespace: "spritz-test"},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: 1},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&spritzv1.Spritz{}).
		WithObjects(spritz, deployment).
		Build()
	reconciler := &SpritzReconciler{
		Client: k8sClient,
		Scheme: scheme,
		ACP: ACPProbeConfig{
			Enabled:                 true,
			Port:                    spritzv1.DefaultACPPort,
			Path:                    spritzv1.DefaultACPPath,
			ProbeTimeout:            2 * time.Second,
			RefreshInterval:         30 * time.Second,
			MetadataRefreshInterval: 10 * time.Minute,
			HealthPath:              "/healthz",
			MetadataPath:            "/.well-known/spritz-acp",
			InstanceURL: func(namespace, name string) string {
				return server.URL
			},
		},
	}

	if _, err := reconciler.reconcileStatus(context.Background(), spritz); err != nil {
		t.Fatalf("reconcileStatus returned error: %v", err)
	}

	if healthRequests != 1 {
		t.Fatalf("expected exactly one health request, got %d", healthRequests)
	}
	if metadataRequests != 0 {
		t.Fatalf("expected no metadata refresh, got %d", metadataRequests)
	}
}
