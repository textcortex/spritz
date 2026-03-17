package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestReconcileDeploymentUsesWorkspaceServiceAccountName(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image:              "example.com/openclaw:latest",
			ServiceAccountName: "zeno-agent-ag-123",
			Owner:              spritzv1.SpritzOwner{ID: "user-1"},
		},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(spritz).
		Build()
	reconciler := &SpritzReconciler{
		Client: k8sClient,
		Scheme: scheme,
	}

	if err := reconciler.reconcileDeployment(context.Background(), spritz); err != nil {
		t.Fatalf("reconcileDeployment returned error: %v", err)
	}

	deployment := &appsv1.Deployment{}
	if err := k8sClient.Get(
		context.Background(),
		client.ObjectKey{Name: spritz.Name, Namespace: spritz.Namespace},
		deployment,
	); err != nil {
		t.Fatalf("failed to load deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.ServiceAccountName != "zeno-agent-ag-123" {
		t.Fatalf(
			"expected deployment service account name %q, got %q",
			"zeno-agent-ag-123",
			deployment.Spec.Template.Spec.ServiceAccountName,
		)
	}
}
