package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestReconcileLifecycleEnsuresOwnerLabel(t *testing.T) {
	scheme := newControllerTestScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "tidy-otter", Namespace: "spritz-test"},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(spritz).Build()
	reconciler := &SpritzReconciler{Client: k8sClient, Scheme: scheme}

	done, err := reconciler.reconcileLifecycle(context.Background(), spritz)
	if err != nil {
		t.Fatalf("reconcileLifecycle returned error: %v", err)
	}
	if !done {
		t.Fatal("expected reconcileLifecycle to request a requeue after updating metadata")
	}

	stored := &spritzv1.Spritz{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: spritz.Name, Namespace: spritz.Namespace}, stored); err != nil {
		t.Fatalf("failed to load updated spritz: %v", err)
	}
	if !controllerutil.ContainsFinalizer(stored, spritzFinalizer) {
		t.Fatalf("expected finalizer %q to be set", spritzFinalizer)
	}
	if stored.Labels["spritz.sh/owner"] != ownerLabelValue(spritz.Spec.Owner.ID) {
		t.Fatalf("expected owner label %q, got %#v", ownerLabelValue(spritz.Spec.Owner.ID), stored.Labels)
	}
}
