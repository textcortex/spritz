package controllers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	spritzv1 "spritz.sh/operator/api/v1"
)

const spritzBindingFinalizer = "spritz.sh/binding-finalizer"
const (
	bindingPresetIDAnnotationKey       = "spritz.sh/preset-id"
	bindingTargetRevisionAnnotationKey = "spritz.sh/target-revision"
)

type bindingIngressDefaults struct {
	Mode               string
	HostTemplate       string
	Path               string
	ClassName          string
	GatewayName        string
	GatewayNamespace   string
	GatewaySectionName string
}

func NewBindingIngressDefaultsFromEnv() bindingIngressDefaults {
	return bindingIngressDefaults{
		Mode:               strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_MODE")),
		HostTemplate:       strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_HOST_TEMPLATE")),
		Path:               strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_PATH")),
		ClassName:          strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_CLASS_NAME")),
		GatewayName:        strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_GATEWAY_NAME")),
		GatewayNamespace:   strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_GATEWAY_NAMESPACE")),
		GatewaySectionName: strings.TrimSpace(os.Getenv("SPRITZ_DEFAULT_INGRESS_GATEWAY_SECTION_NAME")),
	}
}

func (d bindingIngressDefaults) enabled() bool {
	return d.Mode != "" || d.HostTemplate != "" || d.Path != "" || d.ClassName != "" ||
		d.GatewayName != "" || d.GatewayNamespace != "" || d.GatewaySectionName != ""
}

type SpritzBindingReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	IngressDefaults bindingIngressDefaults
}

func (r *SpritzBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	logger := log.FromContext(ctx)

	var binding spritzv1.SpritzBinding
	if err := r.Get(ctx, req.NamespacedName, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if done, err := r.reconcileLifecycle(ctx, &binding); done || err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileBinding(ctx, &binding); err != nil {
		logger.Error(err, "failed to reconcile spritz binding")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, err
	}

	return ctrl.Result{}, nil
}

func (r *SpritzBindingReconciler) reconcileLifecycle(ctx context.Context, binding *spritzv1.SpritzBinding) (bool, error) {
	if !binding.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(binding, spritzBindingFinalizer) {
			controllerutil.RemoveFinalizer(binding, spritzBindingFinalizer)
			if err := r.Update(ctx, binding); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if controllerutil.ContainsFinalizer(binding, spritzBindingFinalizer) {
		return false, nil
	}
	controllerutil.AddFinalizer(binding, spritzBindingFinalizer)
	if err := r.Update(ctx, binding); err != nil {
		return true, err
	}
	return true, nil
}

func (r *SpritzBindingReconciler) reconcileBinding(ctx context.Context, binding *spritzv1.SpritzBinding) error {
	now := metav1.Now()

	if updated, err := r.adoptExistingRuntime(ctx, binding, &now); err != nil {
		return err
	} else if updated {
		return nil
	}

	active, activeExists, err := r.resolveRuntimeRef(ctx, binding, binding.Status.ActiveInstanceRef)
	if err != nil {
		return err
	}
	candidate, candidateExists, err := r.resolveRuntimeRef(ctx, binding, binding.Status.CandidateInstanceRef)
	if err != nil {
		return err
	}
	cleanup, cleanupExists, err := r.resolveRuntimeRef(ctx, binding, binding.Status.CleanupInstanceRef)
	if err != nil {
		return err
	}

	if cleanupExists {
		if err := r.deleteRuntimeIfPresent(ctx, cleanup); err != nil {
			r.setFailureStatus(binding, &now, spritzv1.BindingPhaseCleaningUp, "cleanup_failed", err.Error())
			return r.updateBindingStatus(ctx, binding)
		}
		binding.Status.CleanupInstanceRef = nil
	} else if binding.Status.CleanupInstanceRef != nil {
		binding.Status.CleanupInstanceRef = nil
	}

	if activeExists && binding.Status.ActiveInstanceRef != nil {
		binding.Status.ActiveInstanceRef.Phase = strings.TrimSpace(active.Status.Phase)
	}
	if candidateExists && binding.Status.CandidateInstanceRef != nil {
		binding.Status.CandidateInstanceRef.Phase = strings.TrimSpace(candidate.Status.Phase)
	}

	if binding.Status.ActiveInstanceRef != nil && !activeExists {
		binding.Status.ActiveInstanceRef = nil
		active = nil
	}
	if binding.Status.CandidateInstanceRef != nil && !candidateExists {
		binding.Status.CandidateInstanceRef = nil
		candidate = nil
	}

	if binding.Status.ActiveInstanceRef != nil && active != nil && !runtimeIsUsable(active) {
		binding.Status.ActiveInstanceRef = nil
		active = nil
	}
	if binding.Status.CandidateInstanceRef != nil && candidate != nil && runtimeIsTerminal(candidate) {
		binding.Status.CandidateInstanceRef = nil
		candidate = nil
	}

	desiredRevision := strings.TrimSpace(binding.Spec.DesiredRevision)

	if binding.Status.ActiveInstanceRef == nil {
		if binding.Status.CandidateInstanceRef == nil {
			nextRef, err := r.ensureCandidateRuntime(ctx, binding, desiredRevision)
			if err != nil {
				r.setFailureStatus(binding, &now, spritzv1.BindingPhaseFailed, "candidate_create_failed", err.Error())
				return r.updateBindingStatus(ctx, binding)
			}
			binding.Status.CandidateInstanceRef = nextRef
			r.setProgressingStatus(binding, &now, spritzv1.BindingPhaseCreating, "candidate_creating", "creating initial runtime")
			return r.updateBindingStatus(ctx, binding)
		}
		if candidate != nil && runtimeIsReady(candidate) {
			binding.Status.ActiveInstanceRef = binding.Status.CandidateInstanceRef.DeepCopy()
			binding.Status.CandidateInstanceRef = nil
			binding.Status.ObservedRevision = resolveInstanceRevision(binding.Status.ActiveInstanceRef, candidate, desiredRevision)
			r.setReadyStatus(binding, &now, spritzv1.BindingPhaseReady)
			return r.updateBindingStatus(ctx, binding)
		}
		r.setProgressingStatus(binding, &now, spritzv1.BindingPhaseWaitingReady, "candidate_not_ready", "waiting for initial candidate to become ready")
		return r.updateBindingStatus(ctx, binding)
	}

	if desiredRevision != "" && strings.TrimSpace(binding.Status.ObservedRevision) != desiredRevision {
		if binding.Status.CandidateInstanceRef == nil {
			nextRef, err := r.ensureCandidateRuntime(ctx, binding, desiredRevision)
			if err != nil {
				r.setFailureStatus(binding, &now, spritzv1.BindingPhaseFailed, "candidate_create_failed", err.Error())
				return r.updateBindingStatus(ctx, binding)
			}
			binding.Status.CandidateInstanceRef = nextRef
			r.setProgressingStatus(binding, &now, spritzv1.BindingPhaseCreating, "candidate_creating", "creating replacement runtime")
			return r.updateBindingStatus(ctx, binding)
		}
		if candidate != nil && runtimeIsReady(candidate) {
			previousActiveRef := binding.Status.ActiveInstanceRef.DeepCopy()
			binding.Status.ActiveInstanceRef = binding.Status.CandidateInstanceRef.DeepCopy()
			binding.Status.CandidateInstanceRef = nil
			binding.Status.CleanupInstanceRef = previousActiveRef
			binding.Status.ObservedRevision = resolveInstanceRevision(binding.Status.ActiveInstanceRef, candidate, desiredRevision)
			r.setProgressingStatus(binding, &now, spritzv1.BindingPhaseCleaningUp, "cleanup_pending", "cleaning up replaced runtime")
			return r.updateBindingStatus(ctx, binding)
		}
		r.setProgressingStatus(binding, &now, spritzv1.BindingPhaseWaitingReady, "candidate_not_ready", "waiting for replacement runtime to become ready")
		return r.updateBindingStatus(ctx, binding)
	}

	if binding.Status.CleanupInstanceRef != nil {
		r.setProgressingStatus(binding, &now, spritzv1.BindingPhaseCleaningUp, "cleanup_pending", "cleaning up replaced runtime")
		return r.updateBindingStatus(ctx, binding)
	}

	r.setReadyStatus(binding, &now, spritzv1.BindingPhaseReady)
	if active != nil && binding.Status.ActiveInstanceRef != nil {
		binding.Status.ObservedRevision = resolveInstanceRevision(binding.Status.ActiveInstanceRef, active, desiredRevision)
	}
	return r.updateBindingStatus(ctx, binding)
}

func (r *SpritzBindingReconciler) adoptExistingRuntime(
	ctx context.Context,
	binding *spritzv1.SpritzBinding,
	now *metav1.Time,
) (bool, error) {
	if binding.Status.ActiveInstanceRef != nil || binding.Spec.AdoptActive == nil {
		return false, nil
	}
	ref := binding.Spec.AdoptActive.DeepCopy()
	if strings.TrimSpace(ref.Namespace) == "" {
		ref.Namespace = binding.Namespace
	}
	var spritz spritzv1.Spritz
	if err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if err := r.attachBindingOwnership(ctx, binding, &spritz); err != nil {
		return false, err
	}
	ref.Revision = resolveInstanceRevision(ref, &spritz, strings.TrimSpace(binding.Spec.AdoptedRevision))
	ref.Phase = strings.TrimSpace(spritz.Status.Phase)
	binding.Status.ActiveInstanceRef = ref
	binding.Status.CandidateInstanceRef = nil
	binding.Status.CleanupInstanceRef = nil
	binding.Status.ObservedRevision = ref.Revision
	r.setReadyStatus(binding, now, spritzv1.BindingPhaseReady)
	return true, r.updateBindingStatus(ctx, binding)
}

func (r *SpritzBindingReconciler) ensureCandidateRuntime(
	ctx context.Context,
	binding *spritzv1.SpritzBinding,
	desiredRevision string,
) (*spritzv1.SpritzBindingInstanceRef, error) {
	sequence := binding.Status.NextRuntimeSequence + 1
	name := bindingRuntimeName(binding, sequence)
	spritz, err := r.buildRuntimeFromBinding(binding, name, desiredRevision, "candidate")
	if err != nil {
		return nil, err
	}
	if err := r.Create(ctx, spritz); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		var existing spritzv1.Spritz
		if getErr := r.Get(ctx, client.ObjectKey{Namespace: spritz.Namespace, Name: spritz.Name}, &existing); getErr != nil {
			return nil, getErr
		}
		if err := r.attachBindingOwnership(ctx, binding, &existing); err != nil {
			return nil, err
		}
		spritz = &existing
	}
	binding.Status.NextRuntimeSequence = sequence
	return &spritzv1.SpritzBindingInstanceRef{
		Namespace: spritz.Namespace,
		Name:      spritz.Name,
		Revision:  resolveInstanceRevision(nil, spritz, desiredRevision),
		Phase:     strings.TrimSpace(spritz.Status.Phase),
	}, nil
}

func (r *SpritzBindingReconciler) buildRuntimeFromBinding(
	binding *spritzv1.SpritzBinding,
	name string,
	desiredRevision string,
	role string,
) (*spritzv1.Spritz, error) {
	var spec spritzv1.SpritzSpec
	binding.Spec.Template.Spec.DeepCopyInto(&spec)
	applyBindingIngressDefaults(&spec, name, binding.Namespace, r.IngressDefaults)
	if spec.Ingress != nil && strings.EqualFold(spec.Ingress.Mode, "gateway") && strings.TrimSpace(spec.Ingress.Host) == "" {
		return nil, fmt.Errorf("spec.ingress.host is required when spec.ingress.mode=gateway")
	}
	if spec.Ingress != nil && strings.EqualFold(spec.Ingress.Mode, "gateway") && strings.TrimSpace(spec.Ingress.GatewayName) == "" {
		return nil, fmt.Errorf("spec.ingress.gatewayName is required when spec.ingress.mode=gateway")
	}

	labels := cloneStringMap(binding.Spec.Template.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels["spritz.sh/name"] = name
	labels[spritzv1.BindingNameLabelKey] = binding.Name

	annotations := cloneStringMap(binding.Spec.Template.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	if strings.TrimSpace(binding.Spec.Template.PresetID) != "" {
		annotations[bindingPresetIDAnnotationKey] = strings.TrimSpace(binding.Spec.Template.PresetID)
	}
	if strings.TrimSpace(desiredRevision) != "" {
		annotations[bindingTargetRevisionAnnotationKey] = strings.TrimSpace(desiredRevision)
	}
	annotations[spritzv1.BindingKeyAnnotationKey] = strings.TrimSpace(binding.Spec.BindingKey)
	annotations[spritzv1.BindingInstanceRoleAnnotationKey] = strings.TrimSpace(role)

	spritz := &spritzv1.Spritz{
		TypeMeta: metav1.TypeMeta{Kind: "Spritz", APIVersion: spritzv1.GroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   binding.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: spec,
	}
	if err := controllerutil.SetControllerReference(binding, spritz, r.Scheme); err != nil {
		return nil, err
	}
	return spritz, nil
}

func (r *SpritzBindingReconciler) resolveRuntimeRef(
	ctx context.Context,
	binding *spritzv1.SpritzBinding,
	ref *spritzv1.SpritzBindingInstanceRef,
) (*spritzv1.Spritz, bool, error) {
	if ref == nil {
		return nil, false, nil
	}
	namespace := strings.TrimSpace(ref.Namespace)
	if namespace == "" {
		namespace = binding.Namespace
	}
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return nil, false, nil
	}
	var spritz spritzv1.Spritz
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &spritz, true, nil
}

func (r *SpritzBindingReconciler) deleteRuntimeIfPresent(ctx context.Context, spritz *spritzv1.Spritz) error {
	if spritz == nil {
		return nil
	}
	if err := r.Delete(ctx, spritz); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (r *SpritzBindingReconciler) attachBindingOwnership(
	ctx context.Context,
	binding *spritzv1.SpritzBinding,
	spritz *spritzv1.Spritz,
) error {
	if spritz == nil {
		return nil
	}
	updated := false
	if spritz.Labels == nil {
		spritz.Labels = map[string]string{}
	}
	if spritz.Annotations == nil {
		spritz.Annotations = map[string]string{}
	}
	if !metav1.IsControlledBy(spritz, binding) {
		updated = true
	}
	if spritz.Labels[spritzv1.BindingNameLabelKey] != binding.Name {
		spritz.Labels[spritzv1.BindingNameLabelKey] = binding.Name
		updated = true
	}
	if spritz.Annotations[spritzv1.BindingKeyAnnotationKey] != strings.TrimSpace(binding.Spec.BindingKey) {
		spritz.Annotations[spritzv1.BindingKeyAnnotationKey] = strings.TrimSpace(binding.Spec.BindingKey)
		updated = true
	}
	if err := controllerutil.SetControllerReference(binding, spritz, r.Scheme); err != nil {
		return err
	}
	if !updated {
		return nil
	}
	return r.Update(ctx, spritz)
}

func runtimeIsReady(spritz *spritzv1.Spritz) bool {
	return spritz != nil && strings.EqualFold(strings.TrimSpace(spritz.Status.Phase), "Ready")
}

func runtimeIsUsable(spritz *spritzv1.Spritz) bool {
	if spritz == nil {
		return false
	}
	phase := strings.TrimSpace(strings.ToLower(spritz.Status.Phase))
	return phase == "ready" || phase == "provisioning"
}

func runtimeIsTerminal(spritz *spritzv1.Spritz) bool {
	if spritz == nil {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(spritz.Status.Phase)) {
	case "error", "expired", "terminating":
		return true
	default:
		return false
	}
}

func resolveInstanceRevision(ref *spritzv1.SpritzBindingInstanceRef, spritz *spritzv1.Spritz, fallback string) string {
	if ref != nil && strings.TrimSpace(ref.Revision) != "" {
		return strings.TrimSpace(ref.Revision)
	}
	if spritz != nil {
		if revision := strings.TrimSpace(spritz.GetAnnotations()[bindingTargetRevisionAnnotationKey]); revision != "" {
			return revision
		}
	}
	return strings.TrimSpace(fallback)
}

func (r *SpritzBindingReconciler) updateBindingStatus(ctx context.Context, binding *spritzv1.SpritzBinding) error {
	return r.Status().Update(ctx, binding)
}

func (r *SpritzBindingReconciler) setReadyStatus(binding *spritzv1.SpritzBinding, now *metav1.Time, phase string) {
	binding.Status.Phase = phase
	binding.Status.LastErrorCode = ""
	binding.Status.LastErrorMessage = ""
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.LastReconciledAt = now.DeepCopy()
	metaSetBindingReadyCondition(&binding.Status.Conditions, binding.Generation, metav1.ConditionTrue, "Ready", "binding is ready")
	metaSetBindingProgressingCondition(&binding.Status.Conditions, binding.Generation, metav1.ConditionFalse, "Ready", "binding is stable")
}

func (r *SpritzBindingReconciler) setProgressingStatus(
	binding *spritzv1.SpritzBinding,
	now *metav1.Time,
	phase string,
	reason string,
	message string,
) {
	binding.Status.Phase = phase
	binding.Status.LastErrorCode = ""
	binding.Status.LastErrorMessage = ""
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.LastReconciledAt = now.DeepCopy()
	metaSetBindingReadyCondition(&binding.Status.Conditions, binding.Generation, metav1.ConditionFalse, reason, message)
	metaSetBindingProgressingCondition(&binding.Status.Conditions, binding.Generation, metav1.ConditionTrue, reason, message)
}

func (r *SpritzBindingReconciler) setFailureStatus(
	binding *spritzv1.SpritzBinding,
	now *metav1.Time,
	phase string,
	code string,
	message string,
) {
	binding.Status.Phase = phase
	binding.Status.LastErrorCode = strings.TrimSpace(code)
	binding.Status.LastErrorMessage = strings.TrimSpace(message)
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.LastReconciledAt = now.DeepCopy()
	metaSetBindingReadyCondition(&binding.Status.Conditions, binding.Generation, metav1.ConditionFalse, code, message)
	metaSetBindingProgressingCondition(&binding.Status.Conditions, binding.Generation, metav1.ConditionFalse, code, message)
}

func metaSetBindingReadyCondition(
	conditions *[]metav1.Condition,
	generation int64,
	status metav1.ConditionStatus,
	reason string,
	message string,
) {
	metaSetBindingCondition(conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		ObservedGeneration: generation,
		Reason:             strings.TrimSpace(reason),
		Message:            strings.TrimSpace(message),
		LastTransitionTime: metav1.Now(),
	})
}

func metaSetBindingProgressingCondition(
	conditions *[]metav1.Condition,
	generation int64,
	status metav1.ConditionStatus,
	reason string,
	message string,
) {
	metaSetBindingCondition(conditions, metav1.Condition{
		Type:               "Progressing",
		Status:             status,
		ObservedGeneration: generation,
		Reason:             strings.TrimSpace(reason),
		Message:            strings.TrimSpace(message),
		LastTransitionTime: metav1.Now(),
	})
}

func metaSetBindingCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	for index := range *conditions {
		if (*conditions)[index].Type == condition.Type {
			(*conditions)[index] = condition
			return
		}
	}
	*conditions = append(*conditions, condition)
}

func bindingRuntimeName(binding *spritzv1.SpritzBinding, sequence int64) string {
	prefix := bindingRuntimePrefix(binding)
	base := fmt.Sprintf("%s-%02d", prefix, sequence)
	if len(base) <= 63 {
		return base
	}
	return base[:63]
}

func bindingRuntimePrefix(binding *spritzv1.SpritzBinding) string {
	prefix := sanitizeBindingNameToken(binding.Spec.Template.NamePrefix)
	if prefix == "" {
		prefix = sanitizeBindingNameToken(binding.Spec.Template.PresetID)
	}
	if prefix == "" {
		prefix = "spritz"
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(binding.Spec.BindingKey)))
	base := fmt.Sprintf("%s-%x", prefix, sum[:6])
	if len(base) <= 56 {
		return base
	}
	return base[:56]
}

func sanitizeBindingNameToken(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return ""
	}
	var out strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		default:
			if out.Len() == 0 || lastDash {
				continue
			}
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func applyBindingIngressDefaults(spec *spritzv1.SpritzSpec, name, namespace string, defaults bindingIngressDefaults) {
	if spec == nil || !defaults.enabled() {
		return
	}
	if spec.Ingress == nil && bindingIsWebDisabled(spec) {
		return
	}
	if spec.Ingress == nil {
		spec.Ingress = &spritzv1.SpritzIngress{}
	}
	if spec.Ingress.Mode == "" && defaults.Mode != "" {
		spec.Ingress.Mode = defaults.Mode
	}
	if spec.Ingress.Host == "" && defaults.HostTemplate != "" {
		spec.Ingress.Host = strings.NewReplacer("{name}", name, "{namespace}", namespace).Replace(defaults.HostTemplate)
	}
	if spec.Ingress.Path == "" && defaults.Path != "" {
		spec.Ingress.Path = strings.NewReplacer("{name}", name, "{namespace}", namespace).Replace(defaults.Path)
	}
	if spec.Ingress.ClassName == "" && defaults.ClassName != "" {
		spec.Ingress.ClassName = defaults.ClassName
	}
	if spec.Ingress.GatewayName == "" && defaults.GatewayName != "" {
		spec.Ingress.GatewayName = defaults.GatewayName
	}
	if spec.Ingress.GatewayNamespace == "" && defaults.GatewayNamespace != "" {
		spec.Ingress.GatewayNamespace = defaults.GatewayNamespace
	}
	if spec.Ingress.GatewaySectionName == "" && defaults.GatewaySectionName != "" {
		spec.Ingress.GatewaySectionName = defaults.GatewaySectionName
	}
}

func bindingIsWebDisabled(spec *spritzv1.SpritzSpec) bool {
	if spec == nil || spec.Features == nil || spec.Features.Web == nil {
		return false
	}
	return !*spec.Features.Web
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func (r *SpritzBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spritzv1.SpritzBinding{}).
		Owns(&spritzv1.Spritz{}).
		Complete(r)
}
