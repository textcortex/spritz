package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	scopeInstancesCreate            = "spritz.instances.create"
	scopeInstancesAssignOwner       = "spritz.instances.assign_owner"
	scopePresetsRead                = "spritz.presets.read"
	scopeInstancesSuggestName       = "spritz.instances.suggest_name"
	scopeExternalResolveViaCreate   = "spritz.external_identities.resolve_via_create"
	scopeChannelRouteResolve        = "spritz.channel.route.resolve"
	scopeChannelConversationsUpsert = "spritz.channel.conversations.upsert"

	actorIDAnnotationKey          = "spritz.sh/actor.id"
	actorTypeAnnotationKey        = "spritz.sh/actor.type"
	sourceAnnotationKey           = "spritz.sh/source"
	requestIDAnnotationKey        = "spritz.sh/request-id"
	idempotencyKeyAnnotationKey   = "spritz.sh/idempotency-key"
	idempotencyHashAnnotationKey  = "spritz.sh/idempotency-hash"
	presetIDAnnotationKey         = "spritz.sh/preset-id"
	actorLabelKey                 = "spritz.sh/actor"
	idempotencyLabelKey           = "spritz.sh/idempotency"
	presetLabelKey                = "spritz.sh/preset"
	idempotencyReservationPrefix  = "spritz-idempotency-"
	idempotencyReservationHashKey = "fingerprint"
	idempotencyReservationNameKey = "spritzName"
	idempotencyReservationDoneKey = "completed"
	idempotencyReservationBodyKey = "payload"
	defaultProvisionerSource      = "external"
	defaultProvisionerIdleTTL     = 24 * time.Hour
	defaultProvisionerMaxTTL      = 7 * 24 * time.Hour
)

var (
	errIdempotencyUsed                = errors.New("idempotencyKey already used")
	errIdempotencyUsedDifferent       = errors.New("idempotencyKey already used with a different request")
	errIdempotencyIncompatiblePending = errors.New("idempotencyKey already used by an incompatible pending request")
)

type runtimePreset struct {
	ID            string          `json:"id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Description   string          `json:"description,omitempty"`
	Image         string          `json:"image,omitempty"`
	RepoURL       string          `json:"repoUrl,omitempty"`
	Branch        string          `json:"branch,omitempty"`
	TTL           string          `json:"ttl,omitempty"`
	IdleTTL       string          `json:"idleTtl,omitempty"`
	NamePrefix    string          `json:"namePrefix,omitempty"`
	InstanceClass string          `json:"instanceClass,omitempty"`
	Hidden        bool            `json:"hidden,omitempty"`
	Env           []corev1.EnvVar `json:"env,omitempty"`
}

type publicPreset struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Image         string `json:"image,omitempty"`
	RepoURL       string `json:"repoUrl,omitempty"`
	Branch        string `json:"branch,omitempty"`
	TTL           string `json:"ttl,omitempty"`
	IdleTTL       string `json:"idleTtl,omitempty"`
	NamePrefix    string `json:"namePrefix,omitempty"`
	InstanceClass string `json:"instanceClass,omitempty"`
	Hidden        bool   `json:"hidden,omitempty"`
}

type presetCatalog struct {
	byID []runtimePreset
}

type provisionerPolicy struct {
	allowedPresetIDs       map[string]struct{}
	defaultPresetID        string
	allowCustomImage       bool
	allowCustomRepo        bool
	allowNamespaceOverride bool
	allowedNamespaces      map[string]struct{}
	defaultIdleTTL         time.Duration
	maxIdleTTL             time.Duration
	defaultTTL             time.Duration
	maxTTL                 time.Duration
	maxActivePerOwner      int
	maxCreatesPerActor     int
	maxCreatesPerOwner     int
	rateWindow             time.Duration
}

type createSpritzResponse struct {
	Spritz         *spritzv1.Spritz `json:"spritz"`
	AccessURL      string           `json:"accessUrl,omitempty"`
	ChatURL        string           `json:"chatUrl,omitempty"`
	InstanceURL    string           `json:"instanceUrl,omitempty"`
	Namespace      string           `json:"namespace,omitempty"`
	OwnerID        string           `json:"ownerId,omitempty"`
	ActorID        string           `json:"actorId,omitempty"`
	ActorType      string           `json:"actorType,omitempty"`
	PresetID       string           `json:"presetId,omitempty"`
	Source         string           `json:"source,omitempty"`
	IdempotencyKey string           `json:"idempotencyKey,omitempty"`
	Replayed       bool             `json:"replayed,omitempty"`
	CreatedAt      *metav1.Time     `json:"createdAt,omitempty"`
	IdleTTL        string           `json:"idleTtl,omitempty"`
	TTL            string           `json:"ttl,omitempty"`
	IdleExpiresAt  *metav1.Time     `json:"idleExpiresAt,omitempty"`
	MaxExpiresAt   *metav1.Time     `json:"maxExpiresAt,omitempty"`
	ExpiresAt      *metav1.Time     `json:"expiresAt,omitempty"`
}

type suggestNameMetadata struct {
	presetID   string
	namePrefix string
	image      string
}

type idempotentCreatePayload struct {
	PresetID      string                          `json:"presetId,omitempty"`
	NamePrefix    string                          `json:"namePrefix,omitempty"`
	Source        string                          `json:"source,omitempty"`
	RequestID     string                          `json:"requestId,omitempty"`
	Spec          spritzv1.SpritzSpec             `json:"spec"`
	Labels        map[string]string               `json:"labels,omitempty"`
	Annotations   map[string]string               `json:"annotations,omitempty"`
	ExternalOwner *idempotentExternalOwnerPayload `json:"externalOwner,omitempty"`
}

type provisionerIdempotencyState struct {
	canonicalFingerprint string
	resolvedPayload      string
}

type idempotentExternalOwnerPayload struct {
	Issuer      string `json:"issuer,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Tenant      string `json:"tenant,omitempty"`
	SubjectHash string `json:"subjectHash,omitempty"`
	ResolvedAt  string `json:"resolvedAt,omitempty"`
}

func (s *server) applyCreatePreset(body *createRequest) (*runtimePreset, error) {
	body.PresetID = sanitizeSpritzNameToken(body.PresetID)
	if body.PresetID == "" {
		return nil, nil
	}
	preset, ok := s.presets.get(body.PresetID)
	if !ok {
		return nil, fmt.Errorf("preset not found: %s", body.PresetID)
	}
	buildPresetIntoSpec(&body.Spec, preset)
	if strings.TrimSpace(body.NamePrefix) == "" {
		body.NamePrefix = preset.NamePrefix
	}
	return preset, nil
}

func (s *server) applyProvisionerDefaultPreset(body *createRequest, principal principal) {
	if body == nil || !principal.isService() {
		return
	}
	if strings.TrimSpace(body.PresetID) != "" {
		return
	}
	if strings.TrimSpace(body.Spec.Image) != "" {
		return
	}
	if s.provisioners.defaultPresetID == "" {
		return
	}
	body.PresetID = s.provisioners.defaultPresetID
}

func (s *server) applyProvisionerDefaultSuggestNamePreset(body *suggestNameRequest, principal principal) {
	if body == nil || !principal.isService() {
		return
	}
	if strings.TrimSpace(body.PresetID) != "" {
		return
	}
	if strings.TrimSpace(body.Image) != "" {
		return
	}
	if s.provisioners.defaultPresetID == "" {
		return
	}
	body.PresetID = s.provisioners.defaultPresetID
}

func applyTopLevelCreateFields(body *createRequest) {
	if strings.TrimSpace(body.OwnerID) != "" && strings.TrimSpace(body.Spec.Owner.ID) == "" {
		body.Spec.Owner.ID = strings.TrimSpace(body.OwnerID)
	}
	if strings.TrimSpace(body.IdleTTL) != "" && strings.TrimSpace(body.Spec.IdleTTL) == "" {
		body.Spec.IdleTTL = strings.TrimSpace(body.IdleTTL)
	}
	if strings.TrimSpace(body.TTL) != "" && strings.TrimSpace(body.Spec.TTL) == "" {
		body.Spec.TTL = strings.TrimSpace(body.TTL)
	}
	body.Source = strings.TrimSpace(body.Source)
	body.RequestID = strings.TrimSpace(body.RequestID)
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
}

func normalizeCreateOwnerRequest(body *createRequest, principal principal, authEnabled bool) (spritzv1.SpritzOwner, error) {
	owner := body.Spec.Owner
	if body.OwnerRef != nil {
		ref := *body.OwnerRef
		ref.Type = strings.ToLower(strings.TrimSpace(ref.Type))
		ref.ID = strings.TrimSpace(ref.ID)
		switch ref.Type {
		case "":
			return owner, fmt.Errorf("ownerRef.type is required")
		case "owner":
			if ref.ID == "" {
				return owner, fmt.Errorf("ownerRef.id is required when ownerRef.type=owner")
			}
			if explicitOwner := strings.TrimSpace(body.OwnerID); explicitOwner != "" && explicitOwner != ref.ID {
				return owner, fmt.Errorf("ownerId conflicts with ownerRef.id")
			}
			if strings.TrimSpace(owner.ID) != "" && strings.TrimSpace(owner.ID) != ref.ID {
				return owner, fmt.Errorf("ownerRef.id conflicts with spec.owner.id")
			}
			body.OwnerID = ref.ID
			body.OwnerRef = &ownerRef{Type: "owner", ID: ref.ID}
			owner.ID = ref.ID
			body.Spec.Owner = owner
		case "external":
			normalized, err := normalizeExternalOwnerRef(ref)
			if err != nil {
				return owner, err
			}
			if strings.TrimSpace(body.OwnerID) != "" {
				return owner, fmt.Errorf("ownerId conflicts with ownerRef")
			}
			if strings.TrimSpace(owner.ID) != "" {
				return owner, fmt.Errorf("spec.owner.id conflicts with ownerRef")
			}
			body.OwnerRef = &normalized
			return owner, nil
		default:
			return owner, fmt.Errorf("ownerRef.type must be owner or external")
		}
	}
	if explicitOwner := strings.TrimSpace(body.OwnerID); explicitOwner != "" && strings.TrimSpace(owner.ID) != "" && explicitOwner != strings.TrimSpace(owner.ID) {
		return owner, fmt.Errorf("ownerId conflicts with spec.owner.id")
	}
	if principal.isService() && strings.TrimSpace(body.OwnerID) == "" && strings.TrimSpace(owner.ID) == "" {
		return owner, fmt.Errorf("ownerId is required")
	}
	if owner.ID == "" {
		if authEnabled {
			owner.ID = principal.ID
		} else {
			return owner, fmt.Errorf("spec.owner.id is required")
		}
	}
	return owner, nil
}

func normalizeCreateOwner(body *createRequest, principal principal, authEnabled bool) (spritzv1.SpritzOwner, error) {
	owner, err := normalizeCreateOwnerRequest(body, principal, authEnabled)
	if err != nil {
		return owner, err
	}
	if body != nil && body.OwnerRef != nil && strings.EqualFold(strings.TrimSpace(body.OwnerRef.Type), "external") {
		return owner, fmt.Errorf("ownerRef.type=external requires external resolution")
	}
	return owner, nil
}

func (s *server) resolveCreateOwner(ctx context.Context, body *createRequest, principal principal) (spritzv1.SpritzOwner, *externalOwnerResolution, error) {
	if body != nil && body.OwnerRef != nil && strings.EqualFold(strings.TrimSpace(body.OwnerRef.Type), "external") {
		normalizedRef, err := normalizeExternalOwnerRef(*body.OwnerRef)
		if err != nil {
			return spritzv1.SpritzOwner{}, nil, err
		}
		body.OwnerRef = &normalizedRef
		if strings.TrimSpace(body.OwnerID) != "" {
			return spritzv1.SpritzOwner{}, nil, fmt.Errorf("ownerId conflicts with ownerRef")
		}
		if strings.TrimSpace(body.Spec.Owner.ID) != "" {
			return spritzv1.SpritzOwner{}, nil, fmt.Errorf("spec.owner.id conflicts with ownerRef")
		}
		if !principalCanUseProvisionerFlow(principal) {
			return spritzv1.SpritzOwner{}, nil, errForbidden
		}
		if err := authorizeServiceAction(principal, scopeInstancesCreate, true); err != nil {
			return spritzv1.SpritzOwner{}, nil, errForbidden
		}
		if err := authorizeServiceAction(principal, scopeInstancesAssignOwner, true); err != nil {
			return spritzv1.SpritzOwner{}, nil, errForbidden
		}
		if err := authorizeServiceAction(principal, scopeExternalResolveViaCreate, true); err != nil {
			return spritzv1.SpritzOwner{}, nil, errForbidden
		}
		if !s.externalOwners.enabled() {
			return spritzv1.SpritzOwner{}, nil, externalOwnerResolutionError{
				status:   http.StatusForbidden,
				code:     "external_identity_forbidden",
				message:  "external identity resolution is not configured",
				provider: normalizedRef.Provider,
				tenant:   normalizedRef.Tenant,
				subject:  normalizedRef.Subject,
			}
		}
		resolution, err := s.externalOwners.resolve(ctx, principal, normalizedRef, body.RequestID)
		if err != nil {
			return spritzv1.SpritzOwner{}, nil, err
		}
		owner := body.Spec.Owner
		owner.ID = resolution.OwnerID
		body.Spec.Owner = owner
		return owner, &resolution, nil
	}

	owner, err := normalizeCreateOwner(body, principal, s.auth.enabled())
	return owner, nil, err
}

func validateProvisionerRequestSurface(body *createRequest) error {
	if body == nil {
		return nil
	}
	if body.Spec.Owner.Team != "" {
		return fmt.Errorf("spec.owner.team is not allowed")
	}
	if len(body.Labels) > 0 {
		return fmt.Errorf("labels are not allowed for service principals")
	}
	if len(body.Annotations) > 0 {
		return fmt.Errorf("annotations are not allowed for service principals")
	}
	if len(body.Spec.Labels) > 0 {
		return fmt.Errorf("spec.labels are not allowed")
	}
	if len(body.Spec.Annotations) > 0 {
		return fmt.Errorf("spec.annotations are not allowed")
	}
	if len(body.Spec.Env) > 0 {
		return fmt.Errorf("spec.env is not allowed")
	}
	if len(body.Spec.Repos) > 0 {
		return fmt.Errorf("spec.repos is not allowed")
	}
	if body.Spec.Repo != nil && body.Spec.Repo.Auth != nil {
		return fmt.Errorf("spec.repo.auth is not allowed")
	}
	if len(body.Spec.SharedMounts) > 0 {
		return fmt.Errorf("spec.sharedMounts is not allowed")
	}
	if !reflect.DeepEqual(body.Spec.Resources, corev1.ResourceRequirements{}) {
		return fmt.Errorf("spec.resources is not allowed")
	}
	if body.Spec.Features != nil {
		return fmt.Errorf("spec.features is not allowed")
	}
	if body.Spec.SSH != nil {
		return fmt.Errorf("spec.ssh is not allowed")
	}
	if len(body.Spec.Ports) > 0 {
		return fmt.Errorf("spec.ports is not allowed")
	}
	if body.Spec.Ingress != nil {
		return fmt.Errorf("spec.ingress is not allowed")
	}
	return nil
}

func provisionerSource(body *createRequest) string {
	source := strings.TrimSpace(body.Source)
	if source == "" {
		source = defaultProvisionerSource
	}
	return source
}

func (p provisionerPolicy) validateNamespace(namespace string) error {
	if len(p.allowedNamespaces) == 0 {
		return nil
	}
	if _, ok := p.allowedNamespaces[namespace]; ok {
		return nil
	}
	return fmt.Errorf("namespace is not allowed: %s", namespace)
}

func (p provisionerPolicy) validatePreset(presetID string) error {
	if presetID == "" {
		return nil
	}
	if len(p.allowedPresetIDs) == 0 {
		return nil
	}
	if _, ok := p.allowedPresetIDs[presetID]; ok {
		return nil
	}
	return fmt.Errorf("preset is not allowed: %s", presetID)
}

func (s *server) validateProvisionerPlacement(principal principal, namespace, presetID string, requestedImage, requestedNamespace bool, scope string) error {
	if !principalCanUseProvisionerFlow(principal) {
		return errForbidden
	}
	if err := authorizeServiceAction(principal, scope, true); err != nil {
		return err
	}
	if requestedNamespace && !s.provisioners.allowNamespaceOverride {
		return fmt.Errorf("namespace override is not allowed")
	}
	if err := s.provisioners.validateNamespace(namespace); err != nil {
		return err
	}
	if presetID != "" {
		if err := s.provisioners.validatePreset(presetID); err != nil {
			return err
		}
	}
	if requestedImage && !s.provisioners.allowCustomImage {
		return fmt.Errorf("custom image is not allowed")
	}
	return nil
}

func (s *server) validateProvisionerCreate(ctx context.Context, principal principal, namespace string, body *createRequest, requestedImage, requestedRepo, requestedNamespace bool) error {
	if err := s.validateProvisionerPlacement(principal, namespace, body.PresetID, requestedImage, requestedNamespace, scopeInstancesCreate); err != nil {
		return err
	}
	if err := authorizeServiceAction(principal, scopeInstancesAssignOwner, true); err != nil {
		return err
	}
	if requestedRepo && !s.provisioners.allowCustomRepo {
		return fmt.Errorf("custom repo is not allowed")
	}
	if body.IdempotencyKey == "" {
		return fmt.Errorf("idempotencyKey is required")
	}
	return nil
}

func (s *server) enforceProvisionerQuotas(ctx context.Context, namespace string, principal principal, ownerID string) error {
	if s.provisioners.maxActivePerOwner <= 0 && s.provisioners.maxCreatesPerActor <= 0 && s.provisioners.maxCreatesPerOwner <= 0 {
		return nil
	}
	if s.provisioners.allowNamespaceOverride && len(s.provisioners.allowedNamespaces) == 0 &&
		(s.provisioners.maxActivePerOwner > 0 || s.provisioners.maxCreatesPerActor > 0 || s.provisioners.maxCreatesPerOwner > 0) {
		return fmt.Errorf("quota enforcement requires allowed namespaces when namespace override is enabled")
	}
	namespaces := []string{namespace}
	if fixedNamespace := strings.TrimSpace(s.namespace); fixedNamespace != "" {
		namespaces = []string{fixedNamespace}
	} else if s.provisioners.allowNamespaceOverride && len(s.provisioners.allowedNamespaces) > 0 {
		namespaces = namespaces[:0]
		for allowedNamespace := range s.provisioners.allowedNamespaces {
			namespaces = append(namespaces, allowedNamespace)
		}
		sort.Strings(namespaces)
	}
	activeForOwner := 0
	actorCreates := 0
	ownerCreates := 0
	cutoff := time.Now().Add(-s.provisioners.rateWindow)
	for _, listNamespace := range namespaces {
		list := &spritzv1.SpritzList{}
		if err := s.client.List(ctx, list, client.InNamespace(listNamespace)); err != nil {
			return err
		}
		for _, item := range list.Items {
			if item.DeletionTimestamp != nil {
				continue
			}
			if item.Spec.Owner.ID == ownerID && item.Status.Phase != "Expired" {
				activeForOwner++
			}
			if s.provisioners.rateWindow > 0 && item.CreationTimestamp.Time.Before(cutoff) {
				continue
			}
			if item.Annotations[actorIDAnnotationKey] == principal.ID {
				actorCreates++
			}
			if item.Spec.Owner.ID == ownerID {
				ownerCreates++
			}
		}
	}
	if s.provisioners.maxActivePerOwner > 0 && activeForOwner >= s.provisioners.maxActivePerOwner {
		return fmt.Errorf("owner active instance limit reached")
	}
	if s.provisioners.maxCreatesPerActor > 0 && actorCreates >= s.provisioners.maxCreatesPerActor {
		return fmt.Errorf("actor create rate limit reached")
	}
	if s.provisioners.maxCreatesPerOwner > 0 && ownerCreates >= s.provisioners.maxCreatesPerOwner {
		return fmt.Errorf("owner create rate limit reached")
	}
	return nil
}

func createRequestFingerprint(body createRequest, namespace, name, namePrefix string, userConfig json.RawMessage) (string, error) {
	return createRequestFingerprintWithIssuer(body, "", namespace, name, namePrefix, userConfig)
}

func createRequestFingerprintWithIssuer(body createRequest, externalIssuer, namespace, name, namePrefix string, userConfig json.RawMessage) (string, error) {
	return createFingerprint(
		body.OwnerID,
		body.OwnerRef,
		body.Spec.Owner.ID,
		externalIssuer,
		sanitizeSpritzNameToken(body.PresetID),
		body.PresetInputs,
		strings.TrimSpace(name),
		sanitizeSpritzNameToken(namePrefix),
		namespace,
		provisionerSource(&body),
		body.Spec,
		userConfig,
	)
}

func createResolvedProvisionerPayload(body createRequest, resolvedNamePrefix string, resolvedExternalOwner *externalOwnerResolution) (string, error) {
	payload := idempotentCreatePayload{
		PresetID:      sanitizeSpritzNameToken(body.PresetID),
		NamePrefix:    sanitizeSpritzNameToken(resolvedNamePrefix),
		Source:        provisionerSource(&body),
		RequestID:     strings.TrimSpace(body.RequestID),
		Spec:          body.Spec,
		Labels:        cloneStringMap(body.Labels),
		Annotations:   cloneStringMap(body.Annotations),
		ExternalOwner: newIdempotentExternalOwnerPayload(resolvedExternalOwner),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeResolvedProvisionerPayload(raw string) (idempotentCreatePayload, error) {
	payload := idempotentCreatePayload{}
	if strings.TrimSpace(raw) == "" {
		return payload, nil
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return idempotentCreatePayload{}, err
	}
	return payload, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func newPresetCatalog() (presetCatalog, error) {
	raw := strings.TrimSpace(envOrDefault("SPRITZ_PRESETS", ""))
	if raw == "" {
		return presetCatalog{}, nil
	}
	var items []runtimePreset
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return presetCatalog{}, fmt.Errorf("invalid SPRITZ_PRESETS: %w", err)
	}
	normalized := make([]runtimePreset, 0, len(items))
	seen := map[string]struct{}{}
	for index, item := range items {
		item.Image = strings.TrimSpace(item.Image)
		if item.Image == "" {
			continue
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Description = strings.TrimSpace(item.Description)
		item.TTL = strings.TrimSpace(item.TTL)
		item.IdleTTL = strings.TrimSpace(item.IdleTTL)
		rawInstanceClass := strings.TrimSpace(item.InstanceClass)
		item.InstanceClass = sanitizeSpritzNameToken(item.InstanceClass)
		if rawInstanceClass != "" && item.InstanceClass == "" {
			return presetCatalog{}, fmt.Errorf("invalid SPRITZ_PRESETS: presets[%d].instanceClass is invalid", index)
		}
		item.NamePrefix = resolveSpritzNamePrefix(item.NamePrefix, item.Image)
		item.ID = normalizePresetID(item)
		if item.ID == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			return presetCatalog{}, fmt.Errorf("duplicate preset id: %s", item.ID)
		}
		seen[item.ID] = struct{}{}
		normalized = append(normalized, item)
	}
	return presetCatalog{byID: normalized}, nil
}

func normalizePresetID(preset runtimePreset) string {
	if id := sanitizeSpritzNameToken(preset.ID); id != "" {
		return id
	}
	if id := sanitizeSpritzNameToken(preset.Name); id != "" {
		return id
	}
	return deriveSpritzNamePrefixFromImage(preset.Image)
}

func (c presetCatalog) all() []runtimePreset {
	if len(c.byID) == 0 {
		return nil
	}
	items := append([]runtimePreset(nil), c.byID...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (c presetCatalog) public() []publicPreset {
	items := c.all()
	if len(items) == 0 {
		return nil
	}
	return publicPresetList(items, true)
}

func (c presetCatalog) publicHuman() []publicPreset {
	items := c.all()
	if len(items) == 0 {
		return nil
	}
	return publicPresetList(items, false)
}

func publicPresetList(items []runtimePreset, includeHidden bool) []publicPreset {
	if len(items) == 0 {
		return nil
	}
	publicItems := make([]publicPreset, 0, len(items))
	for _, item := range items {
		if item.Hidden && !includeHidden {
			continue
		}
		publicItems = append(publicItems, publicPreset{
			ID:            item.ID,
			Name:          item.Name,
			Description:   item.Description,
			Image:         item.Image,
			RepoURL:       item.RepoURL,
			Branch:        item.Branch,
			TTL:           item.TTL,
			IdleTTL:       item.IdleTTL,
			NamePrefix:    item.NamePrefix,
			InstanceClass: item.InstanceClass,
			Hidden:        item.Hidden,
		})
	}
	return publicItems
}

func (c presetCatalog) publicAllowed(allowed map[string]struct{}) []publicPreset {
	items := c.all()
	if len(items) == 0 || len(allowed) == 0 {
		return publicPresetList(items, true)
	}
	filtered := make([]runtimePreset, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	return publicPresetList(filtered, true)
}

func (c presetCatalog) get(id string) (*runtimePreset, bool) {
	id = sanitizeSpritzNameToken(id)
	if id == "" {
		return nil, false
	}
	for i := range c.byID {
		if c.byID[i].ID == id {
			copy := c.byID[i]
			return &copy, true
		}
	}
	return nil, false
}

func (s *server) allowedProvisionerPresets() []runtimePreset {
	items := s.presets.all()
	if len(items) == 0 || len(s.provisioners.allowedPresetIDs) == 0 {
		return items
	}
	filtered := make([]runtimePreset, 0, len(items))
	for _, item := range items {
		if _, ok := s.provisioners.allowedPresetIDs[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s *server) resolvedCreateNamePrefix(body createRequest, explicitNamePrefix string) string {
	if prefix := sanitizeSpritzNameToken(explicitNamePrefix); prefix != "" {
		return prefix
	}
	if preset, ok := s.presets.get(body.PresetID); ok {
		return resolvePresetNamePrefix("", *preset)
	}
	return resolveSpritzNamePrefix("", body.Spec.Image)
}

func (s *server) resolvedCreateFingerprint(body createRequest, namespace, explicitNamePrefix string, userConfig json.RawMessage) (string, error) {
	namePrefix := ""
	if strings.TrimSpace(body.Name) == "" {
		namePrefix = s.resolvedCreateNamePrefix(body, explicitNamePrefix)
	}
	return createFingerprint(
		body.OwnerID,
		body.OwnerRef,
		body.Spec.Owner.ID,
		"",
		sanitizeSpritzNameToken(body.PresetID),
		body.PresetInputs,
		strings.TrimSpace(body.Name),
		sanitizeSpritzNameToken(namePrefix),
		namespace,
		provisionerSource(&body),
		body.Spec,
		userConfig,
	)
}

func (s *server) provisionerIdempotencyFingerprints(requestBody, resolvedBody createRequest, resolvedExternalOwner *externalOwnerResolution, externalIssuer, namespace string, userConfig json.RawMessage) (provisionerIdempotencyState, error) {
	canonicalName := strings.TrimSpace(requestBody.Name)
	canonicalNamePrefix := ""
	if canonicalName == "" {
		canonicalNamePrefix = strings.TrimSpace(requestBody.NamePrefix)
	}
	canonicalFingerprint, err := createRequestFingerprintWithIssuer(requestBody, externalIssuer, namespace, canonicalName, canonicalNamePrefix, userConfig)
	if err != nil {
		return provisionerIdempotencyState{}, err
	}
	resolvedPayload, err := createResolvedProvisionerPayload(resolvedBody, s.resolvedCreateNamePrefix(resolvedBody, requestBody.NamePrefix), resolvedExternalOwner)
	if err != nil {
		return provisionerIdempotencyState{}, err
	}
	return provisionerIdempotencyState{
		canonicalFingerprint: canonicalFingerprint,
		resolvedPayload:      resolvedPayload,
	}, nil
}

func newProvisionerPolicy() provisionerPolicy {
	defaultIdle := parseDurationEnv("SPRITZ_PROVISIONER_DEFAULT_IDLE_TTL", defaultProvisionerIdleTTL)
	maxIdle := parseDurationEnv("SPRITZ_PROVISIONER_MAX_IDLE_TTL", defaultIdle)
	defaultTTL := parseDurationEnv("SPRITZ_PROVISIONER_DEFAULT_TTL", defaultProvisionerMaxTTL)
	maxTTL := parseDurationEnv("SPRITZ_PROVISIONER_MAX_TTL", defaultTTL)
	return provisionerPolicy{
		allowedPresetIDs:       splitSet(osEnvString("SPRITZ_PROVISIONER_ALLOWED_PRESET_IDS")),
		defaultPresetID:        sanitizeSpritzNameToken(osEnvString("SPRITZ_PROVISIONER_DEFAULT_PRESET_ID")),
		allowCustomImage:       parseBoolEnv("SPRITZ_PROVISIONER_ALLOW_CUSTOM_IMAGE", false),
		allowCustomRepo:        parseBoolEnv("SPRITZ_PROVISIONER_ALLOW_CUSTOM_REPO", false),
		allowNamespaceOverride: parseBoolEnv("SPRITZ_PROVISIONER_ALLOW_NAMESPACE_OVERRIDE", false),
		allowedNamespaces:      splitSet(osEnvString("SPRITZ_PROVISIONER_ALLOWED_NAMESPACES")),
		defaultIdleTTL:         defaultIdle,
		maxIdleTTL:             maxIdle,
		defaultTTL:             defaultTTL,
		maxTTL:                 maxTTL,
		maxActivePerOwner:      parseIntEnvAllowZero("SPRITZ_PROVISIONER_MAX_ACTIVE_PER_OWNER", 0),
		maxCreatesPerActor:     parseIntEnvAllowZero("SPRITZ_PROVISIONER_MAX_CREATES_PER_ACTOR", 0),
		maxCreatesPerOwner:     parseIntEnvAllowZero("SPRITZ_PROVISIONER_MAX_CREATES_PER_OWNER", 0),
		rateWindow:             parseDurationEnv("SPRITZ_PROVISIONER_RATE_WINDOW", time.Hour),
	}
}

func osEnvString(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func principalCanAccessOwner(principal principal, ownerID string) bool {
	if principal.isAdminPrincipal() {
		return true
	}
	return principal.isHuman() && principal.ID == ownerID
}

func principalCanUseProvisionerFlow(principal principal) bool {
	return principal.isService() || principal.isAdminPrincipal()
}

func buildPresetIntoSpec(spec *spritzv1.SpritzSpec, preset *runtimePreset) {
	if preset == nil || spec == nil {
		return
	}
	if strings.TrimSpace(spec.Image) == "" {
		spec.Image = preset.Image
	}
	if strings.TrimSpace(spec.TTL) == "" && preset.TTL != "" {
		spec.TTL = preset.TTL
	}
	if strings.TrimSpace(spec.IdleTTL) == "" && preset.IdleTTL != "" {
		spec.IdleTTL = preset.IdleTTL
	}
	if spec.Repo == nil && len(spec.Repos) == 0 && strings.TrimSpace(preset.RepoURL) != "" {
		spec.Repo = &spritzv1.SpritzRepo{
			URL:    preset.RepoURL,
			Branch: strings.TrimSpace(preset.Branch),
		}
	}
	if len(spec.Env) == 0 && len(preset.Env) > 0 {
		spec.Env = append([]corev1.EnvVar(nil), preset.Env...)
	}
}

func resolveCreateLifetimes(spec *spritzv1.SpritzSpec, policy provisionerPolicy, servicePrincipal bool) error {
	if spec == nil {
		return nil
	}
	if servicePrincipal {
		if strings.TrimSpace(spec.IdleTTL) == "" && policy.defaultIdleTTL > 0 {
			spec.IdleTTL = policy.defaultIdleTTL.String()
		}
		if strings.TrimSpace(spec.TTL) == "" && policy.defaultTTL > 0 {
			spec.TTL = policy.defaultTTL.String()
		}
	}
	if spec.IdleTTL != "" {
		parsed, err := time.ParseDuration(spec.IdleTTL)
		if err != nil {
			return fmt.Errorf("invalid idleTtl")
		}
		if parsed <= 0 {
			return fmt.Errorf("idleTtl must be greater than zero")
		}
		if servicePrincipal && policy.maxIdleTTL > 0 && parsed > policy.maxIdleTTL {
			return fmt.Errorf("idleTtl exceeds max idle ttl of %s", policy.maxIdleTTL)
		}
	}
	if spec.TTL != "" {
		parsed, err := time.ParseDuration(spec.TTL)
		if err != nil {
			return fmt.Errorf("invalid ttl")
		}
		if parsed <= 0 {
			return fmt.Errorf("ttl must be greater than zero")
		}
		if servicePrincipal && policy.maxTTL > 0 && parsed > policy.maxTTL {
			return fmt.Errorf("ttl exceeds max ttl of %s", policy.maxTTL)
		}
	}
	return nil
}

func actorLabelValue(id string) string {
	return hashLabelValue("actor", id)
}

func idempotencyLabelValue(key string) string {
	return hashLabelValue("idem", key)
}

func hashLabelValue(prefix, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s-%x", prefix, sum[:12])
}

func createFingerprint(ownerID string, ref *ownerRef, resolvedOwnerID, externalIssuer, presetID string, presetInputs json.RawMessage, name, namePrefix, namespace, source string, spec spritzv1.SpritzSpec, userConfig json.RawMessage) (string, error) {
	specCopy := spec
	specCopy.Annotations = nil
	specCopy.Labels = nil
	canonicalOwnerID, ownerPayload, err := canonicalOwnerFingerprintPayload(ownerID, ref, resolvedOwnerID, externalIssuer)
	if err != nil {
		return "", err
	}
	payload := struct {
		OwnerID      string              `json:"ownerId,omitempty"`
		Owner        any                 `json:"owner,omitempty"`
		PresetID     string              `json:"presetId,omitempty"`
		PresetInputs json.RawMessage     `json:"presetInputs,omitempty"`
		Name         string              `json:"name,omitempty"`
		NamePrefix   string              `json:"namePrefix,omitempty"`
		Namespace    string              `json:"namespace,omitempty"`
		Source       string              `json:"source,omitempty"`
		Spec         spritzv1.SpritzSpec `json:"spec"`
		UserConfig   json.RawMessage     `json:"userConfig,omitempty"`
	}{
		OwnerID:      canonicalOwnerID,
		Owner:        ownerPayload,
		PresetID:     presetID,
		PresetInputs: presetInputs,
		Name:         name,
		NamePrefix:   strings.TrimSpace(namePrefix),
		Namespace:    namespace,
		Source:       source,
		Spec:         specCopy,
		UserConfig:   userConfig,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:]), nil
}

func canonicalOwnerFingerprintPayload(ownerID string, ref *ownerRef, resolvedOwnerID, externalIssuer string) (string, any, error) {
	switch {
	case ref != nil:
		normalizedType := strings.ToLower(strings.TrimSpace(ref.Type))
		switch normalizedType {
		case "owner":
			if id := strings.TrimSpace(ref.ID); id != "" {
				return id, nil, nil
			}
		case "external":
			normalized, err := normalizeExternalOwnerRef(*ref)
			if err != nil {
				return "", nil, err
			}
			payload := canonicalOwnerRefPayload(normalized)
			if issuer := strings.TrimSpace(externalIssuer); issuer != "" {
				payload["issuer"] = issuer
			}
			return "", payload, nil
		case "":
			return "", nil, fmt.Errorf("ownerRef.type is required")
		default:
			return "", nil, fmt.Errorf("ownerRef.type must be owner or external")
		}
	case strings.TrimSpace(ownerID) != "":
		return strings.TrimSpace(ownerID), nil, nil
	}

	if strings.TrimSpace(ownerID) != "" {
		return strings.TrimSpace(ownerID), nil, nil
	}
	return strings.TrimSpace(resolvedOwnerID), nil, nil
}

func (s *server) externalOwnerIssuerForPrincipal(principal principal) string {
	if policy, ok := s.externalOwners.policyForPrincipal(principal); ok {
		return policy.issuer()
	}
	return strings.TrimSpace(principal.ID)
}

func canonicalOwnerRefPayload(ref ownerRef) map[string]string {
	payload := map[string]string{
		"type": strings.ToLower(strings.TrimSpace(ref.Type)),
	}
	if id := strings.TrimSpace(ref.ID); id != "" {
		payload["id"] = id
	}
	if provider := strings.ToLower(strings.TrimSpace(ref.Provider)); provider != "" {
		payload["provider"] = provider
	}
	if tenant := strings.TrimSpace(ref.Tenant); tenant != "" {
		payload["tenant"] = tenant
	}
	if subject := strings.TrimSpace(ref.Subject); subject != "" {
		payload["subject"] = subject
	}
	return payload
}

func newIdempotentExternalOwnerPayload(resolution *externalOwnerResolution) *idempotentExternalOwnerPayload {
	if resolution == nil {
		return nil
	}
	payload := &idempotentExternalOwnerPayload{
		Issuer:      strings.TrimSpace(resolution.Issuer),
		Provider:    strings.TrimSpace(resolution.Provider),
		Tenant:      strings.TrimSpace(resolution.Tenant),
		SubjectHash: strings.TrimSpace(resolution.SubjectHash),
	}
	if !resolution.ResolvedAt.IsZero() {
		payload.ResolvedAt = resolution.ResolvedAt.UTC().Format(time.RFC3339)
	}
	return payload
}

func (p *idempotentExternalOwnerPayload) resolution() *externalOwnerResolution {
	if p == nil {
		return nil
	}
	resolution := &externalOwnerResolution{
		Issuer:      strings.TrimSpace(p.Issuer),
		Provider:    strings.TrimSpace(p.Provider),
		Tenant:      strings.TrimSpace(p.Tenant),
		SubjectHash: strings.TrimSpace(p.SubjectHash),
	}
	if strings.TrimSpace(p.ResolvedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, p.ResolvedAt)
		if err == nil {
			resolution.ResolvedAt = parsed.UTC()
		}
	}
	return resolution
}

func idempotencyReservationName(actorID, key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(actorID) + ":" + strings.TrimSpace(key)))
	return fmt.Sprintf("%s%x", idempotencyReservationPrefix, sum[:16])
}

func (s *server) idempotencyReservationNamespace() string {
	if namespace := strings.TrimSpace(s.controlNamespace); namespace != "" {
		return namespace
	}
	if namespace := strings.TrimSpace(s.namespace); namespace != "" {
		return namespace
	}
	return "default"
}

func (s *server) idempotencyReservations() *idempotencyReservationStore {
	return newIdempotencyReservationStore(s.client, s.idempotencyReservationNamespace())
}

func (s *server) getIdempotencyReservation(ctx context.Context, actorID, key, fingerprint string) (string, bool, string, bool, error) {
	record, found, err := s.idempotencyReservations().get(ctx, actorID, key)
	if err != nil {
		return "", false, "", false, err
	}
	if !found {
		return "", false, "", false, nil
	}
	if strings.TrimSpace(record.fingerprint) != strings.TrimSpace(fingerprint) {
		return "", false, "", false, errIdempotencyUsedDifferent
	}
	return record.name, record.completed, record.payload, true, nil
}

func (s *server) reserveIdempotentCreateName(ctx context.Context, namespace string, principal principal, key, desiredName string, state provisionerIdempotencyState) (string, bool, string, error) {
	if strings.TrimSpace(key) == "" {
		return desiredName, false, strings.TrimSpace(state.resolvedPayload), nil
	}
	store := s.idempotencyReservations()
	record := idempotencyReservationRecord{
		fingerprint: state.canonicalFingerprint,
		name:        desiredName,
		payload:     strings.TrimSpace(state.resolvedPayload),
		completed:   false,
	}
	if err := store.create(ctx, principal.ID, key, record); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", false, "", err
		}
		existing, found, getErr := store.get(ctx, principal.ID, key)
		if getErr != nil {
			return "", false, "", getErr
		}
		if !found {
			return "", false, "", apierrors.NewNotFound(corev1.Resource("configmaps"), idempotencyReservationName(principal.ID, key))
		}
		if strings.TrimSpace(existing.fingerprint) != strings.TrimSpace(state.canonicalFingerprint) {
			return "", false, "", errIdempotencyUsedDifferent
		}
		done := existing.completed
		name := existing.name
		storedPayload := existing.payload
		if storedPayload == "" {
			return "", false, "", errIdempotencyIncompatiblePending
		}
		if done {
			if name == "" {
				name = desiredName
			}
			return name, true, storedPayload, nil
		}
		if name == "" {
			return s.setIdempotencyReservationName(ctx, principal.ID, key, "", desiredName, state)
		}
		reservedSpritz, getErr := s.findReservedSpritz(ctx, namespace, name)
		if getErr != nil {
			return "", false, "", getErr
		}
		if reservedSpritz != nil && !matchesIdempotentReplayTarget(reservedSpritz, principal, key, state.canonicalFingerprint) {
			return s.setIdempotencyReservationName(ctx, principal.ID, key, name, desiredName, state)
		}
		return name, false, storedPayload, nil
	}
	return desiredName, false, strings.TrimSpace(state.resolvedPayload), nil
}

func (s *server) completeIdempotencyReservation(ctx context.Context, actorID, key string, spritz *spritzv1.Spritz) error {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(key) == "" || spritz == nil {
		return nil
	}
	_, err := s.idempotencyReservations().update(ctx, actorID, key, func(record *idempotencyReservationRecord) error {
		record.name = spritz.Name
		record.completed = true
		return nil
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (s *server) setIdempotencyReservationName(ctx context.Context, actorID, key, failedName, proposedName string, state provisionerIdempotencyState) (string, bool, string, error) {
	failedName = strings.TrimSpace(failedName)
	proposedName = strings.TrimSpace(proposedName)
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(key) == "" {
		return proposedName, false, strings.TrimSpace(state.resolvedPayload), nil
	}
	selectedName := proposedName
	completed := false
	selectedPayload := strings.TrimSpace(state.resolvedPayload)
	_, err := s.idempotencyReservations().update(ctx, actorID, key, func(record *idempotencyReservationRecord) error {
		if strings.TrimSpace(record.fingerprint) != strings.TrimSpace(state.canonicalFingerprint) {
			return errIdempotencyUsedDifferent
		}
		if strings.TrimSpace(record.payload) == "" {
			return errIdempotencyIncompatiblePending
		}
		if record.completed {
			if strings.TrimSpace(record.name) == "" {
				record.name = proposedName
			}
			selectedName = record.name
			completed = true
			selectedPayload = record.payload
			return nil
		}
		if strings.TrimSpace(record.name) == "" {
			if proposedName == "" {
				selectedName = ""
				completed = false
				selectedPayload = record.payload
				return nil
			}
			record.name = proposedName
			record.completed = false
			selectedName = proposedName
			completed = false
			selectedPayload = record.payload
			return nil
		}
		if failedName == "" || record.name != failedName || proposedName == "" || proposedName == record.name {
			selectedName = record.name
			completed = false
			selectedPayload = record.payload
			return nil
		}
		record.name = proposedName
		record.completed = false
		selectedName = proposedName
		completed = false
		selectedPayload = record.payload
		return nil
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return proposedName, false, strings.TrimSpace(state.resolvedPayload), nil
		}
		return "", false, "", err
	}
	if strings.TrimSpace(selectedPayload) == "" {
		selectedPayload = strings.TrimSpace(state.resolvedPayload)
	}
	return selectedName, completed, selectedPayload, nil
}

func (s *server) findReservedSpritz(ctx context.Context, namespace, name string) (*spritzv1.Spritz, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, name), spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return spritz, nil
}

func matchesIdempotentReplayTarget(spritz *spritzv1.Spritz, principal principal, key, fingerprint string) bool {
	if spritz == nil {
		return false
	}
	annotations := spritz.GetAnnotations()
	if strings.TrimSpace(annotations[idempotencyHashAnnotationKey]) != strings.TrimSpace(fingerprint) {
		return false
	}
	if strings.TrimSpace(annotations[idempotencyKeyAnnotationKey]) != strings.TrimSpace(key) {
		return false
	}
	if strings.TrimSpace(annotations[actorIDAnnotationKey]) != strings.TrimSpace(principal.ID) {
		return false
	}
	return true
}

func summarizeCreateResponse(spritz *spritzv1.Spritz, principal principal, presetID, source, idempotencyKey string, replayed bool) createSpritzResponse {
	annotations := spritz.GetAnnotations()
	responseSpritz := spritz.DeepCopy()
	ownerID := spritz.Spec.Owner.ID
	if principal.isService() && hasExternalOwnerAnnotations(annotations) {
		ownerID = ""
		responseSpritz.Spec.Owner.ID = ""
		if responseSpritz.Labels != nil {
			delete(responseSpritz.Labels, ownerLabelKey)
		}
	}
	if principal.isService() {
		if storedPresetID := strings.TrimSpace(annotations[presetIDAnnotationKey]); storedPresetID != "" {
			presetID = storedPresetID
		}
		if storedSource := strings.TrimSpace(annotations[sourceAnnotationKey]); storedSource != "" {
			source = storedSource
		}
		if storedIdempotencyKey := strings.TrimSpace(annotations[idempotencyKeyAnnotationKey]); storedIdempotencyKey != "" {
			idempotencyKey = storedIdempotencyKey
		}
	}
	createdAt := spritz.CreationTimestamp.DeepCopy()
	idleExpiresAt, maxExpiresAt, expiresAt := lifecycleExpiryTimes(spritz, time.Now())
	instanceURL := spritzv1.InstanceURLForSpritz(spritz)
	chatURL := spritzv1.ChatURLForSpritz(spritz)
	return createSpritzResponse{
		Spritz:         responseSpritz,
		AccessURL:      spritzv1.AccessURLForSpritz(spritz),
		ChatURL:        chatURL,
		InstanceURL:    instanceURL,
		Namespace:      spritz.Namespace,
		OwnerID:        ownerID,
		ActorID:        principal.ID,
		ActorType:      string(principal.Type),
		PresetID:       presetID,
		Source:         source,
		IdempotencyKey: idempotencyKey,
		Replayed:       replayed,
		CreatedAt:      createdAt,
		IdleTTL:        strings.TrimSpace(spritz.Spec.IdleTTL),
		TTL:            strings.TrimSpace(spritz.Spec.TTL),
		IdleExpiresAt:  idleExpiresAt,
		MaxExpiresAt:   maxExpiresAt,
		ExpiresAt:      expiresAt,
	}
}

func hasExternalOwnerAnnotations(annotations map[string]string) bool {
	if len(annotations) == 0 {
		return false
	}
	return strings.TrimSpace(annotations[externalOwnerIssuerAnnotationKey]) != "" &&
		strings.TrimSpace(annotations[externalOwnerProviderAnnotationKey]) != "" &&
		strings.TrimSpace(annotations[externalOwnerSubjectHashAnnotationKey]) != ""
}

func lifecycleExpiryTimes(spritz *spritzv1.Spritz, _ time.Time) (*metav1.Time, *metav1.Time, *metav1.Time) {
	idleExpiresAt, maxExpiresAt, effectiveExpiresAt, _, err := spritzv1.LifecycleExpiryTimes(spritz)
	if err != nil {
		return nil, nil, nil
	}
	return idleExpiresAt, maxExpiresAt, effectiveExpiresAt
}

func resolvePresetNamePrefix(explicit string, preset runtimePreset) string {
	if prefix := sanitizeSpritzNameToken(explicit); prefix != "" {
		return prefix
	}
	if prefix := sanitizeSpritzNameToken(preset.NamePrefix); prefix != "" {
		return prefix
	}
	return deriveSpritzNamePrefixFromImage(preset.Image)
}

func (s *server) resolveSuggestNameMetadata(body suggestNameRequest) (suggestNameMetadata, error) {
	metadata := suggestNameMetadata{
		presetID: sanitizeSpritzNameToken(body.PresetID),
	}
	if metadata.presetID != "" {
		preset, ok := s.presets.get(metadata.presetID)
		if !ok {
			return suggestNameMetadata{}, fmt.Errorf("preset not found: %s", metadata.presetID)
		}
		metadata.image = preset.Image
		metadata.namePrefix = resolvePresetNamePrefix(body.NamePrefix, *preset)
		return metadata, nil
	}
	metadata.image = strings.TrimSpace(body.Image)
	if metadata.image == "" {
		return suggestNameMetadata{}, fmt.Errorf("image or presetId is required")
	}
	metadata.namePrefix = resolveSpritzNamePrefix(body.NamePrefix, metadata.image)
	return metadata, nil
}
