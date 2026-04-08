package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUpsertChannelConversationOmitsImplicitDefaultCWD(t *testing.T) {
	var upsertPayload map[string]any
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channel-conversations/upsert" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upsertPayload); err != nil {
			t.Fatalf("decode upsert payload: %v", err)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"status": "success",
			"data": map[string]any{
				"created": true,
				"conversation": map[string]any{
					"metadata": map[string]any{"name": "conv-1"},
				},
			},
		})
	}))
	defer spritz.Close()

	gateway := newSlackGateway(config{
		SpritzBaseURL:      spritz.URL,
		SpritzServiceToken: "spritz-service-token",
		PrincipalID:        "shared-slack-gateway",
		HTTPTimeout:        5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	conversationID, err := gateway.upsertChannelConversation(
		context.Background(),
		channelSession{
			AccessToken: "spritz-access-token",
			OwnerAuthID: "user-1",
			Namespace:   "spritz-staging",
			InstanceID:  "tidy-otter",
		},
		slackEventInner{Channel: "D123"},
		"T123",
		"",
		"slack-thread-1",
		nil,
	)
	if err != nil {
		t.Fatalf("upsert channel conversation: %v", err)
	}
	if conversationID != "conv-1" {
		t.Fatalf("expected conversation id conv-1, got %q", conversationID)
	}
	if _, exists := upsertPayload["cwd"]; exists {
		t.Fatalf("expected channel conversation upsert to omit cwd, got %#v", upsertPayload)
	}
	if _, exists := upsertPayload["lookupExternalConversationIds"]; exists {
		t.Fatalf("expected channel conversation upsert to omit lookup ids when none are provided, got %#v", upsertPayload)
	}
}

func TestBootstrapConversationUsesEffectiveCWD(t *testing.T) {
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/acp/conversations/conv-1/bootstrap" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{
				"effectiveSessionId": "session-1",
				"effectiveCwd":       "/workspace/platform",
				"conversation": map[string]any{
					"metadata": map[string]any{"name": "conv-1"},
					"spec":     map[string]any{"sessionId": "session-1"},
				},
			},
		})
	}))
	defer spritz.Close()

	gateway := newSlackGateway(config{
		SpritzBaseURL:      spritz.URL,
		SpritzServiceToken: "spritz-service-token",
		PrincipalID:        "shared-slack-gateway",
		HTTPTimeout:        5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sessionID, cwd, err := gateway.bootstrapConversation(context.Background(), "spritz-service-token", "spritz-staging", "conv-1")
	if err != nil {
		t.Fatalf("bootstrap conversation: %v", err)
	}
	if sessionID != "session-1" {
		t.Fatalf("expected session id session-1, got %q", sessionID)
	}
	if cwd != "/workspace/platform" {
		t.Fatalf("expected effective cwd /workspace/platform, got %q", cwd)
	}
}
