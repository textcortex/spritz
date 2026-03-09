package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			enabled: true,
			port:    spritzv1.DefaultACPPort,
			path:    spritzv1.DefaultACPPath,
		},
	}
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

func TestListACPAgentsUsesStoredStatusOnly(t *testing.T) {
	ready := readyACPSpritz("tidy-otter", "user-1")
	ready.Status.ACP.LastProbeAt = nil
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

	s := newACPTestServer(t, ready, ignored)
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

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/acp/conversations/"+newerConv.Name, strings.NewReader(`{"title":"Renamed","sessionId":"sess-new","cwd":"/workspace/app"}`))
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
	if getPayload.Data.Spec.Title != "Renamed" || getPayload.Data.Spec.SessionID != "sess-new" || getPayload.Data.Spec.CWD != "/workspace/app" {
		t.Fatalf("expected patched conversation fields, got %#v", getPayload.Data.Spec)
	}
}
