package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newACPTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register spritz scheme: %v", err)
	}
	return scheme
}

func newACPTestServer(t *testing.T, objects ...client.Object) *server {
	t.Helper()
	scheme := newACPTestScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&spritzv1.Spritz{})
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	return &server{
		client:    builder.Build(),
		scheme:    scheme,
		namespace: "spritz-test",
		auth: authConfig{
			mode:     authModeHeader,
			headerID: "X-Spritz-User-Id",
		},
		internalAuth: internalAuthConfig{enabled: false},
		acp: acpConfig{
			enabled:       true,
			port:          spritzv1.DefaultACPPort,
			path:          spritzv1.DefaultACPPath,
			probeTimeout:  2 * time.Second,
			probeCacheTTL: time.Hour,
			clientInfo: acpImplementationInfo{
				Name:    "spritz-ui",
				Title:   "Spritz ACP UI",
				Version: "1.0.0",
			},
		},
	}
}

func TestListACPAgentsReturnsReadyAgentsWithConversation(t *testing.T) {
	now := metav1.Now()
	ready := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tidy-otter",
			Namespace: "spritz-test",
			Labels: map[string]string{
				ownerLabelKey: ownerLabelValue("user-1"),
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
		Status: spritzv1.SpritzStatus{
			Phase: "Ready",
			ACP: &spritzv1.SpritzACPStatus{
				State: "ready",
				Endpoint: &spritzv1.SpritzACPEndpoint{
					Port: spritzv1.DefaultACPPort,
					Path: spritzv1.DefaultACPPath,
				},
				AgentInfo: &spritzv1.SpritzACPAgentInfo{
					Name:  "agent-otter",
					Title: "Agent Otter",
				},
				LastProbeAt: &now,
			},
		},
	}
	ignored := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "slow-reef",
			Namespace: "spritz-test",
			Labels: map[string]string{
				ownerLabelKey: ownerLabelValue("user-1"),
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
		Status: spritzv1.SpritzStatus{
			Phase: "Provisioning",
		},
	}
	conversation := &spritzv1.SpritzConversation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tidy-otter",
			Namespace: "spritz-test",
			Labels: map[string]string{
				ownerLabelKey: ownerLabelValue("user-1"),
			},
		},
		Spec: spritzv1.SpritzConversationSpec{
			SpritzName: "tidy-otter",
			Owner:      spritzv1.SpritzOwner{ID: "user-1"},
			Title:      "Current chat",
			SessionID:  "sess_123",
			CWD:        "/home/dev",
		},
	}

	s := newACPTestServer(t, ready, ignored, conversation)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/acp/agents", s.listACPAgents)

	req := httptest.NewRequest(http.MethodGet, "/api/acp/agents", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Items []acpAgentResponse `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode agents response: %v", err)
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf("expected exactly one ACP-ready agent, got %d", len(payload.Data.Items))
	}
	if payload.Data.Items[0].Spritz.Name != "tidy-otter" {
		t.Fatalf("expected tidy-otter in ACP list, got %q", payload.Data.Items[0].Spritz.Name)
	}
	if payload.Data.Items[0].Conversation == nil {
		t.Fatalf("expected conversation metadata to be included")
	}
	if payload.Data.Items[0].Conversation.Spec.SessionID != "sess_123" {
		t.Fatalf("expected persisted session id, got %q", payload.Data.Items[0].Conversation.Spec.SessionID)
	}
}

func TestEnsureACPConversationCreatesConversationAfterProbe(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("failed to upgrade test websocket: %v", err)
		}
		defer conn.Close()

		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("failed to read initialize request: %v", err)
		}
		if message["method"] != "initialize" {
			t.Fatalf("expected initialize request, got %#v", message["method"])
		}
		if err := conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"result": map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"loadSession": true,
					"promptCapabilities": map[string]any{
						"embeddedContext": true,
					},
				},
				"agentInfo": map[string]any{
					"name":    "agent-reef",
					"title":   "Agent Reef",
					"version": "1.2.3",
				},
				"authMethods": []string{},
			},
		}); err != nil {
			t.Fatalf("failed to write initialize response: %v", err)
		}
	}))
	defer wsServer.Close()

	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tidy-otter",
			Namespace: "spritz-test",
			Labels: map[string]string{
				ownerLabelKey: ownerLabelValue("user-1"),
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
		Status: spritzv1.SpritzStatus{
			Phase: "Ready",
		},
	}

	s := newACPTestServer(t, spritz)
	s.acp.workspaceURL = func(namespace, name string) string {
		return strings.Replace(wsServer.URL, "http://", "ws://", 1)
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.PUT("/api/acp/conversations/:name", s.ensureACPConversation)

	req := httptest.NewRequest(http.MethodPut, "/api/acp/conversations/tidy-otter", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string                      `json:"status"`
		Data   spritzv1.SpritzConversation `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode ensure conversation response: %v", err)
	}
	if payload.Data.Spec.CWD != "/home/dev" {
		t.Fatalf("expected default ACP cwd /home/dev, got %q", payload.Data.Spec.CWD)
	}
	if payload.Data.Spec.AgentInfo == nil || payload.Data.Spec.AgentInfo.Title != "Agent Reef" {
		t.Fatalf("expected agent info from ACP initialize, got %#v", payload.Data.Spec.AgentInfo)
	}
	if payload.Data.Spec.Capabilities == nil || !payload.Data.Spec.Capabilities.LoadSession {
		t.Fatalf("expected ACP capabilities to be stored, got %#v", payload.Data.Spec.Capabilities)
	}

	storedConversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", "tidy-otter"), storedConversation); err != nil {
		t.Fatalf("expected conversation resource to be persisted: %v", err)
	}

	storedSpritz := &spritzv1.Spritz{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", "tidy-otter"), storedSpritz); err != nil {
		t.Fatalf("expected spritz resource to remain available: %v", err)
	}
	if storedSpritz.Status.ACP == nil || storedSpritz.Status.ACP.State != "ready" {
		t.Fatalf("expected ACP status to be stored on spritz, got %#v", storedSpritz.Status.ACP)
	}
}
