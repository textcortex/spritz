package main

import (
	"fmt"

	"spritz.sh/operator/sharedmounts"
)

func normalizeSharedMounts(mounts []sharedmounts.MountSpec) ([]sharedmounts.MountSpec, error) {
	if len(mounts) == 0 {
		return nil, nil
	}
	normalized := sharedmounts.NormalizeMounts(mounts)
	if err := sharedmounts.ValidateMounts(normalized); err != nil {
		return nil, err
	}
	for _, mount := range normalized {
		if mount.Scope != sharedmounts.ScopeOwner {
			return nil, fmt.Errorf("unsupported shared mount scope: %s", mount.Scope)
		}
	}
	return normalized, nil
}
