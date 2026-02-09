package main

import (
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"spritz.sh/operator/sharedmounts"
)

func TestNormalizeUserConfigSharedMountRoots(t *testing.T) {
	policy := userConfigPolicy{
		allowSharedMounts: true,
		allowedMountRoots: []string{"/home/dev"},
	}
	keys := map[string]json.RawMessage{"sharedMounts": []byte("[]")}
	badMounts := []sharedmounts.MountSpec{
		{Name: "config", MountPath: "/etc", Scope: sharedmounts.ScopeOwner},
	}
	cfg := userConfigPayload{SharedMounts: &badMounts}
	if _, err := normalizeUserConfig(policy, keys, cfg); err == nil {
		t.Fatalf("expected error for disallowed mount path")
	}

	okMounts := []sharedmounts.MountSpec{
		{Name: "config", MountPath: "/home/dev/.config", Scope: sharedmounts.ScopeOwner},
	}
	cfg = userConfigPayload{SharedMounts: &okMounts}
	if _, err := normalizeUserConfig(policy, keys, cfg); err != nil {
		t.Fatalf("expected allowed mount path, got %v", err)
	}
}

func TestNormalizeUserConfigEnvAllowlist(t *testing.T) {
	policy := userConfigPolicy{
		allowEnv:       true,
		allowedEnvKeys: map[string]struct{}{"FOO": {}},
	}
	keys := map[string]json.RawMessage{"env": []byte("[]")}
	env := []corev1.EnvVar{{Name: "BAR", Value: "1"}}
	cfg := userConfigPayload{Env: &env}
	if _, err := normalizeUserConfig(policy, keys, cfg); err == nil {
		t.Fatalf("expected error for disallowed env")
	}

	env = []corev1.EnvVar{{Name: "FOO", Value: "1"}}
	cfg = userConfigPayload{Env: &env}
	if _, err := normalizeUserConfig(policy, keys, cfg); err != nil {
		t.Fatalf("expected allowed env, got %v", err)
	}
}

func TestNormalizeUserConfigTTLMax(t *testing.T) {
	policy := userConfigPolicy{
		allowTTL: true,
		maxTTL:   time.Hour,
	}
	keys := map[string]json.RawMessage{"ttl": []byte("\"2h\"")}
	value := "2h"
	cfg := userConfigPayload{TTL: &value}
	if _, err := normalizeUserConfig(policy, keys, cfg); err == nil {
		t.Fatalf("expected error for ttl exceeding max")
	}

	value = "30m"
	cfg = userConfigPayload{TTL: &value}
	if _, err := normalizeUserConfig(policy, keys, cfg); err != nil {
		t.Fatalf("expected allowed ttl, got %v", err)
	}
}
