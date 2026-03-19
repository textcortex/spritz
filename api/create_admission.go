package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	spritzv1 "spritz.sh/operator/api/v1"
)

type presetCreateResolveInput struct {
	Owner        spritzv1.SpritzOwner `json:"owner"`
	OwnerRef     *ownerRef            `json:"ownerRef,omitempty"`
	PresetInputs json.RawMessage      `json:"presetInputs,omitempty"`
	Spec         spritzv1.SpritzSpec  `json:"spec"`
}

func normalizePresetInputs(raw json.RawMessage) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("presetInputs must be valid JSON")
	}
	if value == nil {
		return nil, nil
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("presetInputs must be a JSON object")
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("presetInputs must be valid JSON")
	}
	return normalized, nil
}

func mergeMetadataStrict(existing, resolved map[string]string, fieldName string) (map[string]string, error) {
	if len(resolved) == 0 {
		return existing, nil
	}
	if existing == nil {
		existing = map[string]string{}
	}
	for key, value := range resolved {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return nil, fmt.Errorf("%s keys must not be empty", fieldName)
		}
		if current, ok := existing[normalizedKey]; ok && current != value {
			return nil, fmt.Errorf("resolver attempted to overwrite %s %q", fieldName, normalizedKey)
		}
		existing[normalizedKey] = value
	}
	return existing, nil
}

func (s *server) resolveCreateAdmission(ctx context.Context, principal principal, namespace string, body *createRequest) error {
	if body == nil {
		return nil
	}
	if body.PresetInputs != nil && strings.TrimSpace(body.PresetID) == "" {
		return newAdmissionError(http.StatusBadRequest, "presetInputs requires presetId", nil, errors.New("presetInputs requires presetId"))
	}

	var selectedClass *instanceClass
	if preset, ok := s.presets.get(body.PresetID); ok && strings.TrimSpace(preset.InstanceClass) != "" {
		instanceClass, found := s.instanceClasses.get(preset.InstanceClass)
		if !found {
			return newAdmissionError(http.StatusInternalServerError, "instance class is not configured", nil, fmt.Errorf("preset %q references unknown instance class %q", preset.ID, preset.InstanceClass))
		}
		selectedClass = instanceClass
		if selectedClass.Version != "" {
			annotations, err := mergeMetadataStrict(body.Annotations, map[string]string{
				instanceClassAnnotationKey:        selectedClass.ID,
				instanceClassVersionAnnotationKey: selectedClass.Version,
			}, "annotation")
			if err != nil {
				return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
			}
			body.Annotations = annotations
		} else {
			annotations, err := mergeMetadataStrict(body.Annotations, map[string]string{
				instanceClassAnnotationKey: selectedClass.ID,
			}, "annotation")
			if err != nil {
				return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
			}
			body.Annotations = annotations
		}
	}

	requestContext := extensionRequestContext{
		Namespace: namespace,
		PresetID:  body.PresetID,
	}
	if selectedClass != nil {
		requestContext.InstanceClassID = selectedClass.ID
	}
	resolver, response, err := s.extensions.resolve(
		ctx,
		extensionOperationPresetCreateResolve,
		principal,
		body.RequestID,
		requestContext,
		presetCreateResolveInput{
			Owner:        body.Spec.Owner,
			OwnerRef:     body.OwnerRef,
			PresetInputs: body.PresetInputs,
			Spec:         body.Spec,
		},
	)
	if err != nil {
		return newAdmissionError(http.StatusInternalServerError, "create resolver failed", nil, err)
	}
	if resolver == nil {
		if body.PresetInputs != nil {
			return newAdmissionError(http.StatusBadRequest, "presetInputs require a matching preset create resolver", nil, errors.New("presetInputs require a matching preset create resolver"))
		}
	} else {
		if err := applyPresetCreateResolverMutations(body, response); err != nil {
			return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
		}
		switch response.Status {
		case "", extensionStatusResolved:
		case extensionStatusUnresolved:
			return newAdmissionError(http.StatusConflict, "preset inputs are unresolved", map[string]any{"error": "preset_create_unresolved"}, errors.New("preset inputs are unresolved"))
		case extensionStatusForbidden:
			return newAdmissionError(http.StatusForbidden, "preset create resolution is forbidden", map[string]any{"error": "preset_create_forbidden"}, errors.New("preset create resolution is forbidden"))
		case extensionStatusAmbiguous:
			return newAdmissionError(http.StatusConflict, "preset inputs are ambiguous", map[string]any{"error": "preset_create_ambiguous"}, errors.New("preset inputs are ambiguous"))
		case extensionStatusInvalid:
			return newAdmissionError(http.StatusBadRequest, "preset inputs are invalid", map[string]any{"error": "preset_create_invalid"}, errors.New("preset inputs are invalid"))
		case extensionStatusUnavailable:
			return newAdmissionError(http.StatusServiceUnavailable, "preset create resolution is unavailable", map[string]any{"error": "preset_create_unavailable"}, errors.New("preset create resolution is unavailable"))
		default:
			return newAdmissionError(http.StatusServiceUnavailable, "preset create resolution returned an unsupported status", nil, fmt.Errorf("unsupported preset create status %q", response.Status))
		}
	}
	if selectedClass != nil {
		if err := selectedClass.validateResolvedCreate(body); err != nil {
			return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
		}
	}
	return nil
}

func applyPresetCreateResolverMutations(body *createRequest, response extensionResolverResponseEnvelope) error {
	if body == nil {
		return nil
	}
	if response.Mutations.OwnerID != "" {
		return errors.New("preset create resolver may not mutate ownerId")
	}
	if response.Mutations.Spec != nil {
		resolvedServiceAccount := strings.TrimSpace(response.Mutations.Spec.ServiceAccountName)
		if resolvedServiceAccount != "" {
			if current := strings.TrimSpace(body.Spec.ServiceAccountName); current != "" && current != resolvedServiceAccount {
				return errors.New("preset create resolver attempted to overwrite spec.serviceAccountName")
			}
			body.Spec.ServiceAccountName = resolvedServiceAccount
		}
	}
	annotations, err := mergeMetadataStrict(body.Annotations, response.Mutations.Annotations, "annotation")
	if err != nil {
		return err
	}
	body.Annotations = annotations
	labels, err := mergeMetadataStrict(body.Labels, response.Mutations.Labels, "label")
	if err != nil {
		return err
	}
	body.Labels = labels
	return nil
}
