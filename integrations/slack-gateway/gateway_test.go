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
	ownerRef, ok := upsertPayload["ownerRef"].(map[string]any)
	if !ok {
		t.Fatalf("expected ownerRef object, got %#v", upsertPayload["ownerRef"])
	}
	if ownerRef["type"] != "external" {
		t.Fatalf("expected external ownerRef, got %#v", ownerRef["type"])
	}
	if ownerRef["provider"] != "slack" {
		t.Fatalf("expected slack ownerRef provider, got %#v", ownerRef["provider"])
	}
	if ownerRef["subject"] != "U_installer" {
		t.Fatalf("expected installer subject, got %#v", ownerRef["subject"])
	}
	if ownerRef["tenant"] != "T_workspace_1" {
		t.Fatalf("expected workspace tenant, got %#v", ownerRef["tenant"])
	}
}

func TestOAuthCallbackKeepsFreshInstallationWhenBackendUpsertIsAmbiguous(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend unavailable", http.StatusServiceUnavailable)
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"app_id":       "A_app_1",
			"scope":        "app_mentions:read,channels:history,chat:write",
			"access_token": "xoxb-new-token",
			"bot_user_id":  "U_bot",
			"team":         map[string]any{"id": "T_workspace_1"},
			"authed_user":  map[string]any{"id": "U_installer"},
		})
	}))
	defer slackAPI.Close()

	cfg := config{
		PublicURL:             "https://gateway.example.test",
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
	if _, err := gateway.installations.Upsert(slackInstallation{
		ProviderInstallRef: "slack-install:T_workspace_1",
		APIAppID:           "A_existing",
		TeamID:             "T_workspace_1",
		InstallingUserID:   "U_existing",
		BotAccessToken:     "xoxb-existing-token",
	}); err != nil {
		t.Fatalf("seed installation failed: %v", err)
	}
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	stored, ok, err := gateway.installations.GetByTeamID("T_workspace_1")
	if err != nil || !ok {
		t.Fatalf("expected fresh installation to remain available, ok=%v err=%v", ok, err)
	}
	if stored.BotAccessToken != "xoxb-new-token" {
		t.Fatalf("expected fresh token to remain after ambiguous failure, got %q", stored.BotAccessToken)
	}
	if stored.APIAppID != "A_app_1" {
		t.Fatalf("expected fresh app id to remain after ambiguous failure, got %q", stored.APIAppID)
	}
}

func TestOAuthCallbackRollsBackInstallationOnDeterministicBackendFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "owner was not found", http.StatusNotFound)
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"app_id":       "A_app_1",
			"scope":        "app_mentions:read,channels:history,chat:write",
			"access_token": "xoxb-new-token",
			"bot_user_id":  "U_bot",
			"team":         map[string]any{"id": "T_workspace_1"},
			"authed_user":  map[string]any{"id": "U_installer"},
		})
	}))
	defer slackAPI.Close()

	cfg := config{
		PublicURL:             "https://gateway.example.test",
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
	if _, err := gateway.installations.Upsert(slackInstallation{
		ProviderInstallRef: "slack-install:T_workspace_1",
		APIAppID:           "A_existing",
		TeamID:             "T_workspace_1",
		InstallingUserID:   "U_existing",
		BotAccessToken:     "xoxb-existing-token",
	}); err != nil {
		t.Fatalf("seed installation failed: %v", err)
	}
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	stored, ok, err := gateway.installations.GetByTeamID("T_workspace_1")
	if err != nil || !ok {
		t.Fatalf("expected prior installation to be restored, ok=%v err=%v", ok, err)
	}
	if stored.BotAccessToken != "xoxb-existing-token" {
		t.Fatalf("expected original token to remain after rollback, got %q", stored.BotAccessToken)
	}
	if stored.APIAppID != "A_existing" {
		t.Fatalf("expected original app id to remain after rollback, got %q", stored.APIAppID)
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

func TestSlackEventReturnsRetryableErrorWhenProcessingFails(t *testing.T) {
	cfg := config{
		SlackSigningSecret:    "signing-secret",
		OAuthStateSecret:      "oauth-state-secret",
		SlackAPIBaseURL:       "https://slack.example.test",
		BackendBaseURL:        "https://backend.example.test",
		BackendInternalToken:  "backend-internal-token",
		SpritzBaseURL:         "https://spritz.example.test",
		SpritzServiceToken:    "spritz-service-token",
		PrincipalID:           "shared-slack-gateway",
		InstallationStorePath: filepath.Join(t.TempDir(), "installations.json"),
		HTTPTimeout:           5 * time.Second,
		DedupeTTL:             time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_missing",
		"event":{"type":"app_mention","user":"U_1","text":"<@U_bot> hello","channel":"C_1","channel_type":"channel","ts":"1711387375.000100"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	signSlackRequest(req.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	rec := httptest.NewRecorder()

	gateway.handleSlackEvents(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 to trigger a Slack retry, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertChannelConversationUsesChannelForDirectMessages(t *testing.T) {
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
					"metadata": map[string]any{"name": "conv-dm"},
				},
			},
		})
	}))
	defer spritz.Close()

	cfg := config{
		SpritzBaseURL:      spritz.URL,
		SpritzServiceToken: "spritz-service-token",
		PrincipalID:        "shared-slack-gateway",
		HTTPTimeout:        5 * time.Second,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	conversationID, err := gateway.upsertChannelConversation(
		t.Context(),
		channelSession{
			Namespace:   "spritz-staging",
			InstanceID:  "zeno-acme",
			OwnerAuthID: "owner-123",
		},
		slackEventInner{
			Type:        "message",
			Channel:     "D_workspace_bot",
			ChannelType: "im",
			TS:          "1711387375.000100",
		},
		"T_workspace_1",
	)
	if err != nil {
		t.Fatalf("upsert channel conversation failed: %v", err)
	}
	if conversationID != "conv-dm" {
		t.Fatalf("expected conversation id conv-dm, got %q", conversationID)
	}
	if upsertPayload["externalConversationId"] != "D_workspace_bot" {
		t.Fatalf(
			"expected DM conversation to key by channel id, got %#v",
			upsertPayload["externalConversationId"],
		)
	}
}

func TestDedupeStoreAllowsRetryAfterFailure(t *testing.T) {
	store := newDedupeStore(time.Minute)
	now := time.Date(2026, 3, 24, 14, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	firstLease, duplicated := store.begin("message:T_workspace_1:C_1:1711387375.000100")
	if duplicated || firstLease == nil {
		t.Fatalf("expected first delivery to acquire a lease")
	}
	if secondLease, duplicated := store.begin("message:T_workspace_1:C_1:1711387375.000100"); !duplicated || secondLease != nil {
		t.Fatalf("expected in-flight duplicate to be suppressed")
	}

	firstLease.finish(false)

	retryLease, duplicated := store.begin("message:T_workspace_1:C_1:1711387375.000100")
	if duplicated || retryLease == nil {
		t.Fatalf("expected retry after failure to reacquire the lease")
	}
	retryLease.finish(true)

	if duplicateLease, duplicated := store.begin("message:T_workspace_1:C_1:1711387375.000100"); !duplicated || duplicateLease != nil {
		t.Fatalf("expected successful delivery to suppress duplicates within the TTL")
	}
}

func TestNormalizeSlackPromptTextPreservesNonGatewayMentions(t *testing.T) {
	normalized := normalizeSlackPromptText(
		"app_mention",
		"<@U_BOT> ask <@U_APPROVER> for approval",
		"U_BOT",
	)
	if normalized != "ask <@U_APPROVER> for approval" {
		t.Fatalf("expected non-gateway mentions to remain, got %q", normalized)
	}
}

func TestProcessMessageEventSuppressesRetryAfterSlackReplyFailure(t *testing.T) {
	var postCalls int
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		postCalls++
		http.Error(w, "slack unavailable", http.StatusBadGateway)
	}))
	defer slackAPI.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	var promptCalls int
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
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
		case "/api/acp/conversations/conv-1/bootstrap":
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
		case "/api/acp/conversations/conv-1/connect":
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
					promptCalls++
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

	envelope := slackEnvelope{
		Type:     "event_callback",
		TeamID:   "T_workspace_1",
		APIAppID: "A_app_1",
		EventID:  "Ev_retry",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_1",
			Text:        "<@U_bot> hello",
			Channel:     "C_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}

	if err := gateway.processMessageEvent(t.Context(), envelope); err == nil {
		t.Fatalf("expected first delivery to fail on slack post")
	}
	if err := gateway.processMessageEvent(t.Context(), envelope); err != nil {
		t.Fatalf("expected duplicate retry to be suppressed, got %v", err)
	}
	if promptCalls != 1 {
		t.Fatalf("expected ACP prompt to run once, got %d", promptCalls)
	}
	if postCalls != 1 {
		t.Fatalf("expected one slack post attempt, got %d", postCalls)
	}
}

func TestProcessMessageEventAllowsRetryWhenPromptWasNotDelivered(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	postCalls := 0
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		postCalls++
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer slackAPI.Close()

	var promptCalls int
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
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
		case "/api/acp/conversations/conv-1/bootstrap":
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
		case "/api/acp/conversations/conv-1/connect":
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
					promptCalls++
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

	envelope := slackEnvelope{
		Type:     "event_callback",
		TeamID:   "T_workspace_1",
		APIAppID: "A_app_1",
		EventID:  "Ev_retryable",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_1",
			Text:        "<@U_bot> hello",
			Channel:     "C_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}

	if err := gateway.processMessageEvent(t.Context(), envelope); err == nil {
		t.Fatalf("expected first delivery to fail when prompt transport drops")
	}
	if err := gateway.processMessageEvent(t.Context(), envelope); err == nil {
		t.Fatalf("expected retry to re-attempt prompt delivery")
	}
	if promptCalls != 2 {
		t.Fatalf("expected ACP prompt to run twice after retryable failures, got %d", promptCalls)
	}
	if postCalls != 0 {
		t.Fatalf("expected no slack reply on undelivered prompt failure, got %d posts", postCalls)
	}
}

func TestLoadConfigRejectsRelativePublicURL(t *testing.T) {
	t.Setenv("SPRITZ_SLACK_GATEWAY_PUBLIC_URL", "/slack")
	t.Setenv("SPRITZ_SLACK_CLIENT_ID", "client-id")
	t.Setenv("SPRITZ_SLACK_CLIENT_SECRET", "client-secret")
	t.Setenv("SPRITZ_SLACK_SIGNING_SECRET", "signing-secret")
	t.Setenv("SPRITZ_SLACK_OAUTH_STATE_SECRET", "oauth-state-secret")
	t.Setenv("SPRITZ_SLACK_BACKEND_BASE_URL", "https://backend.example.test")
	t.Setenv("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN", "backend-internal-token")
	t.Setenv("SPRITZ_SLACK_SPRITZ_BASE_URL", "https://spritz.example.test")
	t.Setenv("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN", "spritz-service-token")
	t.Setenv("SPRITZ_SLACK_PRINCIPAL_ID", "shared-slack-gateway")
	t.Setenv("SPRITZ_SLACK_INSTALLATION_STORE_PATH", filepath.Join(t.TempDir(), "installations.json"))

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "must be an absolute URL") {
		t.Fatalf("expected absolute-url validation error, got %v", err)
	}
}

func TestSpritzWebSocketURLPreservesBasePath(t *testing.T) {
	gateway := newSlackGateway(
		config{SpritzBaseURL: "https://spritz.example.test/prefix"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	wsURL, err := gateway.spritzWebSocketURL(
		"/api/acp/conversations/conv-1/connect",
		map[string]string{"namespace": "spritz-staging"},
	)
	if err != nil {
		t.Fatalf("spritzWebSocketURL failed: %v", err)
	}
	parsed, err := url.Parse(wsURL)
	if err != nil {
		t.Fatalf("parse ws url: %v", err)
	}
	if parsed.Path != "/prefix/api/acp/conversations/conv-1/connect" {
		t.Fatalf("expected base path to be preserved, got %q", parsed.Path)
	}
	if parsed.Query().Get("namespace") != "spritz-staging" {
		t.Fatalf("expected namespace query, got %q", parsed.RawQuery)
	}
}

func signSlackRequest(header http.Header, signingSecret string, body []byte, now time.Time) {
	timestamp := fmt.Sprintf("%d", now.Unix())
	base := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(signingSecret))
	_, _ = mac.Write([]byte(base))
	header.Set("X-Slack-Request-Timestamp", timestamp)
	header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
}
