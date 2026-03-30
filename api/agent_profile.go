package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type agentProfileSyncInput struct {
	Owner            spritzv1.SpritzOwner         `json:"owner"`
	AgentRef         *spritzv1.SpritzAgentRef     `json:"agentRef,omitempty"`
	ProfileOverrides *spritzv1.SpritzAgentProfile `json:"profileOverrides,omitempty"`
}

type agentProfileSyncOutput struct {
	Profile *spritzv1.SpritzAgentProfile `json:"profile,omitempty"`
}

type resolvedAgentProfile struct {
	profile   *spritzv1.SpritzAgentProfile
	syncer    string
	syncedAt  *metav1.Time
	lastError string
}

func normalizeSpritzAgentRef(value *spritzv1.SpritzAgentRef) *spritzv1.SpritzAgentRef {
	if value == nil {
		return nil
	}
	normalized := &spritzv1.SpritzAgentRef{
		Type:     strings.TrimSpace(value.Type),
		Provider: strings.TrimSpace(value.Provider),
		ID:       strings.TrimSpace(value.ID),
	}
	if normalized.Type == "" && normalized.Provider == "" && normalized.ID == "" {
		return nil
	}
	return normalized
}

func validateSpritzAgentRef(value *spritzv1.SpritzAgentRef) error {
	normalized := normalizeSpritzAgentRef(value)
	if normalized == nil {
		return nil
	}
	if normalized.Type == "" {
		return errors.New("spec.agentRef.type is required")
	}
	if normalized.Provider == "" {
		return errors.New("spec.agentRef.provider is required")
	}
	if normalized.ID == "" {
		return errors.New("spec.agentRef.id is required")
	}
	return nil
}

func sameSpritzAgentRef(left, right *spritzv1.SpritzAgentRef) bool {
	left = normalizeSpritzAgentRef(left)
	right = normalizeSpritzAgentRef(right)
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return left.Type == right.Type && left.Provider == right.Provider && left.ID == right.ID
	}
}

func mergeSpritzAgentRefStrict(existing, resolved *spritzv1.SpritzAgentRef) (*spritzv1.SpritzAgentRef, error) {
	resolved = normalizeSpritzAgentRef(resolved)
	if resolved == nil {
		return normalizeSpritzAgentRef(existing), nil
	}
	if err := validateSpritzAgentRef(resolved); err != nil {
		return nil, err
	}
	existing = normalizeSpritzAgentRef(existing)
	if existing != nil && !sameSpritzAgentRef(existing, resolved) {
		return nil, errors.New("preset create resolver attempted to overwrite spec.agentRef")
	}
	return resolved, nil
}

func normalizeSpritzAgentProfile(value *spritzv1.SpritzAgentProfile) *spritzv1.SpritzAgentProfile {
	if value == nil {
		return nil
	}
	normalized := &spritzv1.SpritzAgentProfile{
		Name:     strings.TrimSpace(value.Name),
		ImageURL: strings.TrimSpace(value.ImageURL),
	}
	if normalized.Name == "" && normalized.ImageURL == "" {
		return nil
	}
	return normalized
}

func buildSpritzAgentProfileStatus(
	overrides *spritzv1.SpritzAgentProfile,
	synced *spritzv1.SpritzAgentProfile,
	generation int64,
	syncer string,
	syncedAt *metav1.Time,
	lastError string,
) *spritzv1.SpritzAgentProfileStatus {
	overrides = normalizeSpritzAgentProfile(overrides)
	synced = normalizeSpritzAgentProfile(synced)
	lastError = strings.TrimSpace(lastError)

	status := &spritzv1.SpritzAgentProfileStatus{
		ObservedGeneration: generation,
		Syncer:             strings.TrimSpace(syncer),
		LastError:          lastError,
	}

	if overrides != nil {
		status.Name = overrides.Name
		status.ImageURL = overrides.ImageURL
		status.Source = "override"
	}
	if synced != nil {
		if status.Name == "" {
			status.Name = synced.Name
		}
		if status.ImageURL == "" {
			status.ImageURL = synced.ImageURL
		}
		if status.Source == "" {
			status.Source = "synced"
		}
	}
	if syncedAt != nil {
		status.LastSyncedAt = syncedAt.DeepCopy()
	}
	if status.Name == "" && status.ImageURL == "" && status.LastError == "" {
		return nil
	}
	return status
}

func copySpritzAgentProfileStatus(value *spritzv1.SpritzAgentProfileStatus) *spritzv1.SpritzAgentProfileStatus {
	if value == nil {
		return nil
	}
	copied := *value
	if value.LastSyncedAt != nil {
		copied.LastSyncedAt = value.LastSyncedAt.DeepCopy()
	}
	return &copied
}

func currentSpritzStatusProfile(spritz *spritzv1.Spritz) *spritzv1.SpritzAgentProfileStatus {
	if spritz == nil || spritz.Status.Profile == nil {
		return nil
	}
	return spritz.Status.Profile
}

func parseAgentProfileSyncOutput(raw []byte) (*spritzv1.SpritzAgentProfile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload agentProfileSyncOutput
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid agent profile sync output: %w", err)
	}
	return normalizeSpritzAgentProfile(payload.Profile), nil
}

func agentProfileSyncErrorMessage(status extensionResolverStatus) string {
	switch status {
	case extensionStatusUnresolved:
		return "agent profile is unresolved"
	case extensionStatusForbidden:
		return "agent profile sync is forbidden"
	case extensionStatusAmbiguous:
		return "agent profile sync is ambiguous"
	case extensionStatusInvalid:
		return "agent profile sync is invalid"
	case extensionStatusUnavailable:
		return "agent profile sync is unavailable"
	default:
		return ""
	}
}

func createAgentProfileRequestContext(namespace string, body *createRequest) extensionRequestContext {
	requestContext := extensionRequestContext{
		Namespace: strings.TrimSpace(namespace),
	}
	if body == nil {
		return requestContext
	}
	requestContext.PresetID = strings.TrimSpace(body.PresetID)
	if body.Annotations != nil {
		requestContext.InstanceClassID = strings.TrimSpace(body.Annotations[instanceClassAnnotationKey])
	}
	return requestContext
}

func (s *server) resolveAgentProfile(
	ctx context.Context,
	principal principal,
	namespace string,
	body *createRequest,
) *resolvedAgentProfile {
	if body == nil {
		return nil
	}
	body.Spec.AgentRef = normalizeSpritzAgentRef(body.Spec.AgentRef)
	body.Spec.ProfileOverrides = normalizeSpritzAgentProfile(body.Spec.ProfileOverrides)
	if body.Spec.AgentRef == nil && body.Spec.ProfileOverrides == nil {
		return nil
	}
	if body.Spec.AgentRef == nil {
		return &resolvedAgentProfile{}
	}
	if body.Spec.ProfileOverrides != nil && body.Spec.ProfileOverrides.Name != "" && body.Spec.ProfileOverrides.ImageURL != "" {
		return &resolvedAgentProfile{}
	}

	requestContext := createAgentProfileRequestContext(namespace, body)
	resolver, response, err := s.extensions.resolve(
		ctx,
		extensionOperationAgentProfileSync,
		principal,
		body.RequestID,
		requestContext,
		agentProfileSyncInput{
			Owner:            body.Spec.Owner,
			AgentRef:         body.Spec.AgentRef,
			ProfileOverrides: body.Spec.ProfileOverrides,
		},
	)
	if err != nil {
		lastError := fmt.Sprintf("agent profile sync failed: %v", err)
		if resolver != nil {
			return &resolvedAgentProfile{
				syncer:    resolver.id,
				lastError: lastError,
			}
		}
		return &resolvedAgentProfile{lastError: lastError}
	}
	if resolver == nil {
		return &resolvedAgentProfile{}
	}

	result := &resolvedAgentProfile{syncer: resolver.id}
	switch response.Status {
	case "", extensionStatusResolved:
		profile, parseErr := parseAgentProfileSyncOutput(response.Output)
		if parseErr != nil {
			result.lastError = parseErr.Error()
			return result
		}
		result.profile = profile
		now := metav1.Now()
		result.syncedAt = &now
	default:
		result.lastError = agentProfileSyncErrorMessage(response.Status)
	}
	return result
}

func (s *server) applyResolvedAgentProfileStatus(
	ctx context.Context,
	spritz *spritzv1.Spritz,
	resolved *resolvedAgentProfile,
) (*spritzv1.Spritz, error) {
	if spritz == nil {
		return nil, nil
	}
	var statusProfile *spritzv1.SpritzAgentProfileStatus
	if resolved != nil {
		statusProfile = buildSpritzAgentProfileStatus(
			spritz.Spec.ProfileOverrides,
			resolved.profile,
			spritz.Generation,
			resolved.syncer,
			resolved.syncedAt,
			resolved.lastError,
		)
	} else {
		statusProfile = buildSpritzAgentProfileStatus(
			spritz.Spec.ProfileOverrides,
			nil,
			spritz.Generation,
			"",
			nil,
			"",
		)
	}
	if statusProfile == nil {
		return spritz, nil
	}
	objectKey := client.ObjectKeyFromObject(spritz)
	updated := spritz.DeepCopy()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &spritzv1.Spritz{}
		if err := s.client.Get(ctx, objectKey, current); err != nil {
			return err
		}
		if apiequality.Semantic.DeepEqual(current.Status.Profile, statusProfile) {
			updated = current
			return nil
		}
		current.Status.Profile = copySpritzAgentProfileStatus(statusProfile)
		if err := s.client.Status().Update(ctx, current); err != nil {
			return err
		}
		updated = current
		return nil
	}); err != nil {
		return spritz, err
	}
	return updated, nil
}
