package controllers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xwebsocket "golang.org/x/net/websocket"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
	return scheme
}

func TestReconcileStatusStoresACPReadyCondition(t *testing.T) {
	wsServer := httptest.NewServer(xwebsocket.Handler(func(conn *xwebsocket.Conn) {
		defer conn.Close()
		var message map[string]any
		if err := xwebsocket.JSON.Receive(conn, &message); err != nil {
			t.Errorf("failed to read initialize request: %v", err)
			return
		}
		if message["method"] != "initialize" {
			t.Errorf("expected initialize request, got %#v", message["method"])
			return
		}
		if err := xwebsocket.JSON.Send(conn, map[string]any{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"result": map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"loadSession": true,
				},
				"agentInfo": map[string]any{
					"name":    "agent-otter",
					"title":   "Agent Otter",
					"version": "1.2.3",
				},
			},
		}); err != nil {
			t.Errorf("failed to send initialize response: %v", err)
		}
	}))
	defer wsServer.Close()

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
			Enabled:         true,
			Port:            spritzv1.DefaultACPPort,
			Path:            spritzv1.DefaultACPPath,
			ProbeTimeout:    2 * time.Second,
			RefreshInterval: 30 * time.Second,
			ClientInfo: acpImplementationInfo{
				Name:    "spritz-operator",
				Title:   "Spritz ACP Operator",
				Version: "1.0.0",
			},
			WorkspaceURL: func(namespace, name string) string {
				return strings.Replace(wsServer.URL, "http://", "ws://", 1)
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
			Enabled:         true,
			Port:            spritzv1.DefaultACPPort,
			Path:            spritzv1.DefaultACPPath,
			ProbeTimeout:    2 * time.Second,
			RefreshInterval: 30 * time.Second,
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
