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
