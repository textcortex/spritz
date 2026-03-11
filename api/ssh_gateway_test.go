package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestSSHActivityRefreshIntervalUsesHalfIdleTTLWhenShorter(t *testing.T) {
	spec := spritzv1.SpritzSpec{IdleTTL: "80ms"}

	interval := sshActivityRefreshInterval(spec, time.Second)
	if interval != 40*time.Millisecond {
		t.Fatalf("expected 40ms interval, got %s", interval)
	}
}

func TestStartSSHActivityLoopRefreshesWhileSessionIsOpen(t *testing.T) {
	var calls atomic.Int32
	s := &server{
		sshGateway: sshGatewayConfig{activityRefresh: 40 * time.Millisecond},
		activityRecorder: func(ctx context.Context, namespace, name string, when time.Time) error {
			calls.Add(1)
			return nil
		},
	}
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ssh-workspace",
			Namespace: "spritz-test",
		},
		Spec: spritzv1.SpritzSpec{IdleTTL: "80ms"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.startSSHActivityLoop(ctx, spritz)
	time.Sleep(95 * time.Millisecond)
	cancel()

	if calls.Load() < 2 {
		t.Fatalf("expected repeated activity refreshes, got %d", calls.Load())
	}
}
