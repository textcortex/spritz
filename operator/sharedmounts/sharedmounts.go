package sharedmounts

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

const (
	ModeReadOnly = "read-only"
	ModeSnapshot = "snapshot"

	SyncPoll   = "poll"
	SyncManual = "manual"

	ScopeOwner   = "owner"
	ScopeOrg     = "org"
	ScopeProject = "project"
	ScopeSpritz  = "spritz"
)

type MountSpec struct {
	Name           string `json:"name"`
	Scope          string `json:"scope,omitempty"`
	MountPath      string `json:"mountPath"`
	Mode           string `json:"mode,omitempty"`
	SyncMode       string `json:"syncMode,omitempty"`
	PollSeconds    int    `json:"pollSeconds,omitempty"`
	PublishSeconds int    `json:"publishSeconds,omitempty"`
}

type LatestManifest struct {
	Revision  string `json:"revision"`
	Checksum  string `json:"checksum"`
	UpdatedAt string `json:"updated_at"`
}

func ParseMountsJSON(raw string) ([]MountSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var mounts []MountSpec
	if err := json.Unmarshal([]byte(raw), &mounts); err != nil {
		return nil, fmt.Errorf("invalid shared mounts json: %w", err)
	}
	for i := range mounts {
		mounts[i] = NormalizeMount(mounts[i])
	}
	return mounts, nil
}

func NormalizeMount(mount MountSpec) MountSpec {
	scope := strings.TrimSpace(strings.ToLower(mount.Scope))
	if scope == "" {
		scope = ScopeOwner
	}
	mount.Scope = scope
	mode := strings.TrimSpace(strings.ToLower(mount.Mode))
	if mode == "" {
		mode = ModeReadOnly
	}
	if mode != ModeReadOnly && mode != ModeSnapshot {
		mode = ModeReadOnly
	}
	syncMode := strings.TrimSpace(strings.ToLower(mount.SyncMode))
	if syncMode == "" {
		if mode == ModeSnapshot {
			syncMode = SyncManual
		} else {
			syncMode = SyncPoll
		}
	}
	if mount.PollSeconds <= 0 && syncMode == SyncPoll {
		mount.PollSeconds = 30
	}
	if mount.PublishSeconds <= 0 && mode == ModeSnapshot {
		mount.PublishSeconds = 60
	}
	mount.Mode = mode
	mount.SyncMode = syncMode
	return mount
}

func NormalizeMounts(mounts []MountSpec) []MountSpec {
	if len(mounts) == 0 {
		return nil
	}
	normalized := make([]MountSpec, len(mounts))
	for i := range mounts {
		normalized[i] = NormalizeMount(mounts[i])
	}
	return normalized
}

func ValidateName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("mount name is required")
	}
	if trimmed == "." || trimmed == ".." {
		return fmt.Errorf("mount name must not be '.' or '..'")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return fmt.Errorf("mount name must not contain slashes: %s", name)
	}
	return nil
}

func ValidateRevision(revision string) error {
	trimmed := strings.TrimSpace(revision)
	if trimmed == "" {
		return fmt.Errorf("revision is required")
	}
	if trimmed == "." || trimmed == ".." {
		return fmt.Errorf("revision must not be '.' or '..'")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return fmt.Errorf("revision must not contain slashes: %s", revision)
	}
	return nil
}

func ValidateScope(scope string) error {
	switch strings.TrimSpace(strings.ToLower(scope)) {
	case ScopeOwner, ScopeOrg, ScopeProject, ScopeSpritz:
		return nil
	default:
		return fmt.Errorf("invalid scope: %s", scope)
	}
}

func ValidateMountPath(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("mount path is required")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return fmt.Errorf("mount path must be absolute: %s", trimmed)
	}
	cleaned := strings.TrimRight(trimmed, "/")
	if cleaned == "" {
		return fmt.Errorf("mount path must not be root: %s", trimmed)
	}
	return nil
}

func ValidateMounts(mounts []MountSpec) error {
	if len(mounts) == 0 {
		return nil
	}
	seenNames := map[string]bool{}
	paths := []string{}
	seenPaths := map[string]bool{}
	for _, mount := range mounts {
		if err := ValidateName(mount.Name); err != nil {
			return err
		}
		if err := ValidateScope(mount.Scope); err != nil {
			return err
		}
		if mount.Mode == ModeSnapshot && mount.SyncMode == SyncPoll {
			return fmt.Errorf("snapshot mounts do not support syncMode=poll")
		}
		if err := ValidateMountPath(mount.MountPath); err != nil {
			return err
		}
		if seenNames[mount.Name] {
			return fmt.Errorf("duplicate shared mount name: %s", mount.Name)
		}
		seenNames[mount.Name] = true
		cleaned := strings.TrimRight(strings.TrimSpace(mount.MountPath), "/")
		if !seenPaths[cleaned] {
			paths = append(paths, cleaned)
			seenPaths[cleaned] = true
		}
	}
	for i, base := range paths {
		for j, other := range paths {
			if i == j {
				continue
			}
			if pathHasPrefix(other, base) {
				return fmt.Errorf("shared mount paths overlap: %s and %s", base, other)
			}
		}
	}
	return nil
}

func pathHasPrefix(value, prefix string) bool {
	if value == prefix {
		return true
	}
	return strings.HasPrefix(value, prefix+string('/'))
}

func ValidateScopeID(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("scope id is required")
	}
	if trimmed == "." || trimmed == ".." {
		return fmt.Errorf("scope id must not be '.' or '..'")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return fmt.Errorf("scope id must not contain slashes: %s", value)
	}
	return nil
}

func ValidateUpdatedAt(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("updated_at is required")
	}
	if _, err := time.Parse(time.RFC3339, trimmed); err != nil {
		return fmt.Errorf("invalid updated_at: %w", err)
	}
	return nil
}

func StoragePrefix(basePrefix, scope, scopeID, mount string) string {
	return path.Join(basePrefix, scope, scopeID, mount)
}
