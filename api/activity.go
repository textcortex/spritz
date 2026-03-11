package main

import (
	"context"
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
