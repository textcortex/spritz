package main

import (
	"errors"
	"strings"

	spritzv1 "spritz.sh/operator/api/v1"
)

func normalizeSpritzRuntimePolicy(
	value *spritzv1.SpritzRuntimePolicy,
) *spritzv1.SpritzRuntimePolicy {
	if value == nil {
		return nil
	}
	normalized := &spritzv1.SpritzRuntimePolicy{
		NetworkProfile:  strings.TrimSpace(value.NetworkProfile),
		MountProfile:    strings.TrimSpace(value.MountProfile),
		ExposureProfile: strings.TrimSpace(value.ExposureProfile),
		Revision:        strings.TrimSpace(value.Revision),
	}
	if normalized.NetworkProfile == "" &&
		normalized.MountProfile == "" &&
		normalized.ExposureProfile == "" &&
		normalized.Revision == "" {
		return nil
	}
	return normalized
}

func validateSpritzRuntimePolicy(
	value *spritzv1.SpritzRuntimePolicy,
) error {
	normalized := normalizeSpritzRuntimePolicy(value)
	if normalized == nil {
		return nil
	}
	if normalized.NetworkProfile == "" {
		return errors.New("spec.runtimePolicy.networkProfile is required")
	}
	if normalized.MountProfile == "" {
		return errors.New("spec.runtimePolicy.mountProfile is required")
	}
	if normalized.ExposureProfile == "" {
		return errors.New("spec.runtimePolicy.exposureProfile is required")
	}
	if normalized.Revision == "" {
		return errors.New("spec.runtimePolicy.revision is required")
	}
	return nil
}

func sameSpritzRuntimePolicy(
	left *spritzv1.SpritzRuntimePolicy,
	right *spritzv1.SpritzRuntimePolicy,
) bool {
	left = normalizeSpritzRuntimePolicy(left)
	right = normalizeSpritzRuntimePolicy(right)
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return left.NetworkProfile == right.NetworkProfile &&
			left.MountProfile == right.MountProfile &&
			left.ExposureProfile == right.ExposureProfile &&
			left.Revision == right.Revision
	}
}

func mergeSpritzRuntimePolicyStrict(
	existing *spritzv1.SpritzRuntimePolicy,
	resolved *spritzv1.SpritzRuntimePolicy,
) (*spritzv1.SpritzRuntimePolicy, error) {
	resolved = normalizeSpritzRuntimePolicy(resolved)
	if resolved == nil {
		return normalizeSpritzRuntimePolicy(existing), nil
	}
	if err := validateSpritzRuntimePolicy(resolved); err != nil {
		return nil, err
	}
	existing = normalizeSpritzRuntimePolicy(existing)
	if existing != nil && !sameSpritzRuntimePolicy(existing, resolved) {
		return nil, errors.New("preset create resolver attempted to overwrite spec.runtimePolicy")
	}
	return resolved, nil
}
