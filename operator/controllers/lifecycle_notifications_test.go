package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewLifecycleNotificationConfigFromEnv(t *testing.T) {
	t.Setenv("SPRITZ_LIFECYCLE_NOTIFY_URL", "https://notify.example.test/runtime")
	t.Setenv("SPRITZ_LIFECYCLE_NOTIFY_AUTH_TOKEN", "notify-token")
	t.Setenv("SPRITZ_LIFECYCLE_NOTIFY_TIMEOUT", "7s")

	cfg := NewLifecycleNotificationConfigFromEnv()

	if cfg.URL != "https://notify.example.test/runtime" {
		t.Fatalf("expected lifecycle notify url to be loaded, got %q", cfg.URL)
	}
	if cfg.AuthToken != "notify-token" {
		t.Fatalf("expected lifecycle notify auth token, got %q", cfg.AuthToken)
	}
	if cfg.Timeout != 7*time.Second {
		t.Fatalf("expected lifecycle notify timeout to be 7s, got %s", cfg.Timeout)
	}
}

func TestLifecycleNotificationConfigNotifyPhasePostsExpectedPayload(t *testing.T) {
	var received struct {
		Authorization string
		Payload       map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Authorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&received.Payload); err != nil {
			t.Fatalf("decode notification payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	cfg := LifecycleNotificationConfig{
		URL:       server.URL,
		AuthToken: "notify-token",
		Timeout:   time.Second,
		Client:    server.Client(),
	}

	if err := cfg.notifyPhase(context.Background(), "spritz-system", "zeno-acme", "Expired"); err != nil {
		t.Fatalf("notifyPhase returned error: %v", err)
	}

	if received.Authorization != "Bearer notify-token" {
		t.Fatalf("expected bearer auth header, got %q", received.Authorization)
	}
	if received.Payload["namespace"] != "spritz-system" {
		t.Fatalf("expected namespace payload, got %#v", received.Payload["namespace"])
	}
	if received.Payload["instanceId"] != "zeno-acme" {
		t.Fatalf("expected instanceId payload, got %#v", received.Payload["instanceId"])
	}
	if received.Payload["phase"] != "Expired" {
		t.Fatalf("expected phase payload, got %#v", received.Payload["phase"])
	}
}
