package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"spritz.sh/operator/sharedmounts"
)

const (
	// DefaultACPPort is the reserved internal ACP service/container port for Spritz instances.
	DefaultACPPort = int32(2529)
	// DefaultACPPath is the default WebSocket path for the Spritz ACP transport.
	DefaultACPPath = "/"
)

//go:generate ../../hack/generate-crd.sh

// SpritzSpec defines the desired state of Spritz.
// +kubebuilder:validation:XValidation:rule="!(has(self.repo) && has(self.repos) && size(self.repos) > 0)",message="spec.repo and spec.repos are mutually exclusive"
type SpritzSpec struct {
	// +kubebuilder:validation:Pattern="^[a-z0-9]+((\\.|_|__|-+)[a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+((\\.|_|__|-+)[a-z0-9]+)*)*(@sha256:[a-f0-9]{64}|:[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127})?$"
	Image string `json:"image"`
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	ServiceAccountName string               `json:"serviceAccountName,omitempty"`
	RuntimePolicy      *SpritzRuntimePolicy `json:"runtimePolicy,omitempty"`
	Repo               *SpritzRepo          `json:"repo,omitempty"`
	Repos              []SpritzRepo         `json:"repos,omitempty"`
	Env                []corev1.EnvVar      `json:"env,omitempty"`
	// SharedMounts configures per-spritz shared directories.
	SharedMounts []sharedmounts.MountSpec `json:"sharedMounts,omitempty"`
	// +kubebuilder:validation:Pattern="^([0-9]+h)?([0-9]+m)?([0-9]+s)?$"
	TTL string `json:"ttl,omitempty"`
	// +kubebuilder:validation:Pattern="^([0-9]+h)?([0-9]+m)?([0-9]+s)?$"
	IdleTTL   string                      `json:"idleTtl,omitempty"`
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	Owner     SpritzOwner                 `json:"owner"`
	AgentRef  *SpritzAgentRef             `json:"agentRef,omitempty"`
	// ProfileOverrides stores optional local overrides for UI-facing agent profile fields.
	ProfileOverrides *SpritzAgentProfile `json:"profileOverrides,omitempty"`
	Labels           map[string]string   `json:"labels,omitempty"`
	Annotations      map[string]string   `json:"annotations,omitempty"`
	Features         *SpritzFeatures     `json:"features,omitempty"`
	SSH              *SpritzSSH          `json:"ssh,omitempty"`
	Ports            []SpritzPort        `json:"ports,omitempty"`
	Ingress          *SpritzIngress      `json:"ingress,omitempty"`
}

// SpritzRuntimePolicy stores deployment-resolved infrastructure policy profile references.
// +kubebuilder:validation:XValidation:rule="!has(self.networkProfile) && !has(self.mountProfile) && !has(self.exposureProfile) && !has(self.revision) || has(self.networkProfile) && has(self.mountProfile) && has(self.exposureProfile) && has(self.revision)",message="runtimePolicy requires networkProfile, mountProfile, exposureProfile, and revision together"
type SpritzRuntimePolicy struct {
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	NetworkProfile string `json:"networkProfile,omitempty"`
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	MountProfile string `json:"mountProfile,omitempty"`
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	ExposureProfile string `json:"exposureProfile,omitempty"`
	// +kubebuilder:validation:Pattern="^sha256:[a-f0-9]{64}$"
	Revision string `json:"revision,omitempty"`
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
	ID   string `json:"id"`
	Team string `json:"team,omitempty"`
}

// SpritzAgentRef identifies a deployment-owned external agent record.
type SpritzAgentRef struct {
	// +kubebuilder:validation:MaxLength=64
	Type string `json:"type,omitempty"`
	// +kubebuilder:validation:MaxLength=128
	Provider string `json:"provider,omitempty"`
	// +kubebuilder:validation:MaxLength=256
	ID string `json:"id,omitempty"`
}

// SpritzAgentProfile stores UI-facing agent profile fields.
type SpritzAgentProfile struct {
	// +kubebuilder:validation:MaxLength=128
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:MaxLength=2048
	ImageURL string `json:"imageUrl,omitempty"`
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
	URL             string                    `json:"url,omitempty"`
	Profile         *SpritzAgentProfileStatus `json:"profile,omitempty"`
	ACP             *SpritzACPStatus          `json:"acp,omitempty"`
	SSH             *SpritzSSHInfo            `json:"ssh,omitempty"`
	Message         string                    `json:"message,omitempty"`
	LastActivityAt  *metav1.Time              `json:"lastActivityAt,omitempty"`
	IdleExpiresAt   *metav1.Time              `json:"idleExpiresAt,omitempty"`
	MaxExpiresAt    *metav1.Time              `json:"maxExpiresAt,omitempty"`
	ExpiresAt       *metav1.Time              `json:"expiresAt,omitempty"`
	LifecycleReason string                    `json:"lifecycleReason,omitempty"`
	ReadyAt         *metav1.Time              `json:"readyAt,omitempty"`
	Conditions      []metav1.Condition        `json:"conditions,omitempty"`
}

// SpritzAgentProfileStatus stores the synced UI-facing profile for an instance.
type SpritzAgentProfileStatus struct {
	// +kubebuilder:validation:MaxLength=128
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:MaxLength=2048
	ImageURL string `json:"imageUrl,omitempty"`
	// +kubebuilder:validation:MaxLength=32
	Source string `json:"source,omitempty"`
	// +kubebuilder:validation:MaxLength=128
	Syncer             string       `json:"syncer,omitempty"`
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	LastSyncedAt       *metav1.Time `json:"lastSyncedAt,omitempty"`
	LastError          string       `json:"lastError,omitempty"`
}

// SpritzACPStatus describes ACP discovery state for the workload.
type SpritzACPStatus struct {
	// +kubebuilder:validation:Enum=unknown;probing;ready;unavailable;error
	State string `json:"state,omitempty"`
	// +kubebuilder:validation:Minimum=1
	ProtocolVersion int32                  `json:"protocolVersion,omitempty"`
	Endpoint        *SpritzACPEndpoint     `json:"endpoint,omitempty"`
	AgentInfo       *SpritzACPAgentInfo    `json:"agentInfo,omitempty"`
	Capabilities    *SpritzACPCapabilities `json:"capabilities,omitempty"`
	AuthMethods     []string               `json:"authMethods,omitempty"`
	LastProbeAt     *metav1.Time           `json:"lastProbeAt,omitempty"`
	LastMetadataAt  *metav1.Time           `json:"lastMetadataAt,omitempty"`
	LastError       string                 `json:"lastError,omitempty"`
}

// SpritzACPEndpoint identifies the reserved ACP endpoint for a spritz.
type SpritzACPEndpoint struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32  `json:"port,omitempty"`
	Path string `json:"path,omitempty"`
}

// SpritzACPAgentInfo is the normalized ACP agent identity.
type SpritzACPAgentInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// SpritzACPCapabilities stores the ACP capability subset Spritz uses.
type SpritzACPCapabilities struct {
	LoadSession bool                               `json:"loadSession,omitempty"`
	Prompt      *SpritzACPPromptCapabilities       `json:"prompt,omitempty"`
	MCP         *SpritzACPMCPTransportCapabilities `json:"mcp,omitempty"`
}

// SpritzACPPromptCapabilities stores ACP prompt content support.
type SpritzACPPromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// SpritzACPMCPTransportCapabilities stores ACP MCP transport support.
type SpritzACPMCPTransportCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// SpritzSSHInfo describes SSH access to the workload.
type SpritzSSHInfo struct {
	Host string `json:"host,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32  `json:"port,omitempty"`
	User string `json:"user,omitempty"`
}

// SpritzConversationSpec stores ACP conversation metadata for a spritz.
type SpritzConversationSpec struct {
	SpritzName   string                 `json:"spritzName"`
	Owner        SpritzOwner            `json:"owner"`
	Title        string                 `json:"title,omitempty"`
	SessionID    string                 `json:"sessionId,omitempty"`
	CWD          string                 `json:"cwd,omitempty"`
	AgentInfo    *SpritzACPAgentInfo    `json:"agentInfo,omitempty"`
	Capabilities *SpritzACPCapabilities `json:"capabilities,omitempty"`
}

// SpritzConversationStatus stores observed ACP binding state for a conversation.
type SpritzConversationStatus struct {
	// +kubebuilder:validation:Enum=pending;active;missing;replaced;error
	BindingState           string       `json:"bindingState,omitempty"`
	BoundSessionID         string       `json:"boundSessionId,omitempty"`
	EffectiveCWD           string       `json:"effectiveCwd,omitempty"`
	PreviousSessionID      string       `json:"previousSessionId,omitempty"`
	LastBoundAt            *metav1.Time `json:"lastBoundAt,omitempty"`
	LastReplayAt           *metav1.Time `json:"lastReplayAt,omitempty"`
	LastReplayMessageCount int32        `json:"lastReplayMessageCount,omitempty"`
	LastError              string       `json:"lastError,omitempty"`
	UpdatedAt              *metav1.Time `json:"updatedAt,omitempty"`
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

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=spritzchat
// +kubebuilder:printcolumn:name="Spritz",type=string,JSONPath=".spec.spritzName"
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=".spec.owner.id"
// +kubebuilder:printcolumn:name="Session",type=string,JSONPath=".spec.sessionId"
// +kubebuilder:printcolumn:name="Binding",type=string,JSONPath=".status.bindingState"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// SpritzConversation stores ACP conversation metadata for a spritz instance.
// +kubebuilder:subresource:status
type SpritzConversation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpritzConversationSpec   `json:"spec"`
	Status SpritzConversationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// SpritzConversationList contains a list of SpritzConversation objects.
type SpritzConversationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SpritzConversation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Spritz{}, &SpritzList{}, &SpritzConversation{}, &SpritzConversationList{})
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

func (in *Spritz) DeepCopy() *Spritz {
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

func (in *SpritzList) DeepCopy() *SpritzList {
	if in == nil {
		return nil
	}
	out := new(SpritzList)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzConversation) DeepCopyInto(out *SpritzConversation) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *SpritzConversation) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(SpritzConversation)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzConversation) DeepCopy() *SpritzConversation {
	if in == nil {
		return nil
	}
	out := new(SpritzConversation)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzConversationList) DeepCopyInto(out *SpritzConversationList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]SpritzConversation, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *SpritzConversationList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(SpritzConversationList)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzConversationList) DeepCopy() *SpritzConversationList {
	if in == nil {
		return nil
	}
	out := new(SpritzConversationList)
	in.DeepCopyInto(out)
	return out
}

func (in *SpritzSpec) DeepCopyInto(out *SpritzSpec) {
	*out = *in
	if in.RuntimePolicy != nil {
		out.RuntimePolicy = &SpritzRuntimePolicy{}
		*out.RuntimePolicy = *in.RuntimePolicy
	}
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
	if in.SharedMounts != nil {
		out.SharedMounts = make([]sharedmounts.MountSpec, len(in.SharedMounts))
		copy(out.SharedMounts, in.SharedMounts)
	}
	in.Resources.DeepCopyInto(&out.Resources)
	if in.AgentRef != nil {
		out.AgentRef = &SpritzAgentRef{}
		*out.AgentRef = *in.AgentRef
	}
	if in.ProfileOverrides != nil {
		out.ProfileOverrides = &SpritzAgentProfile{}
		*out.ProfileOverrides = *in.ProfileOverrides
	}
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
	if in.Profile != nil {
		out.Profile = &SpritzAgentProfileStatus{}
		*out.Profile = *in.Profile
		if in.Profile.LastSyncedAt != nil {
			out.Profile.LastSyncedAt = in.Profile.LastSyncedAt.DeepCopy()
		}
	}
	if in.ACP != nil {
		out.ACP = &SpritzACPStatus{}
		in.ACP.DeepCopyInto(out.ACP)
	}
	if in.SSH != nil {
		out.SSH = &SpritzSSHInfo{}
		*out.SSH = *in.SSH
	}
	if in.LastActivityAt != nil {
		out.LastActivityAt = in.LastActivityAt.DeepCopy()
	}
	if in.IdleExpiresAt != nil {
		out.IdleExpiresAt = in.IdleExpiresAt.DeepCopy()
	}
	if in.MaxExpiresAt != nil {
		out.MaxExpiresAt = in.MaxExpiresAt.DeepCopy()
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

func (in *SpritzConversationSpec) DeepCopyInto(out *SpritzConversationSpec) {
	*out = *in
	if in.AgentInfo != nil {
		out.AgentInfo = &SpritzACPAgentInfo{}
		*out.AgentInfo = *in.AgentInfo
	}
	if in.Capabilities != nil {
		out.Capabilities = &SpritzACPCapabilities{}
		in.Capabilities.DeepCopyInto(out.Capabilities)
	}
}

func (in *SpritzConversationStatus) DeepCopyInto(out *SpritzConversationStatus) {
	*out = *in
	if in.LastBoundAt != nil {
		out.LastBoundAt = in.LastBoundAt.DeepCopy()
	}
	if in.LastReplayAt != nil {
		out.LastReplayAt = in.LastReplayAt.DeepCopy()
	}
	if in.UpdatedAt != nil {
		out.UpdatedAt = in.UpdatedAt.DeepCopy()
	}
}

func (in *SpritzACPStatus) DeepCopyInto(out *SpritzACPStatus) {
	*out = *in
	if in.Endpoint != nil {
		out.Endpoint = &SpritzACPEndpoint{}
		*out.Endpoint = *in.Endpoint
	}
	if in.AgentInfo != nil {
		out.AgentInfo = &SpritzACPAgentInfo{}
		*out.AgentInfo = *in.AgentInfo
	}
	if in.Capabilities != nil {
		out.Capabilities = &SpritzACPCapabilities{}
		in.Capabilities.DeepCopyInto(out.Capabilities)
	}
	if in.AuthMethods != nil {
		out.AuthMethods = make([]string, len(in.AuthMethods))
		copy(out.AuthMethods, in.AuthMethods)
	}
	if in.LastProbeAt != nil {
		out.LastProbeAt = in.LastProbeAt.DeepCopy()
	}
	if in.LastMetadataAt != nil {
		out.LastMetadataAt = in.LastMetadataAt.DeepCopy()
	}
}

func (in *SpritzACPCapabilities) DeepCopyInto(out *SpritzACPCapabilities) {
	*out = *in
	if in.Prompt != nil {
		out.Prompt = &SpritzACPPromptCapabilities{}
		*out.Prompt = *in.Prompt
	}
	if in.MCP != nil {
		out.MCP = &SpritzACPMCPTransportCapabilities{}
		*out.MCP = *in.MCP
	}
}
