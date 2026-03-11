package v1

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	LifecycleReasonIdleTTL = "IdleTTL"
	LifecycleReasonTTL     = "TTL"
)

// LifecycleExpiryTimes returns the idle expiry, max expiry, effective expiry,
// and the reason for the effective expiry for a spritz lifecycle configuration.
func LifecycleExpiryTimes(spritz *Spritz) (*metav1.Time, *metav1.Time, *metav1.Time, string, error) {
	if spritz == nil {
		return nil, nil, nil, "", nil
	}

	var idleExpiresAt *metav1.Time
	if value := strings.TrimSpace(spritz.Spec.IdleTTL); value != "" {
		idleTTL, err := time.ParseDuration(value)
		if err != nil {
			return nil, nil, nil, "", fmt.Errorf("invalid idle ttl format")
		}
		base := spritz.CreationTimestamp.Time
		if spritz.Status.LastActivityAt != nil && spritz.Status.LastActivityAt.Time.After(base) {
			base = spritz.Status.LastActivityAt.Time
		}
		expires := metav1.NewTime(base.Add(idleTTL))
		idleExpiresAt = &expires
	}

	var maxExpiresAt *metav1.Time
	if value := strings.TrimSpace(spritz.Spec.TTL); value != "" {
		maxTTL, err := time.ParseDuration(value)
		if err != nil {
			return nil, nil, nil, "", fmt.Errorf("invalid ttl format")
		}
		expires := metav1.NewTime(spritz.CreationTimestamp.Add(maxTTL))
		maxExpiresAt = &expires
	}

	switch {
	case idleExpiresAt == nil && maxExpiresAt == nil:
		return nil, nil, nil, "", nil
	case idleExpiresAt == nil:
		return nil, maxExpiresAt, maxExpiresAt, LifecycleReasonTTL, nil
	case maxExpiresAt == nil:
		return idleExpiresAt, nil, idleExpiresAt, LifecycleReasonIdleTTL, nil
	case idleExpiresAt.Before(maxExpiresAt):
		return idleExpiresAt, maxExpiresAt, idleExpiresAt, LifecycleReasonIdleTTL, nil
	default:
		return idleExpiresAt, maxExpiresAt, maxExpiresAt, LifecycleReasonTTL, nil
	}
}
