package controllers

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestComputeSpritzLifecycleWindowChoosesEarlierIdleExpiry(t *testing.T) {
	createdAt := time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)
	lastActivity := metav1.NewTime(createdAt.Add(30 * time.Minute))
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(createdAt),
		},
		Spec: spritzv1.SpritzSpec{
			IdleTTL: "1h",
			TTL:     "168h",
		},
		Status: spritzv1.SpritzStatus{
			LastActivityAt: &lastActivity,
		},
	}

	idleExpiresAt, maxExpiresAt, effectiveExpiresAt, reason, err := spritzv1.LifecycleExpiryTimes(spritz)
	if err != nil {
		t.Fatalf("LifecycleExpiryTimes returned error: %v", err)
	}
	if reason != spritzv1.LifecycleReasonIdleTTL {
		t.Fatalf("expected idle ttl lifecycle reason, got %q", reason)
	}
	if idleExpiresAt == nil || !idleExpiresAt.Time.Equal(createdAt.Add(90*time.Minute)) {
		t.Fatalf("unexpected idle expiry: %#v", idleExpiresAt)
	}
	if maxExpiresAt == nil || !maxExpiresAt.Time.Equal(createdAt.Add(168*time.Hour)) {
		t.Fatalf("unexpected max expiry: %#v", maxExpiresAt)
	}
	if effectiveExpiresAt == nil || !effectiveExpiresAt.Time.Equal(idleExpiresAt.Time) {
		t.Fatalf("expected effective expiry to match idle expiry")
	}
}

func TestComputeSpritzLifecycleWindowRejectsInvalidIdleTTL(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)),
		},
		Spec: spritzv1.SpritzSpec{
			IdleTTL: "tomorrow",
		},
	}

	if _, _, _, _, err := spritzv1.LifecycleExpiryTimes(spritz); err == nil {
		t.Fatal("expected invalid idle ttl error")
	}
}

func TestComputeSpritzLifecycleWindowReturnsNoReasonWithoutLifetimes(t *testing.T) {
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)),
		},
	}

	idleExpiresAt, maxExpiresAt, effectiveExpiresAt, reason, err := spritzv1.LifecycleExpiryTimes(spritz)
	if err != nil {
		t.Fatalf("LifecycleExpiryTimes returned error: %v", err)
	}
	if idleExpiresAt != nil || maxExpiresAt != nil || effectiveExpiresAt != nil {
		t.Fatalf("expected no expiry timestamps, got idle=%#v max=%#v effective=%#v", idleExpiresAt, maxExpiresAt, effectiveExpiresAt)
	}
	if reason != "" {
		t.Fatalf("expected empty lifecycle reason, got %q", reason)
	}
}
