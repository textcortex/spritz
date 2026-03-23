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

func TestNewExtensionRegistryAcceptsChannelRouteResolveOperation(t *testing.T) {
	t.Setenv(extensionsEnvKey, `[{
		"id": "channel-routing",
		"kind": "resolver",
		"operation": "channel.route.resolve",
		"transport": {"url": "https://example.com/internal/extensions/channel-routing"}
	}]`)

	registry, err := newExtensionRegistry()
	if err != nil {
		t.Fatalf("expected channel route resolve operation to be accepted, got %v", err)
	}
	if len(registry.resolvers) != 1 {
		t.Fatalf("expected one resolver, got %d", len(registry.resolvers))
	}
	if registry.resolvers[0].operation != extensionOperation("channel.route.resolve") {
		t.Fatalf("expected channel.route.resolve operation, got %q", registry.resolvers[0].operation)
	}
}

func TestNormalizeExtensionMatchSanitizesPresetIDs(t *testing.T) {
	match, err := normalizeExtensionMatch(extensionMatchInput{PresetIDs: []string{"Zeno", "my_preset"}})
	if err != nil {
		t.Fatalf("normalizeExtensionMatch failed: %v", err)
	}
	if _, ok := match.presetIDs["zeno"]; !ok {
		t.Fatalf("expected sanitized preset id to include zeno, got %#v", match.presetIDs)
	}
	if _, ok := match.presetIDs["my-preset"]; !ok {
		t.Fatalf("expected sanitized preset id to include my-preset, got %#v", match.presetIDs)
	}
}

func TestNewExtensionRegistryRejectsInvalidSanitizedPresetID(t *testing.T) {
	t.Setenv(extensionsEnvKey, `[{
		"id": "runtime-binding",
		"kind": "resolver",
		"operation": "preset.create.resolve",
		"match": {"presetIds": ["!!!"]},
		"transport": {"url": "https://example.com/internal/extensions/preset-create"}
	}]`)

	_, err := newExtensionRegistry()
	if err == nil {
		t.Fatal("expected invalid preset id error")
	}
	if !strings.Contains(err.Error(), "presetIds contains invalid ids") {
		t.Fatalf("expected invalid preset id error, got %v", err)
	}
}
