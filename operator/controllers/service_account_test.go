package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestReconcileDeploymentUsesInstanceServiceAccountName(t *testing.T) {
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

func TestReconcileDeploymentKeepsRuntimePolicyLabelsOutOfSelector(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
			RuntimePolicy: &spritzv1.SpritzRuntimePolicy{
				NetworkProfile:  "dev-cluster-only",
				MountProfile:    "dev-default",
				ExposureProfile: "internal-acp",
				Revision:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
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
	if deployment.Spec.Selector == nil {
		t.Fatal("expected deployment selector")
	}
	if len(deployment.Spec.Selector.MatchLabels) != 1 ||
		deployment.Spec.Selector.MatchLabels["spritz.sh/name"] != spritz.Name {
		t.Fatalf("expected stable deployment selector, got %#v", deployment.Spec.Selector.MatchLabels)
	}
	if deployment.Spec.Template.Labels[runtimeNetworkProfileLabelKey] != "dev-cluster-only" {
		t.Fatalf("expected runtime policy label on pod template, got %#v", deployment.Spec.Template.Labels)
	}
}

func TestReconcileDeploymentPropagatesSpecLabelsToPodTemplate(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
			Labels: map[string]string{
				"sidecar.istio.io/inject": "true",
				"example.com/runtime":     "dev",
			},
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
	if deployment.Spec.Template.Labels["sidecar.istio.io/inject"] != "true" {
		t.Fatalf("expected sidecar injection label on pod template, got %#v", deployment.Spec.Template.Labels)
	}
	if deployment.Spec.Template.Labels["example.com/runtime"] != "dev" {
		t.Fatalf("expected custom spec label on pod template, got %#v", deployment.Spec.Template.Labels)
	}
	if _, ok := deployment.Spec.Selector.MatchLabels["sidecar.istio.io/inject"]; ok {
		t.Fatalf("deployment selector must not depend on spec labels: %#v", deployment.Spec.Selector.MatchLabels)
	}
}

func TestReconcileDeploymentKeepsSelectorLabelsAuthoritativeOnPodTemplate(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
			Labels: map[string]string{
				"spritz.sh/name":  "spoofed-name",
				"spritz.sh/owner": "spoofed-owner",
			},
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
	if deployment.Spec.Template.Labels["spritz.sh/name"] != spritz.Name {
		t.Fatalf("expected pod template to keep canonical spritz name label, got %#v", deployment.Spec.Template.Labels)
	}
	if deployment.Spec.Template.Labels[ownerLabelKey] != ownerLabelValue("user-1") {
		t.Fatalf("expected pod template to keep canonical owner label, got %#v", deployment.Spec.Template.Labels)
	}
}

func TestReconcileDeploymentPreservesExistingSelectorOnUpgrade(t *testing.T) {
	scheme := newControllerTestScheme(t)
	oldSelector := map[string]string{
		"spritz.sh/name": "tidy-otter",
		ownerLabelKey:    ownerLabelValue("user-1"),
	}
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
			RuntimePolicy: &spritzv1.SpritzRuntimePolicy{
				NetworkProfile:  "dev-cluster-only",
				MountProfile:    "dev-default",
				ExposureProfile: "internal-acp",
				Revision:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	}
	existingDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: oldSelector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: oldSelector},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: spritzContainerName, Image: spritz.Spec.Image},
					},
				},
			},
		},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(spritz, existingDeployment).
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
	if deployment.Spec.Selector == nil {
		t.Fatal("expected deployment selector")
	}
	if deployment.Spec.Selector.MatchLabels["spritz.sh/name"] != spritz.Name {
		t.Fatalf("expected existing deployment selector to keep spritz name, got %#v", deployment.Spec.Selector.MatchLabels)
	}
	if deployment.Spec.Selector.MatchLabels[ownerLabelKey] != ownerLabelValue("user-1") {
		t.Fatalf("expected existing deployment selector to keep owner label, got %#v", deployment.Spec.Selector.MatchLabels)
	}
	if deployment.Spec.Template.Labels[runtimeNetworkProfileLabelKey] != "dev-cluster-only" {
		t.Fatalf("expected runtime policy label on pod template, got %#v", deployment.Spec.Template.Labels)
	}
}

func TestReconcileServiceKeepsRuntimePolicyLabelsOutOfSelector(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
			RuntimePolicy: &spritzv1.SpritzRuntimePolicy{
				NetworkProfile:  "dev-cluster-only",
				MountProfile:    "dev-default",
				ExposureProfile: "internal-acp",
				Revision:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
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

	if err := reconciler.reconcileService(context.Background(), spritz); err != nil {
		t.Fatalf("reconcileService returned error: %v", err)
	}

	service := &corev1.Service{}
	if err := k8sClient.Get(
		context.Background(),
		client.ObjectKey{Name: spritz.Name, Namespace: spritz.Namespace},
		service,
	); err != nil {
		t.Fatalf("failed to load service: %v", err)
	}
	if len(service.Spec.Selector) != 1 ||
		service.Spec.Selector["spritz.sh/name"] != spritz.Name {
		t.Fatalf("expected stable service selector, got %#v", service.Spec.Selector)
	}
	if _, ok := service.Spec.Selector[runtimeNetworkProfileLabelKey]; ok {
		t.Fatalf("service selector must not depend on runtime policy labels: %#v", service.Spec.Selector)
	}
}
