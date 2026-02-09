package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	spritzv1 "spritz.sh/operator/api/v1"
	"spritz.sh/operator/sharedmounts"
)

const userConfigAnnotationKey = "spritz.sh/user-config"

type userConfigPayload struct {
	Image        *string                      `json:"image,omitempty"`
	Repo         *spritzv1.SpritzRepo         `json:"repo,omitempty"`
	Env          *[]corev1.EnvVar             `json:"env,omitempty"`
	TTL          *string                      `json:"ttl,omitempty"`
	Resources    *corev1.ResourceRequirements `json:"resources,omitempty"`
	SharedMounts *[]sharedmounts.MountSpec    `json:"sharedMounts,omitempty"`
}

type userConfigPolicy struct {
	allowImage         bool
	allowedImagePaths  []string
	allowRepo          bool
	allowTTL           bool
	allowEnv           bool
	allowResources     bool
	allowSharedMounts  bool
	allowedEnvKeys     map[string]struct{}
	allowedEnvPrefixes []string
	allowedMountRoots  []string
	maxTTL             time.Duration
}

func newUserConfigPolicy() userConfigPolicy {
	return userConfigPolicy{
		allowImage:         parseBoolEnv("SPRITZ_USER_CONFIG_ALLOW_IMAGE", false),
		allowedImagePaths:  splitList(envOrDefault("SPRITZ_USER_CONFIG_ALLOWED_IMAGE_PREFIXES", "")),
		allowRepo:          parseBoolEnv("SPRITZ_USER_CONFIG_ALLOW_REPO", true),
		allowTTL:           parseBoolEnv("SPRITZ_USER_CONFIG_ALLOW_TTL", true),
		allowEnv:           parseBoolEnv("SPRITZ_USER_CONFIG_ALLOW_ENV", false),
		allowResources:     parseBoolEnv("SPRITZ_USER_CONFIG_ALLOW_RESOURCES", false),
		allowSharedMounts:  parseBoolEnv("SPRITZ_USER_CONFIG_ALLOW_SHARED_MOUNTS", true),
		allowedEnvKeys:     splitSet(envOrDefault("SPRITZ_USER_CONFIG_ALLOWED_ENV_KEYS", "")),
		allowedEnvPrefixes: splitList(envOrDefault("SPRITZ_USER_CONFIG_ALLOWED_ENV_PREFIXES", "")),
		allowedMountRoots: splitListOrDefault(
			envOrDefault("SPRITZ_USER_CONFIG_ALLOWED_MOUNT_ROOTS", ""),
			[]string{"/home/dev", "/workspace"},
		),
		maxTTL: parseDurationEnv("SPRITZ_USER_CONFIG_MAX_TTL", 0),
	}
}

func parseUserConfig(raw []byte) (map[string]json.RawMessage, userConfigPayload, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, userConfigPayload{}, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, userConfigPayload{}, nil
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &rawMap); err != nil {
		return nil, userConfigPayload{}, fmt.Errorf("userConfig must be a JSON object")
	}
	if err := validateUserConfigKeys(rawMap); err != nil {
		return nil, userConfigPayload{}, err
	}
	if len(rawMap) == 0 {
		return rawMap, userConfigPayload{}, nil
	}
	var payload userConfigPayload
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, userConfigPayload{}, fmt.Errorf("invalid userConfig: %w", err)
	}
	return rawMap, payload, nil
}

func validateUserConfigKeys(raw map[string]json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	for key := range raw {
		switch key {
		case "image", "repo", "env", "ttl", "resources", "sharedMounts":
			continue
		default:
			return fmt.Errorf("unsupported userConfig field: %s", key)
		}
	}
	return nil
}

func normalizeUserConfig(policy userConfigPolicy, keys map[string]json.RawMessage, cfg userConfigPayload) (userConfigPayload, error) {
	if keys == nil || len(keys) == 0 {
		return cfg, nil
	}
	if _, ok := keys["image"]; ok && !policy.allowImage {
		return cfg, fmt.Errorf("userConfig.image is not allowed")
	}
	if _, ok := keys["repo"]; ok && !policy.allowRepo {
		return cfg, fmt.Errorf("userConfig.repo is not allowed")
	}
	if _, ok := keys["ttl"]; ok && !policy.allowTTL {
		return cfg, fmt.Errorf("userConfig.ttl is not allowed")
	}
	if _, ok := keys["env"]; ok && !policy.allowEnv {
		return cfg, fmt.Errorf("userConfig.env is not allowed")
	}
	if _, ok := keys["resources"]; ok && !policy.allowResources {
		return cfg, fmt.Errorf("userConfig.resources is not allowed")
	}
	if _, ok := keys["sharedMounts"]; ok && !policy.allowSharedMounts {
		return cfg, fmt.Errorf("userConfig.sharedMounts is not allowed")
	}

	if _, ok := keys["image"]; ok && cfg.Image != nil && *cfg.Image != "" {
		if len(policy.allowedImagePaths) > 0 && !matchesAnyPrefix(*cfg.Image, policy.allowedImagePaths) {
			return cfg, fmt.Errorf("userConfig.image is not allowed: %s", *cfg.Image)
		}
	}

	if _, ok := keys["repo"]; ok && cfg.Repo != nil {
		if cfg.Repo.URL == "" {
			return cfg, fmt.Errorf("userConfig.repo.url is required")
		}
		if err := validateRepoDir(cfg.Repo.Dir); err != nil {
			return cfg, err
		}
	}

	if _, ok := keys["ttl"]; ok && cfg.TTL != nil && *cfg.TTL != "" {
		parsed, err := time.ParseDuration(*cfg.TTL)
		if err != nil {
			return cfg, fmt.Errorf("userConfig.ttl must be a duration like 8h or 30m")
		}
		if policy.maxTTL > 0 && parsed > policy.maxTTL {
			return cfg, fmt.Errorf("userConfig.ttl exceeds max ttl of %s", policy.maxTTL)
		}
	}

	if _, ok := keys["env"]; ok && cfg.Env != nil {
		if err := validateUserEnvVars(*cfg.Env, policy.allowedEnvKeys, policy.allowedEnvPrefixes); err != nil {
			return cfg, err
		}
	}

	if _, ok := keys["sharedMounts"]; ok && cfg.SharedMounts != nil && len(*cfg.SharedMounts) > 0 {
		normalized, err := normalizeSharedMountsForUser(*cfg.SharedMounts, policy.allowedMountRoots)
		if err != nil {
			return cfg, err
		}
		cfg.SharedMounts = &normalized
	}

	return cfg, nil
}

func applyUserConfig(spec *spritzv1.SpritzSpec, keys map[string]json.RawMessage, cfg userConfigPayload) {
	if keys == nil || len(keys) == 0 {
		return
	}
	if _, ok := keys["image"]; ok {
		if cfg.Image == nil {
			spec.Image = ""
		} else {
			spec.Image = *cfg.Image
		}
	}
	if _, ok := keys["repo"]; ok {
		spec.Repo = cfg.Repo
		if cfg.Repo != nil {
			spec.Repos = nil
		}
	}
	if _, ok := keys["env"]; ok {
		if cfg.Env == nil {
			spec.Env = nil
		} else {
			spec.Env = *cfg.Env
		}
	}
	if _, ok := keys["ttl"]; ok {
		if cfg.TTL == nil {
			spec.TTL = ""
		} else {
			spec.TTL = *cfg.TTL
		}
	}
	if _, ok := keys["resources"]; ok {
		if cfg.Resources == nil {
			spec.Resources = corev1.ResourceRequirements{}
		} else {
			spec.Resources = *cfg.Resources
		}
	}
	if _, ok := keys["sharedMounts"]; ok {
		if cfg.SharedMounts == nil {
			spec.SharedMounts = nil
		} else {
			spec.SharedMounts = *cfg.SharedMounts
		}
	}
}

func encodeUserConfig(keys map[string]json.RawMessage, cfg userConfigPayload) (string, error) {
	if keys == nil || len(keys) == 0 {
		return "", nil
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func normalizeSharedMountsForUser(mounts []sharedmounts.MountSpec, allowedRoots []string) ([]sharedmounts.MountSpec, error) {
	normalized, err := normalizeSharedMounts(mounts)
	if err != nil {
		return nil, err
	}
	if len(allowedRoots) == 0 {
		return normalized, nil
	}
	for _, mount := range normalized {
		if !isAllowedMountPath(mount.MountPath, allowedRoots) {
			return nil, fmt.Errorf("shared mount path is not allowed: %s", mount.MountPath)
		}
	}
	return normalized, nil
}

func isAllowedMountPath(value string, roots []string) bool {
	cleaned := strings.TrimRight(path.Clean(value), "/")
	if cleaned == "" || cleaned == "." {
		return false
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		rootClean := strings.TrimRight(path.Clean(root), "/")
		if rootClean == "" || rootClean == "." {
			continue
		}
		if cleaned == rootClean || strings.HasPrefix(cleaned, rootClean+string('/')) {
			return true
		}
	}
	return false
}

func validateUserEnvVars(env []corev1.EnvVar, allowedKeys map[string]struct{}, allowedPrefixes []string) error {
	for _, item := range env {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return fmt.Errorf("env name is required")
		}
		if len(allowedKeys) == 0 && len(allowedPrefixes) == 0 {
			continue
		}
		if _, ok := allowedKeys[name]; ok {
			continue
		}
		if matchesAnyPrefix(name, allowedPrefixes) {
			continue
		}
		return fmt.Errorf("env %s is not allowed", name)
	}
	return nil
}

func matchesAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
