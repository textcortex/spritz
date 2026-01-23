package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestValidateSharedConfigMountPath(t *testing.T) {
	if err := validateSharedConfigMountPath(""); err == nil {
		t.Fatal("expected error for empty mount path")
	}
	if err := validateSharedConfigMountPath("shared"); err == nil {
		t.Fatal("expected error for relative mount path")
	}
	if err := validateSharedConfigMountPath("/"); err == nil {
		t.Fatal("expected error for root mount path")
	}
	if err := validateSharedConfigMountPath("/shared"); err != nil {
		t.Fatalf("unexpected error for /shared: %v", err)
	}
}

func TestLoadSharedConfigPVCSettingsDefaults(t *testing.T) {
	t.Setenv("SPRITZ_SHARED_CONFIG_PVC_PREFIX", "spritz-shared")
	t.Setenv("SPRITZ_SHARED_CONFIG_PVC_ACCESS_MODES", "")
	t.Setenv("SPRITZ_SHARED_CONFIG_MOUNT_PATH", "")

	settings := loadSharedConfigPVCSettings()
	if !settings.enabled {
		t.Fatal("expected shared config PVC to be enabled")
	}
	if settings.mountPath != "/shared" {
		t.Fatalf("expected default mount path /shared, got %s", settings.mountPath)
	}
	if len(settings.accessModes) != 1 || settings.accessModes[0] != corev1.ReadWriteMany {
		t.Fatalf("expected default access mode RWX, got %+v", settings.accessModes)
	}
}
