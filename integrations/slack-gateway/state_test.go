package main

import (
	"testing"
	"time"
)

func TestOAuthStateRoundTrip(t *testing.T) {
	manager := newOAuthStateManager("secret-key", 15*time.Minute)
	fixed := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return fixed }
	state, err := manager.generate()
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	manager.now = func() time.Time { return fixed.Add(5 * time.Minute) }
	if err := manager.validate(state); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	manager.now = func() time.Time { return fixed.Add(16 * time.Minute) }
	if err := manager.validate(state); err == nil {
		t.Fatalf("expected expired state to fail")
	}
}

func TestPendingInstallStateRoundTrip(t *testing.T) {
	manager := newOAuthStateManager("secret-key", 15*time.Minute)
	fixed := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return fixed }

	state, err := manager.generatePendingInstall(pendingInstallState{
		RequestID: "install-request-1",
		Installation: slackInstallation{
			TeamID:           "T_workspace_1",
			InstallingUserID: "U_installer",
			BotAccessToken:   "xoxb-secret",
		},
	})
	if err != nil {
		t.Fatalf("generatePendingInstall failed: %v", err)
	}
	if state == "" {
		t.Fatal("expected encrypted pending install token")
	}
	if state == "xoxb-secret" {
		t.Fatal("pending install token must not expose installation secrets")
	}

	manager.now = func() time.Time { return fixed.Add(5 * time.Minute) }
	parsed, err := manager.parsePendingInstall(state)
	if err != nil {
		t.Fatalf("parsePendingInstall failed: %v", err)
	}
	if parsed.RequestID != "install-request-1" {
		t.Fatalf("expected request id to round-trip, got %q", parsed.RequestID)
	}
	if parsed.Installation.BotAccessToken != "xoxb-secret" {
		t.Fatalf("expected installation payload to round-trip, got %#v", parsed.Installation)
	}
}
