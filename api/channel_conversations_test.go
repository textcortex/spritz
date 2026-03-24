package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newChannelConversationsTestServer(t *testing.T, objects ...client.Object) *server {
	t.Helper()
	s := newACPTestServer(t, objects...)
	s.auth.headerType = "X-Spritz-Principal-Type"
	s.auth.headerScopes = "X-Spritz-Principal-Scopes"
	s.auth.headerTrustTypeAndScopes = true
	return s
}

func newChannelConversationsRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/channel-conversations/upsert", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "shared-slack-gateway")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelConversationsUpsert)
	return req
}

func TestUpsertChannelConversationCreatesConversation(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100",
		"title":"Slack concierge",
		"cwd":"/workspace",
		"requestId":"slack-msg-1"
	}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Created      bool                        `json:"created"`
			Conversation spritzv1.SpritzConversation `json:"conversation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Status != "success" {
		t.Fatalf("expected jsend success, got %#v", payload.Status)
	}
	if !payload.Data.Created {
		t.Fatalf("expected created=true")
	}
	if payload.Data.Conversation.Spec.SpritzName != "zeno-acme" {
		t.Fatalf("expected spritz name zeno-acme, got %q", payload.Data.Conversation.Spec.SpritzName)
	}
	if payload.Data.Conversation.Spec.Owner.ID != "owner-123" {
		t.Fatalf("expected owner owner-123, got %q", payload.Data.Conversation.Spec.Owner.ID)
	}
	expectedName := channelConversationName(
		"zeno-acme",
		"owner-123",
		normalizedChannelConversationIdentity{
			principalID:            "shared-slack-gateway",
			provider:               "slack",
			externalScopeType:      "workspace",
			externalTenantID:       "T_workspace_1",
			externalChannelID:      "C_channel_1",
			externalConversationID: "1711387375.000100",
		},
	)
	if payload.Data.Conversation.Name != expectedName {
		t.Fatalf("expected deterministic conversation name %q, got %q", expectedName, payload.Data.Conversation.Name)
	}
	if payload.Data.Conversation.Annotations[channelConversationPrincipalAnnotationKey] != "shared-slack-gateway" {
		t.Fatalf("expected principal annotation, got %#v", payload.Data.Conversation.Annotations[channelConversationPrincipalAnnotationKey])
	}
	if payload.Data.Conversation.Annotations[channelConversationExternalChannelIDAnnotationKey] != "C_channel_1" {
		t.Fatalf("expected channel annotation, got %#v", payload.Data.Conversation.Annotations[channelConversationExternalChannelIDAnnotationKey])
	}
	if payload.Data.Conversation.Annotations[channelConversationExternalConversationIDAnnotationKey] != "1711387375.000100" {
		t.Fatalf("expected conversation annotation, got %#v", payload.Data.Conversation.Annotations[channelConversationExternalConversationIDAnnotationKey])
	}
	if payload.Data.Conversation.Labels[channelConversationRouteLabelKey] == "" {
		t.Fatalf("expected route label to be set")
	}
}

func TestChannelConversationNameIncludesOwner(t *testing.T) {
	identity := normalizedChannelConversationIdentity{
		principalID:            "shared-slack-gateway",
		provider:               "slack",
		externalScopeType:      "workspace",
		externalTenantID:       "T_workspace_1",
		externalChannelID:      "C_channel_1",
		externalConversationID: "1711387375.000100",
	}

	first := channelConversationName("zeno-acme", "owner-123", identity)
	second := channelConversationName("zeno-acme", "owner-456", identity)

	if first == second {
		t.Fatalf("expected owner-specific conversation names, got %q", first)
	}
}

func TestUpsertChannelConversationReusesExistingConversation(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100",
		"title":"Slack concierge"
	}`

	firstRec := httptest.NewRecorder()
	e.ServeHTTP(firstRec, newChannelConversationsRequest(body))
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("expected first request to create, got %d: %s", firstRec.Code, firstRec.Body.String())
	}

	secondRec := httptest.NewRecorder()
	e.ServeHTTP(secondRec, newChannelConversationsRequest(body))
	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected second request to reuse, got %d: %s", secondRec.Code, secondRec.Body.String())
	}

	var secondPayload struct {
		Status string `json:"status"`
		Data   struct {
			Created      bool                        `json:"created"`
			Conversation spritzv1.SpritzConversation `json:"conversation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(secondRec.Body.Bytes(), &secondPayload); err != nil {
		t.Fatalf("failed to decode second response: %v", err)
	}
	if secondPayload.Status != "success" {
		t.Fatalf("expected jsend success, got %#v", secondPayload.Status)
	}
	if secondPayload.Data.Created {
		t.Fatalf("expected created=false on reuse")
	}

	list := &spritzv1.SpritzConversationList{}
	if err := s.client.List(context.Background(), list, client.InNamespace("spritz-test")); err != nil {
		t.Fatalf("failed to list conversations: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected one persisted conversation, got %d", len(list.Items))
	}
}

func TestUpsertChannelConversationPreservesExistingTitleAndCWD(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	firstRec := httptest.NewRecorder()
	e.ServeHTTP(firstRec, newChannelConversationsRequest(`{
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100",
		"title":"Original Slack concierge",
		"cwd":"/workspace/original"
	}`))
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("expected first request to create, got %d: %s", firstRec.Code, firstRec.Body.String())
	}

	secondRec := httptest.NewRecorder()
	e.ServeHTTP(secondRec, newChannelConversationsRequest(`{
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100",
		"title":"Generated Slack title",
		"cwd":"/home/dev"
	}`))
	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected second request to reuse, got %d: %s", secondRec.Code, secondRec.Body.String())
	}

	list := &spritzv1.SpritzConversationList{}
	if err := s.client.List(context.Background(), list, client.InNamespace("spritz-test")); err != nil {
		t.Fatalf("failed to list conversations: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected one persisted conversation, got %d", len(list.Items))
	}
	if list.Items[0].Spec.Title != "Original Slack concierge" {
		t.Fatalf("expected title to be preserved, got %q", list.Items[0].Spec.Title)
	}
	if list.Items[0].Spec.CWD != "/workspace/original" {
		t.Fatalf("expected cwd to be preserved, got %q", list.Items[0].Spec.CWD)
	}
}

func TestUpsertChannelConversationRejectsMissingScope(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-conversations/upsert", strings.NewReader(`{
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100"
	}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Spritz-User-Id", "shared-slack-gateway")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without scope, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertChannelConversationRejectsOwnerMismatch(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"instanceId":"zeno-acme",
		"ownerId":"owner-456",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100"
	}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for owner mismatch, got %d: %s", rec.Code, rec.Body.String())
	}
}
