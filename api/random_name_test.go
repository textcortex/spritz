package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestCreateRandomSpritzNameFormat(t *testing.T) {
	name := createRandomSpritzName("", nil)
	if name == "" {
		t.Fatal("expected non-empty name")
	}
	pattern := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	if !pattern.MatchString(name) {
		t.Fatalf("name %q did not match expected format", name)
	}
}

func TestCreateRandomSpritzNamePrefixesDerivedImageName(t *testing.T) {
	name := createRandomSpritzName(resolveSpritzNamePrefix("", "registry.example.com/spritz-openclaw:staging"), nil)
	if !strings.HasPrefix(name, "openclaw-") {
		t.Fatalf("expected openclaw prefix, got %q", name)
	}
}

func TestResolveSpritzNamePrefixPrefersExplicitValue(t *testing.T) {
	got := resolveSpritzNamePrefix("Claude Code", "registry.example.com/spritz-openclaw:staging")
	if got != "claude-code" {
		t.Fatalf("expected explicit prefix to win, got %q", got)
	}
}

func TestResolveSpritzNamePrefixDerivesFromImageAndStripsSpritzPrefix(t *testing.T) {
	got := resolveSpritzNamePrefix("", "registry.example.com/spritz-claude-code:staging")
	if got != "claude-code" {
		t.Fatalf("expected claude-code, got %q", got)
	}
}

func TestJoinSpritzNamePreservesTailWhenPrefixIsLong(t *testing.T) {
	name := joinSpritzName("this-prefix-is-way-too-long-for-a-single-kubernetes-resource-name", "tidal-otter", "12")
	if len(name) > 63 {
		t.Fatalf("expected name length <= 63, got %d for %q", len(name), name)
	}
	if !strings.HasSuffix(name, "-tidal-otter-12") {
		t.Fatalf("expected tail to be preserved, got %q", name)
	}
}
