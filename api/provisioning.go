package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	scopeInstancesCreate      = "spritz.instances.create"
	scopeInstancesAssignOwner = "spritz.instances.assign_owner"
	scopePresetsRead          = "spritz.presets.read"
	scopeInstancesSuggestName = "spritz.instances.suggest_name"

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
	defaultProvisionerSource      = "external"
	defaultProvisionerIdleTTL     = 24 * time.Hour
	defaultProvisionerMaxTTL      = 7 * 24 * time.Hour
)

type runtimePreset struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Image       string          `json:"image,omitempty"`
	RepoURL     string          `json:"repoUrl,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	TTL         string          `json:"ttl,omitempty"`
	IdleTTL     string          `json:"idleTtl,omitempty"`
	NamePrefix  string          `json:"namePrefix,omitempty"`
	Env         []corev1.EnvVar `json:"env,omitempty"`
}

type publicPreset struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	RepoURL     string `json:"repoUrl,omitempty"`
	Branch      string `json:"branch,omitempty"`
	TTL         string `json:"ttl,omitempty"`
	IdleTTL     string `json:"idleTtl,omitempty"`
	NamePrefix  string `json:"namePrefix,omitempty"`
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

func normalizeCreateOwner(body *createRequest, principal principal, authEnabled bool) (spritzv1.SpritzOwner, error) {
	owner := body.Spec.Owner
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

func (s *server) validateProvisionerCreate(ctx context.Context, principal principal, namespace string, body *createRequest, userConfig json.RawMessage, requestedImage, requestedRepo, requestedNamespace bool, nameForFingerprint, namePrefixForFingerprint string) (string, error) {
	if !principalCanUseProvisionerFlow(principal) {
		return "", errForbidden
	}
	if err := authorizeServiceAction(principal, scopeInstancesCreate, true); err != nil {
		return "", err
	}
	if err := authorizeServiceAction(principal, scopeInstancesAssignOwner, true); err != nil {
		return "", err
	}
	if requestedNamespace && !s.provisioners.allowNamespaceOverride {
		return "", fmt.Errorf("namespace override is not allowed")
	}
	if err := s.provisioners.validateNamespace(namespace); err != nil {
		return "", err
	}
	if body.PresetID != "" {
		if err := s.provisioners.validatePreset(body.PresetID); err != nil {
			return "", err
		}
	}
	if requestedImage && !s.provisioners.allowCustomImage {
		return "", fmt.Errorf("custom image is not allowed")
	}
	if requestedRepo && !s.provisioners.allowCustomRepo {
		return "", fmt.Errorf("custom repo is not allowed")
	}
	if body.IdempotencyKey == "" {
		return "", fmt.Errorf("idempotencyKey is required")
	}
	if err := resolveCreateLifetimes(&body.Spec, s.provisioners, true); err != nil {
		return "", err
	}
	return createFingerprint(body.Spec.Owner.ID, body.PresetID, nameForFingerprint, namePrefixForFingerprint, namespace, provisionerSource(body), body.Spec, userConfig)
}

func (s *server) enforceProvisionerQuotas(ctx context.Context, namespace string, principal principal, ownerID string) error {
	list := &spritzv1.SpritzList{}
	if err := s.client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return err
	}
	activeForOwner := 0
	actorCreates := 0
	ownerCreates := 0
	cutoff := time.Now().Add(-s.provisioners.rateWindow)
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
	if s.provisioners.maxActivePerOwner > 0 && activeForOwner >= s.provisioners.maxActivePerOwner {
		return fmt.Errorf("owner active workspace limit reached")
	}
	if s.provisioners.maxCreatesPerActor > 0 && actorCreates >= s.provisioners.maxCreatesPerActor {
		return fmt.Errorf("actor create rate limit reached")
	}
	if s.provisioners.maxCreatesPerOwner > 0 && ownerCreates >= s.provisioners.maxCreatesPerOwner {
		return fmt.Errorf("owner create rate limit reached")
	}
	return nil
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
	for _, item := range items {
		item.Image = strings.TrimSpace(item.Image)
		if item.Image == "" {
			continue
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Description = strings.TrimSpace(item.Description)
		item.TTL = strings.TrimSpace(item.TTL)
		item.IdleTTL = strings.TrimSpace(item.IdleTTL)
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
	publicItems := make([]publicPreset, 0, len(items))
	for _, item := range items {
		publicItems = append(publicItems, publicPreset{
			ID:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			Image:       item.Image,
			RepoURL:     item.RepoURL,
			Branch:      item.Branch,
			TTL:         item.TTL,
			IdleTTL:     item.IdleTTL,
			NamePrefix:  item.NamePrefix,
		})
	}
	return publicItems
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

func createFingerprint(ownerID, presetID, name, namePrefix, namespace, source string, spec spritzv1.SpritzSpec, userConfig json.RawMessage) (string, error) {
	specCopy := spec
	specCopy.Annotations = nil
	specCopy.Labels = nil
	payload := struct {
		OwnerID    string              `json:"ownerId"`
		PresetID   string              `json:"presetId,omitempty"`
		Name       string              `json:"name,omitempty"`
		NamePrefix string              `json:"namePrefix,omitempty"`
		Namespace  string              `json:"namespace,omitempty"`
		Source     string              `json:"source,omitempty"`
		Spec       spritzv1.SpritzSpec `json:"spec"`
		UserConfig json.RawMessage     `json:"userConfig,omitempty"`
	}{
		OwnerID:    ownerID,
		PresetID:   presetID,
		Name:       name,
		NamePrefix: strings.TrimSpace(namePrefix),
		Namespace:  namespace,
		Source:     source,
		Spec:       specCopy,
		UserConfig: userConfig,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:]), nil
}

func idempotencyReservationName(actorID, key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(actorID) + ":" + strings.TrimSpace(key)))
	return fmt.Sprintf("%s%x", idempotencyReservationPrefix, sum[:16])
}

func (s *server) reserveIdempotentCreateName(ctx context.Context, namespace string, principal principal, key, fingerprint, desiredName string) (string, bool, error) {
	if strings.TrimSpace(key) == "" {
		return desiredName, false, nil
	}
	reservationName := idempotencyReservationName(principal.ID, key)
	record := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reservationName,
			Namespace: namespace,
			Labels: map[string]string{
				actorLabelKey:       actorLabelValue(principal.ID),
				idempotencyLabelKey: idempotencyLabelValue(key),
			},
		},
		Data: map[string]string{
			idempotencyReservationHashKey: fingerprint,
			idempotencyReservationNameKey: desiredName,
			idempotencyReservationDoneKey: "false",
		},
	}
	if err := s.client.Create(ctx, record); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", false, err
		}
		existing := &corev1.ConfigMap{}
		if getErr := s.client.Get(ctx, clientKey(namespace, reservationName), existing); getErr != nil {
			return "", false, getErr
		}
		if strings.TrimSpace(existing.Data[idempotencyReservationHashKey]) != fingerprint {
			return "", false, fmt.Errorf("idempotencyKey already used with a different request")
		}
		done := strings.EqualFold(strings.TrimSpace(existing.Data[idempotencyReservationDoneKey]), "true")
		name := strings.TrimSpace(existing.Data[idempotencyReservationNameKey])
		if done {
			if name == "" {
				name = desiredName
			}
			return name, true, nil
		}
		if name == "" {
			name = desiredName
		}
		if name != "" {
			reservedSpritz, getErr := s.findReservedSpritz(ctx, namespace, name)
			if getErr != nil {
				return "", false, getErr
			}
			if reservedSpritz != nil && strings.TrimSpace(reservedSpritz.Annotations[idempotencyHashAnnotationKey]) != fingerprint {
				name = desiredName
			}
		}
		if name == "" {
			name = desiredName
		}
		if name != strings.TrimSpace(existing.Data[idempotencyReservationNameKey]) {
			if existing.Data == nil {
				existing.Data = map[string]string{}
			}
			existing.Data[idempotencyReservationNameKey] = name
			existing.Data[idempotencyReservationDoneKey] = "false"
			if updateErr := s.client.Update(ctx, existing); updateErr != nil {
				return "", false, updateErr
			}
		}
		return name, done, nil
	}
	return desiredName, false, nil
}

func (s *server) completeIdempotencyReservation(ctx context.Context, namespace, actorID, key string, spritz *spritzv1.Spritz) error {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(key) == "" || spritz == nil {
		return nil
	}
	reservationName := idempotencyReservationName(actorID, key)
	current := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, clientKey(namespace, reservationName), current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if current.Data == nil {
		current.Data = map[string]string{}
	}
	current.Data[idempotencyReservationNameKey] = spritz.Name
	current.Data[idempotencyReservationDoneKey] = "true"
	return s.client.Update(ctx, current)
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
	createdAt := spritz.CreationTimestamp.DeepCopy()
	idleExpiresAt, maxExpiresAt, expiresAt := lifecycleExpiryTimes(spritz, time.Now())
	return createSpritzResponse{
		Spritz:         spritz,
		AccessURL:      spritzv1.AccessURLForSpritz(spritz),
		Namespace:      spritz.Namespace,
		OwnerID:        spritz.Spec.Owner.ID,
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

func lifecycleExpiryTimes(spritz *spritzv1.Spritz, _ time.Time) (*metav1.Time, *metav1.Time, *metav1.Time) {
	idleExpiresAt, maxExpiresAt, effectiveExpiresAt, _, err := spritzv1.LifecycleExpiryTimes(spritz)
	if err != nil {
		return nil, nil, nil
	}
	return idleExpiresAt, maxExpiresAt, effectiveExpiresAt
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
		metadata.namePrefix = resolveSpritzNamePrefix(body.NamePrefix, preset.NamePrefix)
		if metadata.namePrefix == "" {
			metadata.namePrefix = resolveSpritzNamePrefix("", preset.Image)
		}
		return metadata, nil
	}
	metadata.image = strings.TrimSpace(body.Image)
	if metadata.image == "" {
		return suggestNameMetadata{}, fmt.Errorf("image or presetId is required")
	}
	metadata.namePrefix = resolveSpritzNamePrefix(body.NamePrefix, metadata.image)
	return metadata, nil
}
