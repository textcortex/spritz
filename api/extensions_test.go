package main

import (
	"strings"
	"testing"
)

func TestNewExtensionRegistryRejectsUnsupportedKind(t *testing.T) {
	t.Setenv(extensionsEnvKey, `[{
		"id": "login-metadata",
		"kind": "auth_provider",
		"operation": "auth.login.metadata",
		"transport": {"url": "https://example.com/internal/extensions/login"}
	}]`)

	_, err := newExtensionRegistry()
	if err == nil {
		t.Fatal("expected unsupported extension kind error")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("expected unsupported kind error, got %v", err)
	}
}

func TestNewExtensionRegistryRejectsUnknownOperation(t *testing.T) {
	t.Setenv(extensionsEnvKey, `[{
		"id": "runtime-binding",
		"kind": "resolver",
		"operation": "preset.create.typo",
		"transport": {"url": "https://example.com/internal/extensions/preset-create"}
	}]`)

	_, err := newExtensionRegistry()
	if err == nil {
		t.Fatal("expected unsupported operation error")
	}
	if !strings.Contains(err.Error(), "must be supported") {
		t.Fatalf("expected unsupported operation error, got %v", err)
	}
}
