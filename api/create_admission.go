package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("presetInputs must be valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("presetInputs must be valid JSON")
	}
	objectValue, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("presetInputs must be a JSON object")
	}
	normalized, err := marshalCanonicalPresetInputValue(objectValue)
	if err != nil {
		return nil, errors.New("presetInputs must be valid JSON")
	}
	return normalized, nil
}

func marshalCanonicalPresetInputValue(value any) (json.RawMessage, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var buffer bytes.Buffer
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return nil, err
			}
			buffer.Write(encodedKey)
			buffer.WriteByte(':')
			encodedValue, err := marshalCanonicalPresetInputValue(typed[key])
			if err != nil {
				return nil, err
			}
			buffer.Write(encodedValue)
		}
		buffer.WriteByte('}')
		return buffer.Bytes(), nil
	case []any:
		var buffer bytes.Buffer
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encodedValue, err := marshalCanonicalPresetInputValue(item)
			if err != nil {
				return nil, err
			}
			buffer.Write(encodedValue)
		}
		buffer.WriteByte(']')
		return buffer.Bytes(), nil
	case json.Number:
		return []byte(typed.String()), nil
	case string, bool, nil:
		return json.Marshal(typed)
	default:
		return json.Marshal(typed)
	}
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
	requestedServiceAccount := strings.TrimSpace(body.Spec.ServiceAccountName)
	if selectedClass != nil {
		requestContext.InstanceClassID = selectedClass.ID
	}
	serviceAccountResolved := false
	runtimePolicyResolved := false
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
		var mutationResult presetCreateMutationResult
		mutationResult, err = applyPresetCreateResolverMutations(body, response)
		if err != nil {
			return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
		}
		serviceAccountResolved = mutationResult.serviceAccountResolved
		runtimePolicyResolved = mutationResult.runtimePolicyResolved
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
		if selectedClass.requiresResolvedField(requiredResolvedFieldServiceAccountName) && requestedServiceAccount != "" && !serviceAccountResolved {
			err := fmt.Errorf("instance class %q requires resolver-produced field %q", selectedClass.ID, requiredResolvedFieldServiceAccountName)
			return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
		}
		if selectedClass.requiresResolvedField(requiredResolvedFieldRuntimePolicy) && normalizeSpritzRuntimePolicy(body.Spec.RuntimePolicy) != nil && !runtimePolicyResolved {
			err := fmt.Errorf("instance class %q requires resolver-produced field %q", selectedClass.ID, requiredResolvedFieldRuntimePolicy)
			return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
		}
		if err := selectedClass.validateResolvedCreate(body); err != nil {
			return newAdmissionError(http.StatusBadRequest, err.Error(), nil, err)
		}
	}
	return nil
}

type presetCreateMutationResult struct {
	serviceAccountResolved bool
	runtimePolicyResolved  bool
}

func applyPresetCreateResolverMutations(body *createRequest, response extensionResolverResponseEnvelope) (presetCreateMutationResult, error) {
	if body == nil {
		return presetCreateMutationResult{}, nil
	}
	result := presetCreateMutationResult{}
	if response.Mutations.Spec != nil {
		resolvedServiceAccount := strings.TrimSpace(response.Mutations.Spec.ServiceAccountName)
		if resolvedServiceAccount != "" {
			if current := strings.TrimSpace(body.Spec.ServiceAccountName); current != "" && current != resolvedServiceAccount {
				return presetCreateMutationResult{}, errors.New("preset create resolver attempted to overwrite spec.serviceAccountName")
			}
			body.Spec.ServiceAccountName = resolvedServiceAccount
			result.serviceAccountResolved = true
		}
		resolvedRuntimePolicy := normalizeSpritzRuntimePolicy(
			response.Mutations.Spec.RuntimePolicy,
		)
		mergedRuntimePolicy, err := mergeSpritzRuntimePolicyStrict(
			body.Spec.RuntimePolicy,
			resolvedRuntimePolicy,
		)
		if err != nil {
			return presetCreateMutationResult{}, err
		}
		body.Spec.RuntimePolicy = mergedRuntimePolicy
		if resolvedRuntimePolicy != nil {
			result.runtimePolicyResolved = true
		}
		mergedAgentRef, err := mergeSpritzAgentRefStrict(body.Spec.AgentRef, response.Mutations.Spec.AgentRef)
		if err != nil {
			return presetCreateMutationResult{}, err
		}
		body.Spec.AgentRef = mergedAgentRef
		specAnnotations, err := mergeMetadataStrict(body.Spec.Annotations, response.Mutations.Spec.Annotations, "spec annotation")
		if err != nil {
			return presetCreateMutationResult{}, err
		}
		body.Spec.Annotations = specAnnotations
		specLabels, err := mergeMetadataStrict(body.Spec.Labels, response.Mutations.Spec.Labels, "spec label")
		if err != nil {
			return presetCreateMutationResult{}, err
		}
		body.Spec.Labels = specLabels
	}
	annotations, err := mergeMetadataStrict(body.Annotations, response.Mutations.Annotations, "annotation")
	if err != nil {
		return presetCreateMutationResult{}, err
	}
	body.Annotations = annotations
	labels, err := mergeMetadataStrict(body.Labels, response.Mutations.Labels, "label")
	if err != nil {
		return presetCreateMutationResult{}, err
	}
	body.Labels = labels
	return result, nil
}
