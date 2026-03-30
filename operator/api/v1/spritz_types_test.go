package v1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSpritzStatusDeepCopyIntoCopiesLifecycleTimestamps(t *testing.T) {
	idle := metav1.NewTime(time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC))
	max := metav1.NewTime(time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC))
	ready := metav1.NewTime(time.Date(2026, 3, 11, 11, 0, 0, 0, time.UTC))
	synced := metav1.NewTime(time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC))

	original := &SpritzStatus{
		Profile: &SpritzAgentProfileStatus{
			Name:         "Helpful Otter",
			ImageURL:     "https://console.example.com/otter.png",
			LastSyncedAt: &synced,
		},
		IdleExpiresAt: &idle,
		MaxExpiresAt:  &max,
		ReadyAt:       &ready,
	}

	var copied SpritzStatus
	original.DeepCopyInto(&copied)
	if copied.IdleExpiresAt == original.IdleExpiresAt {
		t.Fatal("expected idle expiry timestamp pointer to be deep-copied")
	}
	if copied.MaxExpiresAt == original.MaxExpiresAt {
		t.Fatal("expected max expiry timestamp pointer to be deep-copied")
	}
	if copied.Profile == original.Profile {
		t.Fatal("expected profile pointer to be deep-copied")
	}
	if copied.Profile.LastSyncedAt == original.Profile.LastSyncedAt {
		t.Fatal("expected profile lastSyncedAt pointer to be deep-copied")
	}

	updatedIdle := metav1.NewTime(copied.IdleExpiresAt.Add(2 * time.Hour))
	updatedMax := metav1.NewTime(copied.MaxExpiresAt.Add(2 * time.Hour))
	copied.IdleExpiresAt = &updatedIdle
	copied.MaxExpiresAt = &updatedMax

	if !original.IdleExpiresAt.Equal(&idle) {
		t.Fatalf("expected original idle expiry to stay unchanged, got %#v", original.IdleExpiresAt)
	}
	if !original.MaxExpiresAt.Equal(&max) {
		t.Fatalf("expected original max expiry to stay unchanged, got %#v", original.MaxExpiresAt)
	}
	if !original.Profile.LastSyncedAt.Equal(&synced) {
		t.Fatalf("expected original profile sync time to stay unchanged, got %#v", original.Profile.LastSyncedAt)
	}
}
