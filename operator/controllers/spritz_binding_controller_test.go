package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

type failingCreateClient struct {
	client.Client
	err error
}

func (c *failingCreateClient) Create(
	ctx context.Context,
	obj client.Object,
	opts ...client.CreateOption,
) error {
	if _, ok := obj.(*spritzv1.Spritz); ok {
		return c.err
	}
	return c.Client.Create(ctx, obj, opts...)
}

type lingeringDeleteClient struct {
	client.Client
}

func (c *lingeringDeleteClient) Delete(
	ctx context.Context,
	obj client.Object,
	opts ...client.DeleteOption,
) error {
	return nil
}

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

	var cleanupPending spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &cleanupPending); err != nil {
		t.Fatalf("failed to load binding after cleanup request: %v", err)
	}
	if cleanupPending.Status.CleanupInstanceRef == nil || cleanupPending.Status.CleanupInstanceRef.Name != oldRuntime.Name {
		t.Fatalf("expected cleanup ref to stay until the next observe pass, got %#v", cleanupPending.Status)
	}
	if err := reconciler.reconcileBinding(context.Background(), &cleanupPending); err != nil {
		t.Fatalf("third reconcileBinding returned error: %v", err)
	}

	var finalized spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &finalized); err != nil {
		t.Fatalf("failed to load binding after cleanup completion: %v", err)
	}
	if finalized.Status.CleanupInstanceRef != nil {
		t.Fatalf("expected cleanup ref to be cleared, got %#v", finalized.Status)
	}
	if finalized.Status.Phase != spritzv1.BindingPhaseReady {
		t.Fatalf("expected ready phase after cleanup, got %#v", finalized.Status)
	}
}

func TestReconcileBindingRequeuesAfterCandidateCreateFailure(t *testing.T) {
	binding := newBindingTestBinding()
	reconciler, k8sClient := newBindingReconcilerForTest(t, binding)
	reconciler.Client = &failingCreateClient{
		Client: reconciler.Client,
		err:    fmt.Errorf("transient create failure"),
	}

	result, err := reconciler.Reconcile(
		context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(binding)},
	)
	if err != nil {
		t.Fatalf("expected create failure to be converted into a retry, got %v", err)
	}
	if result.RequeueAfter != 2*time.Second {
		t.Fatalf("expected reconcile to requeue after 2s, got %#v", result)
	}

	var storedBinding spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &storedBinding); err != nil {
		t.Fatalf("failed to load binding: %v", err)
	}
	if storedBinding.Status.Phase != spritzv1.BindingPhaseFailed {
		t.Fatalf("expected failed phase after create error, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.LastErrorCode != "candidate_create_failed" {
		t.Fatalf("expected candidate_create_failed code, got %#v", storedBinding.Status)
	}
}

func TestReconcileBindingKeepsCleanupRefWhileDeletionIsStillInFlight(t *testing.T) {
	binding := newBindingTestBinding()
	activeRuntime := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingRuntimeName(binding, 2),
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: "sha256:rev-2",
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	cleanupRuntime := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingRuntimeName(binding, 1),
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: "sha256:rev-1",
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	binding.Spec.DesiredRevision = "sha256:rev-2"
	binding.Status.ObservedRevision = "sha256:rev-2"
	binding.Status.ActiveInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      activeRuntime.Name,
		Revision:  "sha256:rev-2",
		Phase:     "Ready",
	}
	binding.Status.CleanupInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      cleanupRuntime.Name,
		Revision:  "sha256:rev-1",
		Phase:     "Ready",
	}
	reconciler, k8sClient := newBindingReconcilerForTest(t, binding, activeRuntime, cleanupRuntime)
	reconciler.Client = &lingeringDeleteClient{Client: reconciler.Client}

	if err := reconciler.reconcileBinding(context.Background(), binding); err != nil {
		t.Fatalf("reconcileBinding returned error: %v", err)
	}

	var storedBinding spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &storedBinding); err != nil {
		t.Fatalf("failed to load binding: %v", err)
	}
	if storedBinding.Status.CleanupInstanceRef == nil || storedBinding.Status.CleanupInstanceRef.Name != cleanupRuntime.Name {
		t.Fatalf("expected cleanup ref to stay until runtime deletion completes, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.Phase != spritzv1.BindingPhaseCleaningUp {
		t.Fatalf("expected cleaning_up phase, got %#v", storedBinding.Status)
	}

	var lingering spritzv1.Spritz
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cleanupRuntime), &lingering); err != nil {
		t.Fatalf("expected cleanup runtime to remain present while terminating: %v", err)
	}
}

func TestReconcileBindingMovesTerminalCandidateIntoCleanup(t *testing.T) {
	binding := newBindingTestBinding()
	activeRuntime := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingRuntimeName(binding, 1),
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: "sha256:rev-1",
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	terminalCandidate := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingRuntimeName(binding, 2),
			Namespace: binding.Namespace,
			Annotations: map[string]string{
				bindingTargetRevisionAnnotationKey: "sha256:rev-2",
			},
		},
		Status: spritzv1.SpritzStatus{Phase: "Error"},
	}
	binding.Spec.DesiredRevision = "sha256:rev-2"
	binding.Status.ObservedRevision = "sha256:rev-1"
	binding.Status.NextRuntimeSequence = 2
	binding.Status.ActiveInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      activeRuntime.Name,
		Revision:  "sha256:rev-1",
		Phase:     "Ready",
	}
	binding.Status.CandidateInstanceRef = &spritzv1.SpritzBindingInstanceRef{
		Namespace: binding.Namespace,
		Name:      terminalCandidate.Name,
		Revision:  "sha256:rev-2",
		Phase:     "Error",
	}
	reconciler, k8sClient := newBindingReconcilerForTest(t, binding, activeRuntime, terminalCandidate)

	if err := reconciler.reconcileBinding(context.Background(), binding); err != nil {
		t.Fatalf("reconcileBinding returned error: %v", err)
	}

	var storedBinding spritzv1.SpritzBinding
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(binding), &storedBinding); err != nil {
		t.Fatalf("failed to load binding: %v", err)
	}
	if storedBinding.Status.CleanupInstanceRef == nil || storedBinding.Status.CleanupInstanceRef.Name != terminalCandidate.Name {
		t.Fatalf("expected terminal candidate to move into cleanup, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.CandidateInstanceRef == nil {
		t.Fatalf("expected a fresh candidate to be created after terminal cleanup, got %#v", storedBinding.Status)
	}
	if storedBinding.Status.CandidateInstanceRef.Name == terminalCandidate.Name {
		t.Fatalf("expected a new candidate identity, got %#v", storedBinding.Status)
	}
}
