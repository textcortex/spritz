package main

import (
	"context"
	"log"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	spritzv1 "spritz.sh/operator/api/v1"
)

const acpPromptActivityTimeout = 2 * time.Second

type warnLogger interface {
	Warnf(string, ...interface{})
}

func (s *server) recordSpritzActivity(ctx context.Context, namespace, name string, when time.Time) error {
	if s.activityRecorder != nil {
		return s.activityRecorder(ctx, namespace, name, when)
	}
	return s.markSpritzActivity(ctx, namespace, name, when)
}

func (s *server) scheduleACPPromptActivity(logger warnLogger, namespace, name string, payload []byte) {
	if !isACPPromptMessage(payload) {
		return
	}
	when := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), acpPromptActivityTimeout)
		defer cancel()
		if err := s.recordSpritzActivity(ctx, namespace, name, when); err != nil && logger != nil {
			logger.Warnf("failed to record acp activity for %s/%s: %v", namespace, name, err)
		}
	}()
}

func (s *server) markSpritzActivity(ctx context.Context, namespace, name string, when time.Time) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &spritzv1.Spritz{}
		if err := s.client.Get(ctx, clientKey(namespace, name), current); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		timestamp := metav1.NewTime(when.UTC())
		if current.Status.LastActivityAt != nil && !current.Status.LastActivityAt.Time.Before(timestamp.Time) {
			return nil
		}
		current.Status.LastActivityAt = &timestamp
		return s.client.Status().Update(ctx, current)
	})
}

func spritzActivityRefreshInterval(spec spritzv1.SpritzSpec, fallback time.Duration) time.Duration {
	interval := fallback
	if interval <= 0 {
		interval = time.Minute
	}
	if raw := strings.TrimSpace(spec.IdleTTL); raw != "" {
		if idleTTL, err := time.ParseDuration(raw); err == nil && idleTTL > 0 {
			candidate := idleTTL / 2
			if candidate <= 0 {
				candidate = idleTTL
			}
			if candidate > 0 && candidate < interval {
				interval = candidate
			}
		}
	}
	if interval <= 0 {
		return time.Minute
	}
	return interval
}

func (s *server) startSpritzActivityLoop(ctx context.Context, spritz *spritzv1.Spritz, fallback time.Duration, source string) {
	if s == nil || spritz == nil {
		return
	}
	record := func(when time.Time) {
		refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.recordSpritzActivity(refreshCtx, spritz.Namespace, spritz.Name, when); err != nil {
			log.Printf("spritz %s: failed to refresh activity name=%s namespace=%s err=%v", source, spritz.Name, spritz.Namespace, err)
		}
	}
	record(time.Now())

	interval := spritzActivityRefreshInterval(spritz.Spec, fallback)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case tick := <-ticker.C:
				record(tick)
			}
		}
	}()
}
