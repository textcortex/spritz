package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type noopWarnLogger struct{}

func (noopWarnLogger) Warnf(string, ...interface{}) {}

func TestScheduleACPPromptActivityDoesNotBlockPromptPath(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32

	s := &server{
		activityRecorder: func(ctx context.Context, namespace, name string, when time.Time) error {
			calls.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	start := time.Now()
	s.scheduleACPPromptActivity(noopWarnLogger{}, "spritz-test", "young-crest", []byte(`{"jsonrpc":"2.0","method":"session/prompt"}`))
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("expected prompt activity scheduling to return immediately, took %s", elapsed)
	}

	select {
	case <-started:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected background activity recorder to start")
	}

	close(release)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected one background activity write, got %d", calls.Load())
}
