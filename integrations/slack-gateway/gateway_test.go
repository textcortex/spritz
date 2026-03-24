package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOAuthCallbackStoresInstallationAndUpsertsRegistry(t *testing.T) {
	var upsertPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-installations/upsert" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upsertPayload); err != nil {
			t.Fatalf("decode backend payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "resolved"})
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth.v2.access" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"app_id":       "A_app_1",
			"scope":        "app_mentions:read,channels:history,chat:write",
			"access_token": "xoxb-installed",
			"bot_user_id":  "U_bot",
			"team":         map[string]any{"id": "T_workspace_1"},
			"authed_user":  map[string]any{"id": "U_installer"},
		})
	}))
	defer slackAPI.Close()

	publicURL := "https://gateway.example.test"
	cfg := config{
		PublicURL:             publicURL,
		SlackClientID:         "client-id",
		SlackClientSecret:     "client-secret",
		SlackSigningSecret:    "signing-secret",
		OAuthStateSecret:      "oauth-state-secret",
		SlackAPIBaseURL:       slackAPI.URL,
		SlackBotScopes:        []string{"chat:write"},
		BackendBaseURL:        backend.URL,
		BackendInternalToken:  "backend-internal-token",
		SpritzBaseURL:         "https://spritz.example.test",
		SpritzServiceToken:    "spritz-service-token",
		PrincipalID:           "shared-slack-gateway",
		InstallationStorePath: filepath.Join(t.TempDir(), "installations.json"),
		HTTPTimeout:           5 * time.Second,
		DedupeTTL:             time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	stored, ok, err := gateway.installations.GetByTeamID("T_workspace_1")
	if err != nil || !ok {
		t.Fatalf("expected stored installation, ok=%v err=%v", ok, err)
	}
	if stored.BotAccessToken != "xoxb-installed" {
		t.Fatalf("expected stored token, got %q", stored.BotAccessToken)
	}
	if upsertPayload["principalId"] != "shared-slack-gateway" {
		t.Fatalf("expected principalId to match, got %#v", upsertPayload["principalId"])
	}
}

func TestSlackEventRoutesToConversationAndReplies(t *testing.T) {
	var slackCalls struct {
		sync.Mutex
		payloads []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat.postMessage" {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode slack post body: %v", err)
			}
			slackCalls.Lock()
			slackCalls.payloads = append(slackCalls.payloads, payload)
			slackCalls.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		t.Fatalf("unexpected slack path %s", r.URL.Path)
	}))
	defer slackAPI.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
			},
		})
	}))
	defer backend.Close()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/channel-conversations/upsert":
			writeJSON(w, http.StatusCreated, map[string]any{
				"status": "success",
				"data": map[string]any{
					"created": true,
					"conversation": map[string]any{
						"metadata": map[string]any{"name": "conv-1"},
						"spec":     map[string]any{"cwd": "/home/dev"},
					},
				},
			})
		case r.URL.Path == "/api/acp/conversations/conv-1/bootstrap":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success",
				"data": map[string]any{
					"effectiveSessionId": "session-1",
					"conversation": map[string]any{
						"metadata": map[string]any{"name": "conv-1"},
						"spec":     map[string]any{"sessionId": "session-1", "cwd": "/home/dev"},
					},
				},
			})
		case r.URL.Path == "/api/acp/conversations/conv-1/connect":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Fatalf("upgrade failed: %v", err)
			}
			defer conn.Close()
			for {
				_, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var message map[string]any
				if err := json.Unmarshal(payload, &message); err != nil {
					t.Fatalf("decode ws payload: %v", err)
				}
				switch message["method"] {
				case "initialize":
					_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": map[string]any{"protocolVersion": 1}})
				case "session/load":
					_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": map[string]any{}})
				case "session/prompt":
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello from concierge",
								}},
							},
						},
					})
					_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": map[string]any{}})
					return
				default:
					t.Fatalf("unexpected ACP method %#v", message["method"])
				}
			}
		default:
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
	}))
	defer spritz.Close()

	cfg := config{
		SlackSigningSecret:    "signing-secret",
		OAuthStateSecret:      "oauth-state-secret",
		SlackAPIBaseURL:       slackAPI.URL,
		BackendBaseURL:        backend.URL,
		BackendInternalToken:  "backend-internal-token",
		SpritzBaseURL:         spritz.URL,
		SpritzServiceToken:    "spritz-service-token",
		PrincipalID:           "shared-slack-gateway",
		InstallationStorePath: filepath.Join(t.TempDir(), "installations.json"),
		HTTPTimeout:           5 * time.Second,
		DedupeTTL:             time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := gateway.installations.Upsert(slackInstallation{
		ProviderInstallRef: "slack-install:T_workspace_1",
		APIAppID:           "A_app_1",
		TeamID:             "T_workspace_1",
		BotAccessToken:     "xoxb-installed",
	}); err != nil {
		t.Fatalf("seed installation failed: %v", err)
	}

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_1",
		"event":{"type":"app_mention","user":"U_1","text":"<@U_bot> hello","channel":"C_1","channel_type":"channel","ts":"1711387375.000100"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	signSlackRequest(req.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	rec := httptest.NewRecorder()

	gateway.handleSlackEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		slackCalls.Lock()
		count := len(slackCalls.payloads)
		payload := map[string]any(nil)
		if count > 0 {
			payload = slackCalls.payloads[0]
		}
		slackCalls.Unlock()
		if count > 0 {
			if payload["channel"] != "C_1" {
				t.Fatalf("expected channel C_1, got %#v", payload["channel"])
			}
			if !strings.Contains(fmt.Sprint(payload["text"]), "Hello from concierge") {
				t.Fatalf("expected assistant reply, got %#v", payload["text"])
			}
			if payload["thread_ts"] != "1711387375.000100" {
				t.Fatalf("expected thread reply, got %#v", payload["thread_ts"])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected slack reply to be posted")
}

func signSlackRequest(header http.Header, signingSecret string, body []byte, now time.Time) {
	timestamp := fmt.Sprintf("%d", now.Unix())
	base := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(signingSecret))
	_, _ = mac.Write([]byte(base))
	header.Set("X-Slack-Request-Timestamp", timestamp)
	header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
}
