package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"spritz.sh/operator/sharedmounts"
)

//go:generate ../../hack/generate-crd.sh

// SpritzSpec defines the desired state of Spritz.
// +kubebuilder:validation:XValidation:rule="!(has(self.repo) && has(self.repos) && size(self.repos) > 0)",message="spec.repo and spec.repos are mutually exclusive"
type SpritzSpec struct {
	// +kubebuilder:validation:Pattern="^[a-z0-9]+((\\.|_|__|-+)[a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+((\\.|_|__|-+)[a-z0-9]+)*)*(@sha256:[a-f0-9]{64}|:[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127})?$"
	Image string          `json:"image"`
	Repo  *SpritzRepo     `json:"repo,omitempty"`
	Repos []SpritzRepo    `json:"repos,omitempty"`
	Env   []corev1.EnvVar `json:"env,omitempty"`
	// SharedMounts configures per-spritz shared directories.
	SharedMounts []sharedmounts.MountSpec `json:"sharedMounts,omitempty"`
	// +kubebuilder:validation:Pattern="^([0-9]+h)?([0-9]+m)?([0-9]+s)?$"
	TTL         string                      `json:"ttl,omitempty"`
	Resources   corev1.ResourceRequirements `json:"resources,omitempty"`
	Owner       SpritzOwner                 `json:"owner"`
	Labels      map[string]string           `json:"labels,omitempty"`
	Annotations map[string]string           `json:"annotations,omitempty"`
	Features    *SpritzFeatures             `json:"features,omitempty"`
	SSH         *SpritzSSH                  `json:"ssh,omitempty"`
	Ports       []SpritzPort                `json:"ports,omitempty"`
	Ingress     *SpritzIngress              `json:"ingress,omitempty"`
}

// SpritzRepo describes the repository to clone inside the workload.
type SpritzRepo struct {
	// +kubebuilder:validation:Format=uri
	URL      string `json:"url"`
	Dir      string `json:"dir,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Revision string `json:"revision,omitempty"`
	// +kubebuilder:validation:Minimum=1
	Depth      int             `json:"depth,omitempty"`
	Submodules bool            `json:"submodules,omitempty"`
	Auth       *SpritzRepoAuth `json:"auth,omitempty"`
}

// SpritzRepoAuth describes how to authenticate git clone operations.
type SpritzRepoAuth struct {
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`
	// NetrcKey points to a Secret key containing a full .netrc file.
	NetrcKey string `json:"netrcKey,omitempty"`
	// UsernameKey points to a Secret key containing the username to use.
	UsernameKey string `json:"usernameKey,omitempty"`
	// PasswordKey points to a Secret key containing the password/token to use.
	PasswordKey string `json:"passwordKey,omitempty"`
}

// SpritzOwner identifies the creator of a spritz.
type SpritzOwner struct {
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
	// +kubebuilder:validation:Format=email
	Email string `json:"email,omitempty"`
	Team  string `json:"team,omitempty"`
}

// SpritzFeatures toggles optional capabilities.
type SpritzFeatures struct {
	// +kubebuilder:default=false
	SSH *bool `json:"ssh,omitempty"`
	// +kubebuilder:default=true
	Web *bool `json:"web,omitempty"`
}

// SpritzSSH configures SSH access behavior.
type SpritzSSH struct {
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Enum=service;gateway
	Mode string `json:"mode,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ServicePort      int32  `json:"servicePort,omitempty"`
	GatewayService   string `json:"gatewayService,omitempty"`
	GatewayNamespace string `json:"gatewayNamespace,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	GatewayPort int32  `json:"gatewayPort,omitempty"`
	User        string `json:"user,omitempty"`
}

// SpritzPort exposes a container port via a Service.
type SpritzPort struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ServicePort int32 `json:"servicePort,omitempty"`
	// +kubebuilder:validation:Enum=TCP;UDP;SCTP
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

// SpritzIngress configures optional HTTP routing.
type SpritzIngress struct {
	// +kubebuilder:validation:Enum=ingress;gateway
	Mode string `json:"mode,omitempty"`
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
	// ClassName is only used when Mode=ingress.
	ClassName string `json:"className,omitempty"`
	// GatewayName is required when Mode=gateway.
	GatewayName string `json:"gatewayName,omitempty"`
	// GatewayNamespace defaults to the spritz namespace when empty.
	GatewayNamespace string `json:"gatewayNamespace,omitempty"`
	// GatewaySectionName can be used to target a specific Gateway listener.
	GatewaySectionName string            `json:"gatewaySectionName,omitempty"`
	Annotations        map[string]string `json:"annotations,omitempty"`
}

// SpritzStatus defines the observed state of Spritz.
type SpritzStatus struct {
	// +kubebuilder:validation:Enum=Provisioning;Ready;Expiring;Expired;Terminating;Error
	Phase string `json:"phase,omitempty"`
	// +kubebuilder:validation:Format=uri
	URL            string             `json:"url,omitempty"`
	SSH            *SpritzSSHInfo     `json:"ssh,omitempty"`
	Message        string             `json:"message,omitempty"`
	LastActivityAt *metav1.Time       `json:"lastActivityAt,omitempty"`
	ExpiresAt      *metav1.Time       `json:"expiresAt,omitempty"`
	ReadyAt        *metav1.Time       `json:"readyAt,omitempty"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

// SpritzSSHInfo describes SSH access to the workload.
type SpritzSSHInfo struct {
	Host string `json:"host,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32  `json:"port,omitempty"`
	User string `json:"user,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=spr
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=".spec.owner.id"
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=".spec.repo.url"
// +kubebuilder:printcolumn:name="Url",type=string,JSONPath=".status.url"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// Spritz is the Schema for the spritzes API.
type Spritz struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpritzSpec   `json:"spec"`
	Status SpritzStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// SpritzList contains a list of Spritz.
type SpritzList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Spritz `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Spritz{}, &SpritzList{})
}

func (in *Spritz) DeepCopyInto(out *Spritz) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *Spritz) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(Spritz)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzList) DeepCopyInto(out *SpritzList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Spritz, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *SpritzList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(SpritzList)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzSpec) DeepCopyInto(out *SpritzSpec) {
	*out = *in
	if in.Repo != nil {
		out.Repo = &SpritzRepo{}
		*out.Repo = *in.Repo
		if in.Repo.Auth != nil {
			out.Repo.Auth = &SpritzRepoAuth{}
			*out.Repo.Auth = *in.Repo.Auth
		}
	}
	if in.Repos != nil {
		out.Repos = make([]SpritzRepo, len(in.Repos))
		for i := range in.Repos {
			repo := in.Repos[i]
			out.Repos[i] = SpritzRepo{
				URL:        repo.URL,
				Dir:        repo.Dir,
				Branch:     repo.Branch,
				Revision:   repo.Revision,
				Depth:      repo.Depth,
				Submodules: repo.Submodules,
			}
			if repo.Auth != nil {
				out.Repos[i].Auth = &SpritzRepoAuth{}
				*out.Repos[i].Auth = *repo.Auth
			}
		}
	}
	if in.Env != nil {
		out.Env = make([]corev1.EnvVar, len(in.Env))
		for i := range in.Env {
			in.Env[i].DeepCopyInto(&out.Env[i])
		}
	}
	in.Resources.DeepCopyInto(&out.Resources)
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
	if in.Features != nil {
		out.Features = &SpritzFeatures{}
		if in.Features.SSH != nil {
			ssh := *in.Features.SSH
			out.Features.SSH = &ssh
		}
		if in.Features.Web != nil {
			web := *in.Features.Web
			out.Features.Web = &web
		}
	}
	if in.SSH != nil {
		out.SSH = &SpritzSSH{}
		*out.SSH = *in.SSH
	}
	if in.Ports != nil {
		out.Ports = make([]SpritzPort, len(in.Ports))
		copy(out.Ports, in.Ports)
	}
	if in.Ingress != nil {
		out.Ingress = &SpritzIngress{}
		out.Ingress.Mode = in.Ingress.Mode
		out.Ingress.Host = in.Ingress.Host
		out.Ingress.Path = in.Ingress.Path
		out.Ingress.ClassName = in.Ingress.ClassName
		out.Ingress.GatewayName = in.Ingress.GatewayName
		out.Ingress.GatewayNamespace = in.Ingress.GatewayNamespace
		out.Ingress.GatewaySectionName = in.Ingress.GatewaySectionName
		if in.Ingress.Annotations != nil {
			out.Ingress.Annotations = make(map[string]string, len(in.Ingress.Annotations))
			for k, v := range in.Ingress.Annotations {
				out.Ingress.Annotations[k] = v
			}
		}
	}
}

func (in *SpritzStatus) DeepCopyInto(out *SpritzStatus) {
	*out = *in
	if in.SSH != nil {
		out.SSH = &SpritzSSHInfo{}
		*out.SSH = *in.SSH
	}
	if in.LastActivityAt != nil {
		out.LastActivityAt = in.LastActivityAt.DeepCopy()
	}
	if in.ExpiresAt != nil {
		out.ExpiresAt = in.ExpiresAt.DeepCopy()
	}
	if in.ReadyAt != nil {
		out.ReadyAt = in.ReadyAt.DeepCopy()
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}
