package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
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
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register core scheme: %v", err)
	}
	return scheme
}

func newACPTestServer(t *testing.T, objects ...client.Object) *server {
	t.Helper()
	scheme := newACPTestScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&spritzv1.Spritz{}, &spritzv1.SpritzConversation{})
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	return &server{
		client:    builder.Build(),
		scheme:    scheme,
		namespace: "spritz-test",
		auth: authConfig{
			mode:              authModeHeader,
			headerID:          "X-Spritz-User-Id",
			headerDefaultType: principalTypeHuman,
		},
		internalAuth: internalAuthConfig{enabled: false},
		acp: acpConfig{
			enabled:              true,
			port:                 spritzv1.DefaultACPPort,
			path:                 spritzv1.DefaultACPPath,
			clientCapabilities:   defaultACPClientCapabilities(),
			bootstrapDialTimeout: 5 * time.Second,
		},
	}
}

func configureServicePrincipalHeaders(s *server) {
	s.auth.headerType = "X-Spritz-Principal-Type"
	s.auth.headerScopes = "X-Spritz-Principal-Scopes"
	s.auth.headerTrustTypeAndScopes = true
}

type fakeACPBootstrapServerOptions struct {
	LoadError         *acpBootstrapJSONRPCError
	NewSessionID      string
	LoadReplayUpdates []map[string]any
}

type fakeACPBootstrapServer struct {
	url          string
	server       *httptest.Server
	mu           sync.Mutex
	loadIDs      []string
	newCalls     int
	initRequests []acpBootstrapInitializeRequest
}

func newFakeACPBootstrapServer(t *testing.T, options fakeACPBootstrapServerOptions) *fakeACPBootstrapServer {
	t.Helper()
	fakeServer := &fakeACPBootstrapServer{}
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
				var params acpBootstrapInitializeRequest
				if err := json.Unmarshal(message.Params, &params); err != nil {
					t.Fatalf("failed to decode initialize params: %v", err)
				}
				fakeServer.mu.Lock()
				fakeServer.initRequests = append(fakeServer.initRequests, params)
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
							"name":    "openclaw-gateway",
							"title":   "OpenClaw ACP Gateway",
							"version": "2026.3.8",
						},
					},
				}); err != nil {
					t.Fatalf("failed to write initialize result: %v", err)
				}
			case "session/load":
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(message.Params, &params); err != nil {
					t.Fatalf("failed to decode load params: %v", err)
				}
				fakeServer.mu.Lock()
				fakeServer.loadIDs = append(fakeServer.loadIDs, params.SessionID)
				fakeServer.mu.Unlock()
				if options.LoadError != nil {
					if err := conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"id":      message.ID,
						"error":   options.LoadError,
					}); err != nil {
						t.Fatalf("failed to write load error: %v", err)
					}
					continue
				}
				for _, update := range options.LoadReplayUpdates {
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
			case "session/new":
				fakeServer.mu.Lock()
				fakeServer.newCalls++
				fakeServer.mu.Unlock()
				sessionID := options.NewSessionID
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

func readyACPSpritz(name, owner string) *spritzv1.Spritz {
	return &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "spritz-test",
			Labels: map[string]string{
				ownerLabelKey: ownerLabelValue(owner),
			},
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/openclaw:latest",
			Owner: spritzv1.SpritzOwner{ID: owner},
		},
		Status: spritzv1.SpritzStatus{
			Phase: "Ready",
			ACP: &spritzv1.SpritzACPStatus{
				State: "ready",
				Capabilities: &spritzv1.SpritzACPCapabilities{
					LoadSession: true,
				},
				Endpoint: &spritzv1.SpritzACPEndpoint{
					Port: spritzv1.DefaultACPPort,
					Path: spritzv1.DefaultACPPath,
				},
				AgentInfo: &spritzv1.SpritzACPAgentInfo{
					Name:  "agent-" + name,
					Title: "Agent " + name,
				},
			},
		},
	}
}

func conversationFor(name, spritzName, owner, title string, createdAt metav1.Time) *spritzv1.SpritzConversation {
	return &spritzv1.SpritzConversation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "spritz-test",
			CreationTimestamp: createdAt,
			Labels: map[string]string{
				acpConversationLabelKey:       acpConversationLabelValue,
				acpConversationOwnerLabelKey:  ownerLabelValue(owner),
				acpConversationSpritzLabelKey: spritzName,
			},
		},
		Spec: spritzv1.SpritzConversationSpec{
			SpritzName: spritzName,
			Owner:      spritzv1.SpritzOwner{ID: owner},
			Title:      title,
			SessionID:  "sess-" + name,
			CWD:        "/home/dev",
		},
	}
}

func markConversationAsChannelBound(conversation *spritzv1.SpritzConversation, principalID string) {
	if conversation.Annotations == nil {
		conversation.Annotations = map[string]string{}
	}
	conversation.Annotations[channelConversationPrincipalAnnotationKey] = principalID
}

func TestListACPAgentsUsesStoredStatusOnly(t *testing.T) {
	ready := readyACPSpritz("tidy-otter", "user-1")
	ready.Status.ACP.LastProbeAt = nil
	unsupported := readyACPSpritz("echo-harbor", "user-1")
	unsupported.Status.ACP.Capabilities.LoadSession = false
	ignored := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "slow-reef",
			Namespace: "spritz-test",
			Labels: map[string]string{
				ownerLabelKey: ownerLabelValue("user-1"),
			},
		},
		Spec:   spritzv1.SpritzSpec{Image: "example.com/openclaw:latest", Owner: spritzv1.SpritzOwner{ID: "user-1"}},
		Status: spritzv1.SpritzStatus{Phase: "Provisioning"},
	}

	s := newACPTestServer(t, ready, unsupported, ignored)
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
}

func TestCreateACPConversationGeneratesIndependentConversationID(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	s := newACPTestServer(t, spritz)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations", s.createACPConversation)

	body := strings.NewReader(`{"spritzName":"tidy-otter","cwd":"/workspace/repo"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string                      `json:"status"`
		Data   spritzv1.SpritzConversation `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode conversation response: %v", err)
	}
	if payload.Data.Name == "tidy-otter" || !strings.HasPrefix(payload.Data.Name, "tidy-otter-") {
		t.Fatalf("expected generated conversation id with tidy-otter prefix, got %q", payload.Data.Name)
	}
	if payload.Data.Spec.SpritzName != "tidy-otter" {
		t.Fatalf("expected conversation to target tidy-otter, got %q", payload.Data.Spec.SpritzName)
	}
	if payload.Data.Spec.CWD != "/workspace/repo" {
		t.Fatalf("expected cwd /workspace/repo, got %q", payload.Data.Spec.CWD)
	}
	if payload.Data.Spec.Title != defaultACPConversationTitle {
		t.Fatalf("expected default title %q, got %q", defaultACPConversationTitle, payload.Data.Spec.Title)
	}
	if payload.Data.Labels[acpConversationSpritzLabelKey] != "tidy-otter" {
		t.Fatalf("expected spritz label tidy-otter, got %#v", payload.Data.Labels)
	}

	stored := &spritzv1.SpritzConversation{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", payload.Data.Name), stored); err != nil {
		t.Fatalf("expected conversation resource to be persisted: %v", err)
	}
}

func TestCreateACPConversationRejectsAgentsWithoutLoadSessionSupport(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	spritz.Status.ACP.Capabilities.LoadSession = false

	s := newACPTestServer(t, spritz)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations", s.createACPConversation)

	body := strings.NewReader(`{"spritzName":"tidy-otter"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAndPatchACPConversationsByID(t *testing.T) {
	now := metav1.Now()
	older := metav1.NewTime(now.Add(-time.Hour))
	spritz := readyACPSpritz("tidy-otter", "user-1")
	newerConv := conversationFor("tidy-otter-new", "tidy-otter", "user-1", "Latest", now)
	olderConv := conversationFor("tidy-otter-old", "tidy-otter", "user-1", "Earlier", older)
	otherOwner := conversationFor("other-owner", "tidy-otter", "user-2", "Hidden", now)
	otherSpritz := conversationFor("other-spritz", "brisk-fox", "user-1", "Wrong spritz", now)

	s := newACPTestServer(t, spritz, newerConv, olderConv, otherOwner, otherSpritz)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/acp/conversations", s.listACPConversations)
	secured.PATCH("/api/acp/conversations/:id", s.updateACPConversation)
	secured.GET("/api/acp/conversations/:id", s.getACPConversation)

	listReq := httptest.NewRequest(http.MethodGet, "/api/acp/conversations?spritz=tidy-otter", nil)
	listReq.Header.Set("X-Spritz-User-Id", "user-1")
	listRec := httptest.NewRecorder()
	e.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected status 200 from list, got %d: %s", listRec.Code, listRec.Body.String())
	}

	var listPayload struct {
		Status string `json:"status"`
		Data   struct {
			Items []spritzv1.SpritzConversation `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(listPayload.Data.Items) != 2 {
		t.Fatalf("expected 2 visible conversations, got %d", len(listPayload.Data.Items))
	}
	if listPayload.Data.Items[0].Name != newerConv.Name || listPayload.Data.Items[1].Name != olderConv.Name {
		t.Fatalf("expected newest conversation first, got %q then %q", listPayload.Data.Items[0].Name, listPayload.Data.Items[1].Name)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/acp/conversations/"+newerConv.Name, strings.NewReader(`{"title":"Renamed","cwd":"/workspace/app"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("X-Spritz-User-Id", "user-1")
	patchRec := httptest.NewRecorder()
	e.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("expected status 200 from patch, got %d: %s", patchRec.Code, patchRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/acp/conversations/"+newerConv.Name, nil)
	getReq.Header.Set("X-Spritz-User-Id", "user-1")
	getRec := httptest.NewRecorder()
	e.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected status 200 from get, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var getPayload struct {
		Status string                      `json:"status"`
		Data   spritzv1.SpritzConversation `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getPayload); err != nil {
		t.Fatalf("failed to decode get response: %v", err)
	}
	if getPayload.Data.Spec.Title != "Renamed" || getPayload.Data.Spec.SessionID != "sess-tidy-otter-new" || getPayload.Data.Spec.CWD != "/workspace/app" {
		t.Fatalf("expected patched conversation fields, got %#v", getPayload.Data.Spec)
	}
}

func TestListACPConversationsAllowsAdminToSeeAllOwners(t *testing.T) {
	now := metav1.Now()
	spritz := readyACPSpritz("tidy-otter", "user-1")
	ownerOne := conversationFor("tidy-otter-user-1", "tidy-otter", "user-1", "Owner one", now)
	ownerTwo := conversationFor("tidy-otter-user-2", "tidy-otter", "user-2", "Owner two", now)

	s := newACPTestServer(t, spritz, ownerOne, ownerTwo)
	s.auth.adminIDs = map[string]struct{}{"admin-1": {}}
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/acp/conversations", s.listACPConversations)

	req := httptest.NewRequest(http.MethodGet, "/api/acp/conversations?spritz=tidy-otter", nil)
	req.Header.Set("X-Spritz-User-Id", "admin-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Items []spritzv1.SpritzConversation `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(payload.Data.Items) != 2 {
		t.Fatalf("expected 2 visible conversations for admin, got %d", len(payload.Data.Items))
	}
}

func TestPatchACPConversationRejectsSessionIDMutation(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-new", "tidy-otter", "user-1", "Latest", metav1.Now())

	s := newACPTestServer(t, spritz, conversation)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.PATCH("/api/acp/conversations/:id", s.updateACPConversation)

	req := httptest.NewRequest(http.MethodPatch, "/api/acp/conversations/"+conversation.Name, strings.NewReader(`{"sessionId":"sess-new"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListACPAgentsUsesSpecOwnerWhenOwnerLabelMissing(t *testing.T) {
	ownedMissingLabel := readyACPSpritz("tidy-otter", "user-1")
	ownedMissingLabel.Labels = nil
	mislabelledOtherOwner := readyACPSpritz("wrong-owner", "user-2")
	mislabelledOtherOwner.Labels = map[string]string{ownerLabelKey: ownerLabelValue("user-1")}

	s := newACPTestServer(t, ownedMissingLabel, mislabelledOtherOwner)
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
}

func TestBootstrapACPConversationLoadsStoredSessionWithoutMutatingIdentity(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	conversation.Spec.SessionID = "session-existing"
	fakeACP := newFakeACPBootstrapServer(t, fakeACPBootstrapServerOptions{
		LoadReplayUpdates: []map[string]any{
			{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "hello"}},
			{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "world"}},
		},
	})

	s := newACPTestServer(t, spritz, conversation)
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/bootstrap", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string               `json:"status"`
		Data   acpBootstrapResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode bootstrap response: %v", err)
	}
	if payload.Data.EffectiveSessionID != "session-existing" || !payload.Data.Loaded || payload.Data.Replaced {
		t.Fatalf("unexpected bootstrap response: %#v", payload.Data)
	}

	stored := &spritzv1.SpritzConversation{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", conversation.Name), stored); err != nil {
		t.Fatalf("failed to reload conversation: %v", err)
	}
	if stored.Spec.SessionID != "session-existing" {
		t.Fatalf("expected session id to remain unchanged, got %q", stored.Spec.SessionID)
	}
	if stored.Status.BindingState != "active" || stored.Status.BoundSessionID != "session-existing" {
		t.Fatalf("expected active binding state, got %#v", stored.Status)
	}
	if stored.Status.LastReplayMessageCount != 2 {
		t.Fatalf("expected 2 replay messages, got %d", stored.Status.LastReplayMessageCount)
	}

	fakeACP.mu.Lock()
	defer fakeACP.mu.Unlock()
	if len(fakeACP.loadIDs) != 1 || fakeACP.loadIDs[0] != "session-existing" {
		t.Fatalf("expected session/load for session-existing, got %#v", fakeACP.loadIDs)
	}
	if fakeACP.newCalls != 0 {
		t.Fatalf("expected no session/new calls, got %d", fakeACP.newCalls)
	}
}

func TestBootstrapACPConversationRepairsMissingSessionExplicitly(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	conversation.Spec.SessionID = "session-stale"
	fakeACP := newFakeACPBootstrapServer(t, fakeACPBootstrapServerOptions{
		LoadError: &acpBootstrapJSONRPCError{
			Code:    -32603,
			Message: "Internal error",
			Data:    json.RawMessage(`{"details":"Session session-stale not found"}`),
		},
		NewSessionID: "session-fresh",
	})

	s := newACPTestServer(t, spritz, conversation)
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/bootstrap", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string               `json:"status"`
		Data   acpBootstrapResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode bootstrap response: %v", err)
	}
	if payload.Data.EffectiveSessionID != "session-fresh" || payload.Data.Loaded || !payload.Data.Replaced || payload.Data.BindingState != "replaced" {
		t.Fatalf("unexpected bootstrap response: %#v", payload.Data)
	}

	stored := &spritzv1.SpritzConversation{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", conversation.Name), stored); err != nil {
		t.Fatalf("failed to reload conversation: %v", err)
	}
	if stored.Spec.SessionID != "session-fresh" {
		t.Fatalf("expected replaced session id, got %q", stored.Spec.SessionID)
	}
	if stored.Status.BindingState != "replaced" || stored.Status.BoundSessionID != "session-fresh" || stored.Status.PreviousSessionID != "session-stale" {
		t.Fatalf("expected replaced binding status, got %#v", stored.Status)
	}

	fakeACP.mu.Lock()
	defer fakeACP.mu.Unlock()
	if len(fakeACP.loadIDs) != 1 || fakeACP.loadIDs[0] != "session-stale" {
		t.Fatalf("expected session/load for session-stale, got %#v", fakeACP.loadIDs)
	}
	if fakeACP.newCalls != 1 {
		t.Fatalf("expected one session/new call, got %d", fakeACP.newCalls)
	}
}

func TestBootstrapACPConversationRepairsResourceNotFoundSession(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	conversation.Spec.SessionID = "session-stale"
	fakeACP := newFakeACPBootstrapServer(t, fakeACPBootstrapServerOptions{
		LoadError: &acpBootstrapJSONRPCError{
			Code:    -32002,
			Message: "Resource not found: 68eaf10f-a6b2-495c-8dd5-958707901a31",
			Data:    json.RawMessage(`{"sessionId":"session-stale"}`),
		},
		NewSessionID: "session-fresh",
	})

	s := newACPTestServer(t, spritz, conversation)
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/bootstrap", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string               `json:"status"`
		Data   acpBootstrapResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode bootstrap response: %v", err)
	}
	if payload.Data.EffectiveSessionID != "session-fresh" || payload.Data.Loaded || !payload.Data.Replaced || payload.Data.BindingState != "replaced" {
		t.Fatalf("unexpected bootstrap response: %#v", payload.Data)
	}

	stored := &spritzv1.SpritzConversation{}
	if err := s.client.Get(context.Background(), clientKey("spritz-test", conversation.Name), stored); err != nil {
		t.Fatalf("failed to reload conversation: %v", err)
	}
	if stored.Spec.SessionID != "session-fresh" {
		t.Fatalf("expected replaced session id, got %q", stored.Spec.SessionID)
	}
	if stored.Status.BindingState != "replaced" || stored.Status.BoundSessionID != "session-fresh" || stored.Status.PreviousSessionID != "session-stale" {
		t.Fatalf("expected replaced binding status, got %#v", stored.Status)
	}
}

func TestBootstrapACPConversationUsesDefaultNamespaceWhenRequestOmitsIt(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	spritz.Namespace = "default"
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	conversation.Namespace = "default"
	conversation.Spec.SessionID = "session-existing"
	fakeACP := newFakeACPBootstrapServer(t, fakeACPBootstrapServerOptions{})

	s := newACPTestServer(t, spritz, conversation)
	s.namespace = ""
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/bootstrap", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBootstrapACPConversationAdvertisesRichClientCapabilities(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	conversation.Spec.SessionID = "session-existing"
	fakeACP := newFakeACPBootstrapServer(t, fakeACPBootstrapServerOptions{})

	s := newACPTestServer(t, spritz, conversation)
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/bootstrap", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	fakeACP.mu.Lock()
	defer fakeACP.mu.Unlock()
	if len(fakeACP.initRequests) != 1 {
		t.Fatalf("expected one initialize request, got %d", len(fakeACP.initRequests))
	}
	initRequest := fakeACP.initRequests[0]
	authCapabilities, ok := initRequest.ClientCapabilities["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth client capabilities, got %#v", initRequest.ClientCapabilities)
	}
	if authCapabilities["terminal"] != true {
		t.Fatalf("expected terminal auth capability, got %#v", authCapabilities)
	}
	authMeta, ok := authCapabilities["_meta"].(map[string]any)
	if !ok || authMeta["gateway"] != true {
		t.Fatalf("expected gateway auth capability, got %#v", authCapabilities)
	}
	metaCapabilities, ok := initRequest.ClientCapabilities["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected _meta client capabilities, got %#v", initRequest.ClientCapabilities)
	}
	if metaCapabilities["terminal-auth"] != true || metaCapabilities["terminal_output"] != true {
		t.Fatalf("expected terminal metadata capabilities, got %#v", metaCapabilities)
	}
}

func TestBootstrapACPConversationAllowsScopedChannelServicePrincipal(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	conversation.Spec.SessionID = "session-existing"
	markConversationAsChannelBound(conversation, "shared-slack-gateway")
	fakeACP := newFakeACPBootstrapServer(t, fakeACPBootstrapServerOptions{})

	s := newACPTestServer(t, spritz, conversation)
	configureServicePrincipalHeaders(s)
	s.acp.instanceURL = func(namespace, name string) string { return fakeACP.url }

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/bootstrap", nil)
	req.Header.Set("X-Spritz-User-Id", "shared-slack-gateway")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelConversationsUpsert)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOpenACPConversationConnectionAllowsScopedChannelServicePrincipal(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())
	markConversationAsChannelBound(conversation, "shared-slack-gateway")

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("failed to upgrade instance websocket: %v", err)
		}
		defer conn.Close()

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read instance websocket message: %v", err)
		}
		if err := conn.WriteMessage(msgType, []byte(strings.ToUpper(string(payload)))); err != nil {
			t.Fatalf("failed to write instance websocket message: %v", err)
		}
	}))
	defer instance.Close()

	s := newACPTestServer(t, spritz, conversation)
	configureServicePrincipalHeaders(s)
	s.acp.instanceURL = func(namespace, name string) string {
		return "ws" + strings.TrimPrefix(instance.URL, "http")
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/acp/conversations/:id/connect", s.openACPConversationConnection)
	proxy := httptest.NewServer(e)
	defer proxy.Close()

	proxyURL := strings.TrimPrefix(proxy.URL, "http")
	wsURL := "ws" + proxyURL + "/api/acp/conversations/" + conversation.Name + "/connect"
	headers := http.Header{}
	headers.Set("X-Spritz-User-Id", "shared-slack-gateway")
	headers.Set("X-Spritz-Principal-Type", "service")
	headers.Set("X-Spritz-Principal-Scopes", scopeChannelConversationsUpsert)
	headers.Set("Origin", proxy.URL)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("expected websocket dial to succeed, got %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("failed to write websocket message: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read websocket message: %v", err)
	}
	if string(payload) != "PING" {
		t.Fatalf("expected websocket echo PING, got %q", string(payload))
	}
}
