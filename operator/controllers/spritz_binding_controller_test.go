package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newBindingTestBinding() *spritzv1.SpritzBinding {
	return &spritzv1.SpritzBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "channel-installation-binding-1",
			Namespace:  "spritz-test",
			Finalizers: []string{spritzBindingFinalizer},
		},
		Spec: spritzv1.SpritzBindingSpec{
			BindingKey:      "channel-installation-binding-1",
			DesiredRevision: "sha256:rev-1",
			Template: spritzv1.SpritzBindingTemplate{
				PresetID:   "zeno",
				NamePrefix: "zeno",
				Spec: spritzv1.SpritzSpec{
					Image: "example.com/openclaw:latest",
					Owner: spritzv1.SpritzOwner{ID: "user-1"},
				},
			},
		},
	}
}

func newBindingReconcilerForTest(t *testing.T, objects ...runtime.Object) (*SpritzBindingReconciler, client.Client) {
	t.Helper()
	scheme := newControllerTestScheme(t)
	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&spritzv1.SpritzBinding{})
	if len(objects) > 0 {
		builder = builder.WithRuntimeObjects(objects...)
	}
	k8sClient := builder.Build()
	return &SpritzBindingReconciler{
		Client: k8sClient,
		Scheme: scheme,
	}, k8sClient
}

func TestReconcileBindingCreatesInitialCandidateRuntime(t *testing.T) {
	binding := newBindingTestBinding()
	reconciler, k8sClient := newBindingReconcilerForTest(t, binding)

	if err := reconciler.reconcileBinding(context.Background(), binding); err != nil {
		t.Fatalf("reconcileBinding returned error: %v", err)
	}

	var storedBinding spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &storedBinding); err != nil {
		t.Fatalf("failed to load binding: %v", err)
	}
	if storedBinding.Status.Phase != spritzv1.BindingPhaseCreating {
		t.Fatalf("expected creating phase, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.CandidateInstanceRef == nil {
		t.Fatalf("expected candidate instance ref, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.NextRuntimeSequence != 1 {
		t.Fatalf("expected next runtime sequence 1, got %#v", storedBinding.Status)
	}

	var candidate spritzv1.Spritz
	if err := k8sClient.Get(
		context.Background(),
		client.ObjectKey{Namespace: binding.Namespace, Name: storedBinding.Status.CandidateInstanceRef.Name},
		&candidate,
	); err != nil {
		t.Fatalf("failed to load candidate runtime: %v", err)
	}
	if candidate.Annotations[spritzv1.BindingKeyAnnotationKey] != binding.Spec.BindingKey {
		t.Fatalf("expected binding key annotation on candidate, got %#v", candidate.Annotations)
	}
	if candidate.Labels[spritzv1.BindingNameLabelKey] != binding.Name {
		t.Fatalf("expected binding name label on candidate, got %#v", candidate.Labels)
	}
}

func TestReconcileBindingPromotesReadyInitialCandidate(t *testing.T) {
	binding := newBindingTestBinding()
	candidateName := bindingRuntimeName(binding, 1)
	candidate := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      candidateName,
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: binding.Spec.DesiredRevision,
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	binding.Status.CandidateInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      candidate.Name,
		Revision:  binding.Spec.DesiredRevision,
		Phase:     "Ready",
	}
	binding.Status.NextRuntimeSequence = 1
	reconciler, k8sClient := newBindingReconcilerForTest(t, binding, candidate)

	if err := reconciler.reconcileBinding(context.Background(), binding); err != nil {
		t.Fatalf("reconcileBinding returned error: %v", err)
	}

	var storedBinding spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &storedBinding); err != nil {
		t.Fatalf("failed to load binding: %v", err)
	}
	if storedBinding.Status.ActiveInstanceRef == nil || storedBinding.Status.ActiveInstanceRef.Name != candidate.Name {
		t.Fatalf("expected candidate to become active, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.CandidateInstanceRef != nil {
		t.Fatalf("expected candidate ref to be cleared, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.ObservedRevision != binding.Spec.DesiredRevision {
		t.Fatalf("expected observed revision %q, got %#v", binding.Spec.DesiredRevision, storedBinding.Status)
	}
	if storedBinding.Status.Phase != spritzv1.BindingPhaseReady {
		t.Fatalf("expected ready phase, got %#v", storedBinding.Status)
	}
}

func TestReconcileBindingCutsOverReadyReplacementAndCleansUpOldRuntime(t *testing.T) {
	binding := newBindingTestBinding()
	oldRuntimeName := bindingRuntimeName(binding, 1)
	newRuntimeName := bindingRuntimeName(binding, 2)
	oldRuntime := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oldRuntimeName,
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: "sha256:rev-1",
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	newRuntime := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      newRuntimeName,
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: "sha256:rev-2",
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	binding.Spec.DesiredRevision = "sha256:rev-2"
	binding.Status.ObservedRevision = "sha256:rev-1"
	binding.Status.ActiveInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      oldRuntime.Name,
		Revision:  "sha256:rev-1",
		Phase:     "Ready",
	}
	binding.Status.CandidateInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      newRuntime.Name,
		Revision:  "sha256:rev-2",
		Phase:     "Ready",
	}
	binding.Status.NextRuntimeSequence = 2
	reconciler, k8sClient := newBindingReconcilerForTest(t, binding, oldRuntime, newRuntime)

	if err := reconciler.reconcileBinding(context.Background(), binding); err != nil {
		t.Fatalf("first reconcileBinding returned error: %v", err)
	}

	var cuttingOver spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &cuttingOver); err != nil {
		t.Fatalf("failed to load binding after cutover: %v", err)
	}
	if cuttingOver.Status.ActiveInstanceRef == nil || cuttingOver.Status.ActiveInstanceRef.Name != newRuntime.Name {
		t.Fatalf("expected replacement to become active, got %#v", cuttingOver.Status)
	}
	if cuttingOver.Status.CleanupInstanceRef == nil || cuttingOver.Status.CleanupInstanceRef.Name != oldRuntime.Name {
		t.Fatalf("expected old runtime to move into cleanup, got %#v", cuttingOver.Status)
	}
	if cuttingOver.Status.Phase != spritzv1.BindingPhaseCleaningUp {
		t.Fatalf("expected cleaning_up phase, got %#v", cuttingOver.Status)
	}

	if err := reconciler.reconcileBinding(context.Background(), &cuttingOver); err != nil {
		t.Fatalf("second reconcileBinding returned error: %v", err)
	}

	var finalized spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &finalized); err != nil {
		t.Fatalf("failed to load binding after cleanup: %v", err)
	}
	if finalized.Status.CleanupInstanceRef != nil {
		t.Fatalf("expected cleanup ref to be cleared, got %#v", finalized.Status)
	}
	if finalized.Status.Phase != spritzv1.BindingPhaseReady {
		t.Fatalf("expected ready phase after cleanup, got %#v", finalized.Status)
	}
	var deletedOld spritzv1.Spritz
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(oldRuntime), &deletedOld); err == nil {
		t.Fatalf("expected old runtime to be deleted during cleanup")
	}
}
