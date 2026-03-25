package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	req.Header.Set("X-Spritz-User-Id", "owner-123")
	return req
}

func newChannelConversationsServiceRequest(body string) *http.Request {
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
		"principalId":"shared-slack-gateway",
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
		"principalId":"shared-slack-gateway",
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

func TestUpsertChannelConversationPersistsAndResolvesReplyAliases(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	createRec := httptest.NewRecorder()
	e.ServeHTTP(createRec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100",
		"title":"Slack concierge"
	}`))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected first request to create, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var createPayload struct {
		Data struct {
			Conversation spritzv1.SpritzConversation `json:"conversation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}

	aliasRec := httptest.NewRecorder()
	e.ServeHTTP(aliasRec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"conversationId":"`+createPayload.Data.Conversation.Name+`",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387376.000100",
		"title":"Slack concierge"
	}`))
	if aliasRec.Code != http.StatusOK {
		t.Fatalf("expected alias request to reuse, got %d: %s", aliasRec.Code, aliasRec.Body.String())
	}

	reuseRec := httptest.NewRecorder()
	e.ServeHTTP(reuseRec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387376.000100",
		"title":"Slack concierge"
	}`))
	if reuseRec.Code != http.StatusOK {
		t.Fatalf("expected alias lookup to reuse, got %d: %s", reuseRec.Code, reuseRec.Body.String())
	}

	var reusePayload struct {
		Data struct {
			Created      bool                        `json:"created"`
			Conversation spritzv1.SpritzConversation `json:"conversation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(reuseRec.Body.Bytes(), &reusePayload); err != nil {
		t.Fatalf("failed to decode reuse response: %v", err)
	}
	if reusePayload.Data.Created {
		t.Fatalf("expected alias lookup to reuse the original conversation")
	}
	if reusePayload.Data.Conversation.Name != createPayload.Data.Conversation.Name {
		t.Fatalf("expected alias lookup to reuse %q, got %q", createPayload.Data.Conversation.Name, reusePayload.Data.Conversation.Name)
	}
	aliases := channelConversationExternalConversationAliases(&reusePayload.Data.Conversation)
	if len(aliases) != 1 || aliases[0] != "1711387376.000100" {
		t.Fatalf("expected persisted alias, got %#v", aliases)
	}
}

func legacyChannelConversationRouteHash(identity normalizedChannelConversationIdentity, ownerID, instanceID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		identity.principalID,
		identity.provider,
		identity.externalScopeType,
		identity.externalTenantID,
		identity.externalChannelID,
		identity.externalConversationID,
		strings.TrimSpace(ownerID),
		strings.TrimSpace(instanceID),
	}, "\n")))
	return hex.EncodeToString(sum[:16])
}

func TestUpsertChannelConversationReusesLegacyConversationWithoutBaseRouteLabel(t *testing.T) {
	spritz := readyACPSpritz("zeno-acme", "owner-123")
	identity := normalizedChannelConversationIdentity{
		principalID:            "shared-slack-gateway",
		provider:               "slack",
		externalScopeType:      "workspace",
		externalTenantID:       "T_workspace_1",
		externalChannelID:      "C_channel_1",
		externalConversationID: "1711387375.000100",
	}
	conversation, err := buildACPConversationResource(spritz, "Slack concierge", "")
	if err != nil {
		t.Fatalf("build conversation: %v", err)
	}
	conversation.Name = channelConversationName(spritz.Name, spritz.Spec.Owner.ID, identity)
	conversation.Spec.Owner = spritz.Spec.Owner
	conversation.Spec.SpritzName = spritz.Name
	conversation.Labels = map[string]string{
		acpConversationLabelKey:       acpConversationLabelValue,
		acpConversationOwnerLabelKey:  ownerLabelValue(spritz.Spec.Owner.ID),
		acpConversationSpritzLabelKey: spritz.Name,
		channelConversationRouteLabelKey: legacyChannelConversationRouteHash(
			identity,
			spritz.Spec.Owner.ID,
			spritz.Name,
		),
	}
	conversation.Annotations = map[string]string{
		channelConversationPrincipalAnnotationKey:              identity.principalID,
		channelConversationProviderAnnotationKey:               identity.provider,
		channelConversationExternalScopeTypeAnnotationKey:      identity.externalScopeType,
		channelConversationExternalTenantIDAnnotationKey:       identity.externalTenantID,
		channelConversationExternalChannelIDAnnotationKey:      identity.externalChannelID,
		channelConversationExternalConversationIDAnnotationKey: identity.externalConversationID,
		requestIDAnnotationKey:                                 "legacy-request",
	}

	s := newChannelConversationsTestServer(t, spritz, conversation)
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100",
		"title":"Slack concierge"
	}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected legacy conversation reuse, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data struct {
			Created      bool                        `json:"created"`
			Conversation spritzv1.SpritzConversation `json:"conversation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Data.Created {
		t.Fatalf("expected legacy conversation to be reused")
	}
	if payload.Data.Conversation.Name != conversation.Name {
		t.Fatalf("expected legacy conversation %q, got %q", conversation.Name, payload.Data.Conversation.Name)
	}
}

func TestUpsertChannelConversationRejectsAliasForWrongSpritzConversation(t *testing.T) {
	targetSpritz := readyACPSpritz("zeno-acme", "owner-123")
	otherSpritz := readyACPSpritz("zeno-other", "owner-999")
	identity := normalizedChannelConversationIdentity{
		principalID:            "shared-slack-gateway",
		provider:               "slack",
		externalScopeType:      "workspace",
		externalTenantID:       "T_workspace_1",
		externalChannelID:      "C_channel_1",
		externalConversationID: "1711387375.000100",
	}
	otherConversation, err := buildACPConversationResource(otherSpritz, "Slack concierge", "")
	if err != nil {
		t.Fatalf("build conversation: %v", err)
	}
	otherConversation.Name = channelConversationName(otherSpritz.Name, otherSpritz.Spec.Owner.ID, identity)
	applyChannelConversationMetadata(otherConversation, identity, "other-request", otherSpritz)

	s := newChannelConversationsTestServer(t, targetSpritz, otherSpritz, otherConversation)
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"conversationId":"`+otherConversation.Name+`",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387376.000100",
		"title":"Slack concierge"
	}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected alias against wrong spritz to conflict, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertChannelConversationRejectsAliasWhenAnotherConversationAlreadyOwnsIt(t *testing.T) {
	spritz := readyACPSpritz("zeno-acme", "owner-123")
	rootIdentity := normalizedChannelConversationIdentity{
		principalID:            "shared-slack-gateway",
		provider:               "slack",
		externalScopeType:      "workspace",
		externalTenantID:       "T_workspace_1",
		externalChannelID:      "C_channel_1",
		externalConversationID: "1711387375.000100",
	}
	aliasIdentity := rootIdentity
	aliasIdentity.externalConversationID = "1711387376.000100"

	rootConversation, err := buildACPConversationResource(spritz, "Slack concierge", "")
	if err != nil {
		t.Fatalf("build root conversation: %v", err)
	}
	rootConversation.Name = channelConversationName(spritz.Name, spritz.Spec.Owner.ID, rootIdentity)
	applyChannelConversationMetadata(rootConversation, rootIdentity, "root-request", spritz)

	aliasConversation, err := buildACPConversationResource(spritz, "Slack concierge", "")
	if err != nil {
		t.Fatalf("build alias conversation: %v", err)
	}
	aliasConversation.Name = channelConversationName(spritz.Name, spritz.Spec.Owner.ID, aliasIdentity)
	applyChannelConversationMetadata(aliasConversation, aliasIdentity, "alias-request", spritz)

	s := newChannelConversationsTestServer(t, spritz, rootConversation, aliasConversation)
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"conversationId":"`+rootConversation.Name+`",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387376.000100",
		"title":"Slack concierge"
	}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected alias conflict when another conversation already owns it, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertChannelConversationRejectsWhenExactAndAliasedMatchesConflict(t *testing.T) {
	spritz := readyACPSpritz("zeno-acme", "owner-123")
	exactIdentity := normalizedChannelConversationIdentity{
		principalID:            "shared-slack-gateway",
		provider:               "slack",
		externalScopeType:      "workspace",
		externalTenantID:       "T_workspace_1",
		externalChannelID:      "C_channel_1",
		externalConversationID: "1711387376.000100",
	}
	aliasedIdentity := exactIdentity
	aliasedIdentity.externalConversationID = "1711387375.000100"

	exactConversation, err := buildACPConversationResource(spritz, "Slack concierge", "")
	if err != nil {
		t.Fatalf("build exact conversation: %v", err)
	}
	exactConversation.Name = channelConversationName(spritz.Name, spritz.Spec.Owner.ID, exactIdentity)
	applyChannelConversationMetadata(exactConversation, exactIdentity, "exact-request", spritz)

	aliasedConversation, err := buildACPConversationResource(spritz, "Slack concierge", "")
	if err != nil {
		t.Fatalf("build aliased conversation: %v", err)
	}
	aliasedConversation.Name = channelConversationName(spritz.Name, spritz.Spec.Owner.ID, aliasedIdentity)
	applyChannelConversationMetadata(aliasedConversation, aliasedIdentity, "aliased-request", spritz)
	if _, err := appendChannelConversationAlias(aliasedConversation, exactIdentity.externalConversationID); err != nil {
		t.Fatalf("append alias: %v", err)
	}

	s := newChannelConversationsTestServer(t, spritz, exactConversation, aliasedConversation)
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387376.000100",
		"title":"Slack concierge"
	}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected ambiguity conflict when exact and aliased matches disagree, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertChannelConversationPreservesExistingTitleAndCWD(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	firstRec := httptest.NewRecorder()
	e.ServeHTTP(firstRec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
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
		"principalId":"shared-slack-gateway",
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

func TestUpsertChannelConversationRejectsScopedServicePrincipal(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsServiceRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100"
	}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for scoped service principal, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertChannelConversationRejectsOwnerMismatch(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
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

func TestUpsertChannelConversationAllowsAuthDisabledOwnerBoundRequest(t *testing.T) {
	s := newChannelConversationsTestServer(t, readyACPSpritz("zeno-acme", "owner-123"))
	s.auth.mode = authModeNone
	e := echo.New()
	s.registerRoutes(e)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, newChannelConversationsRequest(`{
		"principalId":"shared-slack-gateway",
		"instanceId":"zeno-acme",
		"ownerId":"owner-123",
		"provider":"slack",
		"externalScopeType":"workspace",
		"externalTenantId":"T_workspace_1",
		"externalChannelId":"C_channel_1",
		"externalConversationId":"1711387375.000100"
	}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when auth is disabled, got %d: %s", rec.Code, rec.Body.String())
	}
}
