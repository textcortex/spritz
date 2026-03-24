package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

type fakeACPDebugChatServerOptions struct {
	SessionID    string
	LoadReplay   []map[string]any
	PromptChunks []string
	StopReason   string
}

type fakeACPDebugChatServer struct {
	url string

	server *httptest.Server
	mu     sync.Mutex

	initCalls        int
	newCalls         int
	loadSessionIDs   []string
	promptSessionIDs []string
	promptTexts      []string
}

func newFakeACPDebugChatServer(t *testing.T, options fakeACPDebugChatServerOptions) *fakeACPDebugChatServer {
	t.Helper()
	fakeServer := &fakeACPDebugChatServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("failed to upgrade websocket: %v", err)
		}
		defer func() {
			_ = conn.Close()
		}()

		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message struct {
				ID     any             `json:"id,omitempty"`
				Method string          `json:"method,omitempty"`
				Params json.RawMessage `json:"params,omitempty"`
			}
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("failed to decode ACP message: %v", err)
			}

			switch message.Method {
			case "initialize":
				fakeServer.mu.Lock()
				fakeServer.initCalls++
				fakeServer.mu.Unlock()
				if err := conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      message.ID,
					"result": map[string]any{
						"protocolVersion": 1,
						"agentCapabilities": map[string]any{
							"loadSession": true,
						},
						"agentInfo": map[string]any{
							"name":    "debug-agent",
							"title":   "Debug Agent",
							"version": "1.0.0",
						},
					},
				}); err != nil {
					t.Fatalf("failed to write initialize result: %v", err)
				}
			case "session/new":
				fakeServer.mu.Lock()
				fakeServer.newCalls++
				fakeServer.mu.Unlock()
				sessionID := strings.TrimSpace(options.SessionID)
				if sessionID == "" {
					sessionID = "session-fresh"
				}
				if err := conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      message.ID,
					"result": map[string]any{
						"sessionId": sessionID,
					},
				}); err != nil {
					t.Fatalf("failed to write new session result: %v", err)
				}
			case "session/load":
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(message.Params, &params); err != nil {
					t.Fatalf("failed to decode load params: %v", err)
				}
				fakeServer.mu.Lock()
				fakeServer.loadSessionIDs = append(fakeServer.loadSessionIDs, params.SessionID)
				fakeServer.mu.Unlock()
				for _, update := range options.LoadReplay {
					if err := conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": update,
						},
					}); err != nil {
						t.Fatalf("failed to write replay update: %v", err)
					}
				}
				if err := conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      message.ID,
					"result":  map[string]any{},
				}); err != nil {
					t.Fatalf("failed to write load result: %v", err)
				}
			case "session/prompt":
				var params struct {
					SessionID string `json:"sessionId"`
					Prompt    []struct {
						Text string `json:"text"`
					} `json:"prompt"`
				}
				if err := json.Unmarshal(message.Params, &params); err != nil {
					t.Fatalf("failed to decode prompt params: %v", err)
				}
				text := ""
				if len(params.Prompt) > 0 {
					text = params.Prompt[0].Text
				}
				fakeServer.mu.Lock()
				fakeServer.promptSessionIDs = append(fakeServer.promptSessionIDs, params.SessionID)
				fakeServer.promptTexts = append(fakeServer.promptTexts, text)
				fakeServer.mu.Unlock()
				for _, chunk := range options.PromptChunks {
					if err := conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": map[string]any{
									"type": "text",
									"text": chunk,
								},
							},
						},
					}); err != nil {
						t.Fatalf("failed to write prompt update: %v", err)
					}
				}
				stopReason := strings.TrimSpace(options.StopReason)
				if stopReason == "" {
					stopReason = "end_turn"
				}
				if err := conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      message.ID,
					"result": map[string]any{
						"stopReason": stopReason,
					},
				}); err != nil {
					t.Fatalf("failed to write prompt result: %v", err)
				}
			default:
				t.Fatalf("unexpected ACP method %q", message.Method)
			}
		}
	}))
	t.Cleanup(httpServer.Close)
	fakeServer.server = httpServer
	fakeServer.url = "ws" + strings.TrimPrefix(httpServer.URL, "http")
	return fakeServer
}

func TestInternalDebugChatSendCreatesConversationAndReturnsAssistantText(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	fakeACP := newFakeACPDebugChatServer(t, fakeACPDebugChatServerOptions{
		SessionID:    "session-fresh",
		PromptChunks: []string{"spritz ", "debug"},
		StopReason:   "end_turn",
	})

	s := newACPTestServer(t, spritz)
	s.internalAuth = internalAuthConfig{enabled: true, token: "internal-token"}
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	internal := e.Group("", s.internalAuthMiddleware(), s.authMiddleware())
	internal.POST("/api/internal/v1/debug/chat/send", s.sendInternalDebugChat)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/debug/chat/send", strings.NewReader(`{
		"target":{"spritzName":"tidy-otter","cwd":"/workspace/app","title":"Debug Run"},
		"reason":"local smoke",
		"message":"  hello from cli  "
	}`))
	req.Header.Set(internalTokenHeader, "internal-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string                        `json:"status"`
		Data   internalDebugChatSendResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Data.CreatedConversation {
		t.Fatalf("expected createdConversation=true")
	}
	if payload.Data.AssistantText != "spritz debug" {
		t.Fatalf("expected assistant text %q, got %q", "spritz debug", payload.Data.AssistantText)
	}
	if payload.Data.StopReason != "end_turn" {
		t.Fatalf("expected stopReason end_turn, got %q", payload.Data.StopReason)
	}
	if payload.Data.EffectiveSessionID != "session-fresh" {
		t.Fatalf("expected effective session id session-fresh, got %q", payload.Data.EffectiveSessionID)
	}
	if payload.Data.Conversation == nil || payload.Data.Conversation.Spec.CWD != "/workspace/app" {
		t.Fatalf("expected conversation cwd /workspace/app, got %#v", payload.Data.Conversation)
	}

	stored := &spritzv1.SpritzConversation{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", payload.Data.Conversation.Name), stored); err != nil {
		t.Fatalf("failed to reload stored conversation: %v", err)
	}
	if stored.Spec.Title != "Debug Run" {
		t.Fatalf("expected stored title Debug Run, got %q", stored.Spec.Title)
	}
	if stored.Spec.SessionID != "session-fresh" {
		t.Fatalf("expected stored session id session-fresh, got %q", stored.Spec.SessionID)
	}

	fakeACP.mu.Lock()
	defer fakeACP.mu.Unlock()
	if fakeACP.newCalls != 1 {
		t.Fatalf("expected one session/new call, got %d", fakeACP.newCalls)
	}
	if len(fakeACP.loadSessionIDs) != 0 {
		t.Fatalf("expected no session/load calls for a fresh session, got %#v", fakeACP.loadSessionIDs)
	}
	if len(fakeACP.promptTexts) != 1 || fakeACP.promptTexts[0] != "  hello from cli  " {
		t.Fatalf("expected one prompt with original message, got %#v", fakeACP.promptTexts)
	}
}

func TestInternalDebugChatSendTargetsExistingConversation(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Existing", metav1.Now())
	conversation.Spec.SessionID = "session-existing"

	fakeACP := newFakeACPDebugChatServer(t, fakeACPDebugChatServerOptions{
		PromptChunks: []string{"ok"},
	})

	s := newACPTestServer(t, spritz, conversation)
	s.internalAuth = internalAuthConfig{enabled: true, token: "internal-token"}
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	internal := e.Group("", s.internalAuthMiddleware(), s.authMiddleware())
	internal.POST("/api/internal/v1/debug/chat/send", s.sendInternalDebugChat)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/debug/chat/send", strings.NewReader(`{
		"target":{"conversationId":"tidy-otter-conv"},
		"message":"follow up"
	}`))
	req.Header.Set(internalTokenHeader, "internal-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string                        `json:"status"`
		Data   internalDebugChatSendResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Data.CreatedConversation {
		t.Fatalf("expected createdConversation=false")
	}
	if payload.Data.AssistantText != "ok" {
		t.Fatalf("expected assistant text ok, got %q", payload.Data.AssistantText)
	}
	if payload.Data.Conversation == nil || payload.Data.Conversation.Name != "tidy-otter-conv" {
		t.Fatalf("expected original conversation id, got %#v", payload.Data.Conversation)
	}

	fakeACP.mu.Lock()
	defer fakeACP.mu.Unlock()
	if fakeACP.newCalls != 0 {
		t.Fatalf("expected no session/new call, got %d", fakeACP.newCalls)
	}
	if len(fakeACP.loadSessionIDs) != 1 {
		t.Fatalf("expected a single bootstrap session/load call, got %#v", fakeACP.loadSessionIDs)
	}
	if fakeACP.loadSessionIDs[0] != "session-existing" {
		t.Fatalf("expected load to target session-existing, got %#v", fakeACP.loadSessionIDs)
	}
}

func TestInternalDebugChatSendRejectsOwnerMismatch(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-2")

	s := newACPTestServer(t, spritz)
	s.internalAuth = internalAuthConfig{enabled: true, token: "internal-token"}

	e := echo.New()
	internal := e.Group("", s.internalAuthMiddleware(), s.authMiddleware())
	internal.POST("/api/internal/v1/debug/chat/send", s.sendInternalDebugChat)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/debug/chat/send", strings.NewReader(`{
		"target":{"spritzName":"tidy-otter"},
		"message":"hello"
	}`))
	req.Header.Set(internalTokenHeader, "internal-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDebugChatRouteIsUnavailableWithoutAuthOrInternalAuth(t *testing.T) {
	s := newACPTestServer(t)
	s.auth = authConfig{mode: authModeNone}
	s.internalAuth = internalAuthConfig{enabled: false}

	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/debug/chat/send", strings.NewReader(`{"target":{"spritzName":"tidy-otter"},"message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected debug chat route to be unavailable without auth and internal auth, got %d: %s", rec.Code, rec.Body.String())
	}
}
