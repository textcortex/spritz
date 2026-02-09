package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"spritz.sh/operator/sharedmounts"
)

type sharedMountsConfig struct {
	enabled          bool
	prefix           string
	rcloneRemote     string
	rcloneConfigPath string
	bucket           string
	mounts           map[string]sharedmounts.MountSpec
	maxBundleBytes   int64
}

func newSharedMountsConfig() (sharedMountsConfig, error) {
	rawMounts := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS"))
	mounts, err := sharedmounts.ParseMountsJSON(rawMounts)
	if err != nil {
		return sharedMountsConfig{}, err
	}
	allowed := map[string]sharedmounts.MountSpec{}
	if err := sharedmounts.ValidateMounts(mounts); err != nil {
		return sharedMountsConfig{}, err
	}
	for _, mount := range mounts {
		if mount.Scope != sharedmounts.ScopeOwner {
			return sharedMountsConfig{}, fmt.Errorf("unsupported shared mount scope: %s", mount.Scope)
		}
		allowed[mount.Name] = mount
	}
	remote := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_RCLONE_REMOTE"))
	bucket := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_BUCKET"))
	enabled := remote != "" || bucket != "" || len(mounts) > 0
	if !enabled {
		return sharedMountsConfig{enabled: false}, nil
	}
	if remote == "" {
		return sharedMountsConfig{}, fmt.Errorf("SPRITZ_SHARED_MOUNTS_RCLONE_REMOTE is required when shared mounts are enabled")
	}
	if bucket == "" {
		return sharedMountsConfig{}, fmt.Errorf("SPRITZ_SHARED_MOUNTS_BUCKET is required when shared mounts are enabled")
	}
	prefix := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_PREFIX"))
	if prefix == "" {
		prefix = "spritz-shared"
	}
	configPath := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_RCLONE_CONFIG"))
	maxBundleBytes := parseInt64Env("SPRITZ_SHARED_MOUNTS_MAX_BUNDLE_BYTES")

	return sharedMountsConfig{
		enabled:          true,
		prefix:           prefix,
		rcloneRemote:     remote,
		rcloneConfigPath: configPath,
		bucket:           bucket,
		mounts:           allowed,
		maxBundleBytes:   maxBundleBytes,
	}, nil
}

func (c sharedMountsConfig) enabledForMount(name string) bool {
	if !c.enabled {
		return false
	}
	_, ok := c.mounts[name]
	return ok
}

func parseInt64Env(key string) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}
