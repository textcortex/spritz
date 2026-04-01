package main

import (
	"strings"
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestValidateSpritzRuntimePolicyRequiresAllFields(t *testing.T) {
	err := validateSpritzRuntimePolicy(&spritzv1.SpritzRuntimePolicy{
		NetworkProfile: "dev-cluster-only",
		MountProfile:   "dev-default",
	})
	if err == nil {
		t.Fatal("expected runtime policy validation to fail")
	}
	if !strings.Contains(err.Error(), "exposureProfile") {
		t.Fatalf("expected missing exposureProfile error, got %v", err)
	}
}

func TestMergeSpritzRuntimePolicyStrictRejectsOverwrite(t *testing.T) {
	_, err := mergeSpritzRuntimePolicyStrict(
		&spritzv1.SpritzRuntimePolicy{
			NetworkProfile:  "dev-cluster-only",
			MountProfile:    "dev-default",
			ExposureProfile: "internal-acp",
			Revision:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		&spritzv1.SpritzRuntimePolicy{
			NetworkProfile:  "dev-github-only",
			MountProfile:    "dev-default",
			ExposureProfile: "internal-acp",
			Revision:        "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	)
	if err == nil {
		t.Fatal("expected overwrite rejection")
	}
	if !strings.Contains(err.Error(), "spec.runtimePolicy") {
		t.Fatalf("expected runtimePolicy overwrite error, got %v", err)
	}
}
