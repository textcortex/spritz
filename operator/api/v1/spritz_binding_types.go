package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// BindingKeyAnnotationKey stores the canonical external binding key on a runtime.
	BindingKeyAnnotationKey = "spritz.sh/binding-key"
	// BindingNameLabelKey links a runtime back to its owning SpritzBinding resource.
	BindingNameLabelKey = "spritz.sh/binding-name"
	// BindingInstanceRoleAnnotationKey marks whether a runtime is active or candidate.
	BindingInstanceRoleAnnotationKey = "spritz.sh/binding-instance-role"
	// BindingReconcileRequestedAtAnnotationKey nudges the binding controller to reconcile now.
	BindingReconcileRequestedAtAnnotationKey = "spritz.sh/binding-reconcile-requested-at"
)

const (
	BindingPhasePending      = "pending"
	BindingPhaseCreating     = "creating"
	BindingPhaseWaitingReady = "waiting_ready"
	BindingPhaseCuttingOver  = "cutting_over"
	BindingPhaseCleaningUp   = "cleaning_up"
	BindingPhaseReady        = "ready"
	BindingPhaseFailed       = "failed"
)

type SpritzBindingTemplate struct {
	PresetID    string            `json:"presetId,omitempty"`
	NamePrefix  string            `json:"namePrefix,omitempty"`
	Source      string            `json:"source,omitempty"`
	RequestID   string            `json:"requestId,omitempty"`
	Spec        SpritzSpec        `json:"spec"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type SpritzBindingInstanceRef struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Phase     string `json:"phase,omitempty"`
}

type SpritzBindingSpec struct {
	BindingKey        string                    `json:"bindingKey"`
	DesiredRevision   string                    `json:"desiredRevision,omitempty"`
	Disconnected      bool                      `json:"disconnected,omitempty"`
	Attributes        map[string]string         `json:"attributes,omitempty"`
	Template          SpritzBindingTemplate     `json:"template"`
	AdoptActive       *SpritzBindingInstanceRef `json:"adoptActive,omitempty"`
	AdoptedRevision   string                    `json:"adoptedRevision,omitempty"`
	ObservedRequestID string                    `json:"observedRequestId,omitempty"`
}

type SpritzBindingStatus struct {
	Phase                string                    `json:"phase,omitempty"`
	ObservedRevision     string                    `json:"observedRevision,omitempty"`
	ActiveInstanceRef    *SpritzBindingInstanceRef `json:"activeInstanceRef,omitempty"`
	CandidateInstanceRef *SpritzBindingInstanceRef `json:"candidateInstanceRef,omitempty"`
	CleanupInstanceRef   *SpritzBindingInstanceRef `json:"cleanupInstanceRef,omitempty"`
	LastErrorCode        string                    `json:"lastErrorCode,omitempty"`
	LastErrorMessage     string                    `json:"lastErrorMessage,omitempty"`
	ObservedGeneration   int64                     `json:"observedGeneration,omitempty"`
	NextRuntimeSequence  int64                     `json:"nextRuntimeSequence,omitempty"`
	LastReconciledAt     *metav1.Time              `json:"lastReconciledAt,omitempty"`
	Conditions           []metav1.Condition        `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=sprbind
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=".status.observedRevision"
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=".status.activeInstanceRef.name"
// +kubebuilder:printcolumn:name="Candidate",type=string,JSONPath=".status.candidateInstanceRef.name"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// SpritzBinding is the durable logical binding that owns disposable Spritz runtimes.
type SpritzBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpritzBindingSpec   `json:"spec"`
	Status SpritzBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// SpritzBindingList contains a list of SpritzBinding objects.
type SpritzBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SpritzBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SpritzBinding{}, &SpritzBindingList{})
}

func (in *SpritzBindingTemplate) DeepCopyInto(out *SpritzBindingTemplate) {
	*out = *in
	in.Spec.DeepCopyInto(&out.Spec)
	if in.Labels != nil {
		out.Labels = make(map[string]string, len(in.Labels))
		for k, v := range in.Labels {
			out.Labels[k] = v
		}
	}
	if in.Annotations != nil {
		out.Annotations = make(map[string]string, len(in.Annotations))
		for k, v := range in.Annotations {
			out.Annotations[k] = v
		}
	}
}

func (in *SpritzBindingInstanceRef) DeepCopyInto(out *SpritzBindingInstanceRef) {
	*out = *in
}

func (in *SpritzBindingInstanceRef) DeepCopy() *SpritzBindingInstanceRef {
	if in == nil {
		return nil
	}
	out := new(SpritzBindingInstanceRef)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzBindingSpec) DeepCopyInto(out *SpritzBindingSpec) {
	*out = *in
	if in.Attributes != nil {
		out.Attributes = make(map[string]string, len(in.Attributes))
		for k, v := range in.Attributes {
			out.Attributes[k] = v
		}
	}
	in.Template.DeepCopyInto(&out.Template)
	if in.AdoptActive != nil {
		out.AdoptActive = in.AdoptActive.DeepCopy()
	}
}

func (in *SpritzBindingStatus) DeepCopyInto(out *SpritzBindingStatus) {
	*out = *in
	if in.ActiveInstanceRef != nil {
		out.ActiveInstanceRef = in.ActiveInstanceRef.DeepCopy()
	}
	if in.CandidateInstanceRef != nil {
		out.CandidateInstanceRef = in.CandidateInstanceRef.DeepCopy()
	}
	if in.CleanupInstanceRef != nil {
		out.CleanupInstanceRef = in.CleanupInstanceRef.DeepCopy()
	}
	if in.LastReconciledAt != nil {
		timestamp := in.LastReconciledAt.DeepCopy()
		out.LastReconciledAt = timestamp
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

func (in *SpritzBinding) DeepCopyInto(out *SpritzBinding) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *SpritzBinding) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(SpritzBinding)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzBinding) DeepCopy() *SpritzBinding {
	if in == nil {
		return nil
	}
	out := new(SpritzBinding)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzBindingList) DeepCopyInto(out *SpritzBindingList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]SpritzBinding, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *SpritzBindingList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(SpritzBindingList)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzBindingList) DeepCopy() *SpritzBindingList {
	if in == nil {
		return nil
	}
	out := new(SpritzBindingList)
	in.DeepCopyInto(out)
	return out
}
