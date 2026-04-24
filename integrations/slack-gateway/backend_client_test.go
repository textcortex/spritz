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

func TestListInstallTargetsUsesBackendFastAPIBaseURLWhenConfigured(t *testing.T) {
	backendHits := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHits++
		t.Fatalf("unexpected backend base request to %s", r.URL.Path)
	}))
	defer backend.Close()

	fastapiHits := 0
	fastapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-install-targets/list" {
			t.Fatalf("unexpected fastapi path %s", r.URL.Path)
		}
		fastapiHits++
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"targets": []map[string]any{
				{
					"id": "ag_workspace",
					"profile": map[string]any{
						"name": "Workspace Helper",
					},
					"presetInputs": map[string]any{
						"agentId": "ag_workspace",
					},
				},
			},
		})
	}))
	defer fastapi.Close()

	gateway := newSlackGateway(config{
		BackendBaseURL:        backend.URL,
		BackendFastAPIBaseURL: fastapi.URL,
		BackendInternalToken:  "backend-internal-token",
		PresetID:              "zeno",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	targets, err := gateway.listInstallTargets(
		context.Background(),
		&slackInstallation{
			TeamID:           "T_workspace_1",
			InstallingUserID: "U_installer",
		},
		"req_install_1",
	)
	if err != nil {
		t.Fatalf("list install targets: %v", err)
	}
	if backendHits != 0 {
		t.Fatalf("expected backend base URL to be unused, got %d hits", backendHits)
	}
	if fastapiHits != 1 {
		t.Fatalf("expected fastapi base URL to be hit once, got %d", fastapiHits)
	}
	if len(targets) != 1 || targets[0].ID != "ag_workspace" {
		t.Fatalf("unexpected install targets: %#v", targets)
	}
}

func TestListManagedInstallationsUsesFastAPIBaseURL(t *testing.T) {
	fastapiHits := 0
	fastapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected fastapi path %s", r.URL.Path)
		}
		fastapiHits++
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"installations": []map[string]any{
				{
					"route": map[string]any{
						"principalId":       "shared-slack-gateway",
						"provider":          "slack",
						"externalScopeType": "workspace",
						"externalTenantId":  "T_workspace_1",
					},
					"state": "ready",
					"currentTarget": map[string]any{
						"id": "ag_workspace",
						"profile": map[string]any{
							"name": "Workspace Helper",
						},
						"ownerLabel": "Personal",
					},
					"allowedActions": []string{"changeTarget", "disconnect"},
				},
			},
		})
	}))
	defer fastapi.Close()

	gateway := newSlackGateway(config{
		BackendBaseURL:        "https://unused.example.test",
		BackendFastAPIBaseURL: fastapi.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	installations, err := gateway.listManagedInstallations(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("list managed installations: %v", err)
	}
	if fastapiHits != 1 {
		t.Fatalf("expected fastapi base URL to be hit once, got %d", fastapiHits)
	}
	if len(installations) != 1 || installations[0].Route.ExternalTenantID != "T_workspace_1" {
		t.Fatalf("unexpected managed installations: %#v", installations)
	}
}

func TestUpdateManagedInstallationTargetPostsExpectedPayload(t *testing.T) {
	var updatePayload map[string]any
	fastapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/target/update" {
			t.Fatalf("unexpected fastapi path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&updatePayload); err != nil {
			t.Fatalf("decode update payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "resolved",
			"needsProvisioning": true,
		})
	}))
	defer fastapi.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: fastapi.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := gateway.updateManagedInstallationTarget(
		context.Background(),
		"user-1",
		"T_workspace_1",
		"req-manage-1",
		map[string]any{"agentId": "ag_workspace"},
	); err != nil {
		t.Fatalf("update managed installation target: %v", err)
	}
	if updatePayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id to be forwarded, got %#v", updatePayload["callerAuthId"])
	}
	if updatePayload["externalTenantId"] != "T_workspace_1" {
		t.Fatalf("expected team id to be forwarded, got %#v", updatePayload["externalTenantId"])
	}
}

func TestUpdateManagedInstallationConfigPostsExpectedPayload(t *testing.T) {
	var updatePayload map[string]any
	fastapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/config/update" {
			t.Fatalf("unexpected fastapi path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&updatePayload); err != nil {
			t.Fatalf("decode update payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "resolved",
			"needsProvisioning": true,
		})
	}))
	defer fastapi.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: fastapi.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	requireMention := false
	if err := gateway.updateManagedInstallationConfig(
		context.Background(),
		"user-1",
		"T_workspace_1",
		"req-manage-config-1",
		installationConfig{
			ChannelPolicies: []installationChannelPolicy{
				{ExternalChannelID: "C_channel_1", RequireMention: &requireMention},
			},
		},
	); err != nil {
		t.Fatalf("update managed installation config: %v", err)
	}
	if updatePayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id to be forwarded, got %#v", updatePayload["callerAuthId"])
	}
	if updatePayload["externalTenantId"] != "T_workspace_1" {
		t.Fatalf("expected team id to be forwarded, got %#v", updatePayload["externalTenantId"])
	}
	configPayload, ok := updatePayload["installationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected installationConfig payload, got %#v", updatePayload["installationConfig"])
	}
	policies, ok := configPayload["channelPolicies"].([]any)
	if !ok || len(policies) != 1 {
		t.Fatalf("expected one channel policy, got %#v", configPayload["channelPolicies"])
	}
	policy, ok := policies[0].(map[string]any)
	if !ok || policy["externalChannelId"] != "C_channel_1" || policy["requireMention"] != false {
		t.Fatalf("unexpected channel policy: %#v", policies[0])
	}
}

func TestManagedChannelRoutesDefaultMissingBooleansSafely(t *testing.T) {
	var connection backendManagedConnection
	if err := json.Unmarshal(
		[]byte(`{"id":"cc_1","routes":[{"externalChannelId":"C_default"}]}`),
		&connection,
	); err != nil {
		t.Fatalf("decode connection: %v", err)
	}

	policies := channelPoliciesFromConnection(connection)
	if len(policies) != 1 {
		t.Fatalf("expected route with omitted enabled flag to stay enabled, got %#v", policies)
	}
	if policies[0].RequireMention == nil || !*policies[0].RequireMention {
		t.Fatalf("expected omitted requireMention to default to true, got %#v", policies[0])
	}
	rows := channelRouteSettingsRows(connection)
	if len(rows) != 1 || rows[0].ModeLabel != "Mentions required" {
		t.Fatalf("expected settings row to render as mention-required, got %#v", rows)
	}
}

func TestChannelSessionUnavailablePolicySnapshotRequiresStructuredPayload(t *testing.T) {
	snapshot, ok := channelSessionUnavailablePolicySnapshot(&channelSessionUnavailableError{
		cause: &httpStatusError{
			method:     http.MethodPost,
			endpoint:   "/internal/v1/spritz/channel-sessions/exchange",
			statusCode: http.StatusServiceUnavailable,
			body:       "temporarily unavailable",
		},
	})
	if ok {
		t.Fatalf("expected unstructured 503 to have no policy snapshot, got %#v", snapshot)
	}

	requireMention := false
	snapshot, ok = channelSessionUnavailablePolicySnapshot(&channelSessionUnavailableError{
		providerAuth: slackInstallation{BotUserID: "U_bot"},
		installationConfig: installationConfig{
			ChannelPolicies: []installationChannelPolicy{
				{ExternalChannelID: "C_channel_1", RequireMention: &requireMention},
			},
		},
		hasPolicySnapshot: true,
	})
	if !ok {
		t.Fatalf("expected structured unavailable response to have a policy snapshot")
	}
	if snapshot.botUserID != "U_bot" || len(snapshot.config.ChannelPolicies) != 1 {
		t.Fatalf("unexpected policy snapshot: %#v", snapshot)
	}
}

func TestDisconnectManagedInstallationPostsExpectedPayload(t *testing.T) {
	var disconnectPayload map[string]any
	fastapi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/disconnect" {
			t.Fatalf("unexpected fastapi path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&disconnectPayload); err != nil {
			t.Fatalf("decode disconnect payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "resolved"})
	}))
	defer fastapi.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: fastapi.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := gateway.disconnectManagedInstallation(context.Background(), "user-1", "T_workspace_1"); err != nil {
		t.Fatalf("disconnect managed installation: %v", err)
	}
	if disconnectPayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id to be forwarded, got %#v", disconnectPayload["callerAuthId"])
	}
	if disconnectPayload["externalTenantId"] != "T_workspace_1" {
		t.Fatalf("expected team id to be forwarded, got %#v", disconnectPayload["externalTenantId"])
	}
}
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
