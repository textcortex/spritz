package main

import (
	"bytes"
	"context"
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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"spritz.sh/acptext"
)

func TestOAuthCallbackAutoSelectsSingleInstallTargetAndUpsertsRegistry(t *testing.T) {
	var upsertPayload map[string]any
	listHits := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-install-targets/list":
			listHits++
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"targets": []map[string]any{
					{
						"id": "ag_123",
						"profile": map[string]any{
							"name": "Workspace Helper",
						},
						"ownerLabel": "Personal",
						"presetInputs": map[string]any{
							"agentId": "ag_123",
						},
					},
				},
			})
		case "/internal/v1/spritz/channel-installations/upsert":
			if err := json.NewDecoder(r.Body).Decode(&upsertPayload); err != nil {
				t.Fatalf("decode backend payload: %v", err)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"installation": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
				},
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
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
		PublicURL:            publicURL,
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("expected callback redirect location")
	}
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	if redirectURL.Path != "/slack/install/result" {
		t.Fatalf("expected install result path, got %s", redirectURL.Path)
	}
	if got := redirectURL.Query().Get("status"); got != "success" {
		t.Fatalf("expected success status, got %q", got)
	}
	if got := redirectURL.Query().Get("code"); got != "installed" {
		t.Fatalf("expected installed code, got %q", got)
	}
	if got := redirectURL.Query().Get("provider"); got != "slack" {
		t.Fatalf("expected slack provider, got %q", got)
	}
	if got := redirectURL.Query().Get("teamId"); got != "T_workspace_1" {
		t.Fatalf("expected team id in result redirect, got %q", got)
	}
	requestID := redirectURL.Query().Get("requestId")
	if strings.TrimSpace(requestID) == "" {
		t.Fatal("expected request id in result redirect")
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
	providerAuth, ok := upsertPayload["providerAuth"].(map[string]any)
	if !ok {
		t.Fatalf("expected providerAuth object, got %#v", upsertPayload["providerAuth"])
	}
	if providerAuth["botAccessToken"] != "xoxb-installed" {
		t.Fatalf("expected bot access token to be forwarded, got %#v", providerAuth["botAccessToken"])
	}
	if providerAuth["botUserId"] != "U_bot" {
		t.Fatalf("expected bot user id to be forwarded, got %#v", providerAuth["botUserId"])
	}
	if upsertPayload["presetId"] != "zeno" {
		t.Fatalf("expected presetId zeno, got %#v", upsertPayload["presetId"])
	}
	if upsertPayload["requestId"] != requestID {
		t.Fatalf("expected requestId to propagate to backend, got %#v", upsertPayload["requestId"])
	}
	presetInputs, ok := upsertPayload["presetInputs"].(map[string]any)
	if !ok {
		t.Fatalf("expected presetInputs object, got %#v", upsertPayload["presetInputs"])
	}
	if presetInputs["agentId"] != "ag_123" {
		t.Fatalf("expected selected agentId ag_123, got %#v", presetInputs["agentId"])
	}
	if listHits != 1 {
		t.Fatalf("expected install targets to be listed once, got %d", listHits)
	}

	resultReq := httptest.NewRequest(http.MethodGet, redirectURL.RequestURI(), nil)
	resultRec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(resultRec, resultReq)

	if resultRec.Code != http.StatusSeeOther {
		t.Fatalf("expected result redirect, got %d: %s", resultRec.Code, resultRec.Body.String())
	}
	resultLocation, err := url.Parse(resultRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse result redirect: %v", err)
	}
	if resultLocation.Scheme != "https" || resultLocation.Host != "spritz.example.test" {
		t.Fatalf("expected result redirect to use Spritz host, got %s", resultLocation.String())
	}
	if resultLocation.Path != "/settings/slack/install/result" {
		t.Fatalf("expected React result route, got %q", resultLocation.Path)
	}
	if resultLocation.Query().Get("requestId") != requestID {
		t.Fatalf("expected request id in result redirect, got %q", resultLocation.RawQuery)
	}
}

func TestOAuthCallbackRedirectsToReactInstallTargetPickerWhenSameOrigin(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-install-targets/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"targets": []map[string]any{
					{
						"id": "ag_123",
						"profile": map[string]any{
							"name": "Personal Helper",
						},
						"ownerLabel": "Personal",
						"presetInputs": map[string]any{
							"agentId": "ag_123",
						},
					},
					{
						"id": "ag_456",
						"profile": map[string]any{
							"name": "Org Helper",
						},
						"ownerLabel": "Acme Workspace",
						"presetInputs": map[string]any{
							"agentId": "ag_456",
						},
					},
				},
			})
		case "/internal/v1/spritz/channel-installations/upsert":
			t.Fatal("upsert should not happen before the picker selection")
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"app_id":       "A_app_1",
			"scope":        "chat:write",
			"access_token": "xoxb-installed",
			"bot_user_id":  "U_bot",
			"team":         map[string]any{"id": "T_workspace_1"},
			"authed_user":  map[string]any{"id": "U_installer"},
		})
	}))
	defer slackAPI.Close()

	gateway := newSlackGateway(config{
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://gateway.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected picker redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	redirectURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse picker redirect: %v", err)
	}
	if redirectURL.Scheme != "https" || redirectURL.Host != "gateway.example.test" {
		t.Fatalf("expected picker redirect to use Spritz host, got %s", redirectURL.String())
	}
	if redirectURL.Path != "/settings/slack/install/select" {
		t.Fatalf("expected React picker route, got %q", redirectURL.Path)
	}
	requestID := redirectURL.Query().Get("requestId")
	if requestID == "" {
		t.Fatalf("expected picker redirect to include requestId, got %q", redirectURL.RawQuery)
	}
	if strings.Contains(redirectURL.RawQuery, "state=") {
		t.Fatalf("expected picker redirect without pending state query, got %q", redirectURL.RawQuery)
	}
	var pendingCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == pendingInstallCookieNameForRequest(requestID) {
			pendingCookie = cookie
			break
		}
	}
	if pendingCookie == nil || pendingCookie.Value == "" {
		t.Fatalf("expected pending install state cookie")
	}
	if !pendingCookie.HttpOnly || !pendingCookie.Secure {
		t.Fatalf("expected secure http-only pending install cookie, got %#v", pendingCookie)
	}
	if strings.Contains(pendingCookie.Value, "xoxb-installed") {
		t.Fatalf("expected pending install cookie to keep bot token encrypted")
	}
	if strings.Contains(rec.Body.String(), "xoxb-installed") {
		t.Fatalf("expected picker redirect to keep bot token encrypted, got %q", rec.Body.String())
	}

	selectionReq := httptest.NewRequest(
		http.MethodGet,
		"/api/slack/install/selection?requestId="+url.QueryEscape(requestID),
		nil,
	)
	selectionReq.AddCookie(pendingCookie)
	selectionRec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(selectionRec, selectionReq)

	if selectionRec.Code != http.StatusOK {
		t.Fatalf("expected selection API response, got %d: %s", selectionRec.Code, selectionRec.Body.String())
	}
	if strings.Contains(selectionRec.Body.String(), "xoxb-installed") || strings.Contains(selectionRec.Body.String(), "botAccessToken") {
		t.Fatalf("selection API leaked bot token material: %s", selectionRec.Body.String())
	}
	var selectionPayload map[string]any
	if err := json.NewDecoder(selectionRec.Body).Decode(&selectionPayload); err != nil {
		t.Fatalf("decode selection API payload: %v", err)
	}
	if _, ok := selectionPayload["installation"]; ok {
		t.Fatalf("selection API should not expose pending installation payload: %#v", selectionPayload["installation"])
	}
}

func TestOAuthCallbackRendersGatewayInstallTargetPickerWhenCrossOrigin(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-install-targets/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"targets": []map[string]any{
					{
						"id": "ag_123",
						"profile": map[string]any{
							"name": "Personal Helper",
						},
						"ownerLabel": "Personal",
						"presetInputs": map[string]any{
							"agentId": "ag_123",
						},
					},
					{
						"id": "ag_456",
						"profile": map[string]any{
							"name": "Workspace Helper",
						},
						"ownerLabel": "Workspace",
						"presetInputs": map[string]any{
							"agentId": "ag_456",
						},
					},
				},
			})
		case "/internal/v1/spritz/channel-installations/upsert":
			t.Fatal("upsert should not happen before the picker selection")
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"app_id":       "A_app_1",
			"scope":        "chat:write",
			"access_token": "xoxb-installed",
			"bot_user_id":  "U_bot",
			"team":         map[string]any{"id": "T_workspace_1"},
			"authed_user":  map[string]any{"id": "U_installer"},
		})
	}))
	defer slackAPI.Close()

	gateway := newSlackGateway(config{
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected gateway picker page, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Choose an install target") {
		t.Fatalf("expected picker title, got %q", body)
	}
	if !strings.Contains(body, "Personal Helper") || !strings.Contains(body, "Workspace Helper") {
		t.Fatalf("expected picker targets, got %q", body)
	}
	if !strings.Contains(body, `/slack/install/select`) {
		t.Fatalf("expected picker form action, got %q", body)
	}
	if strings.Contains(body, "xoxb-installed") {
		t.Fatalf("expected picker state to keep bot token encrypted, got %q", body)
	}
	for _, cookie := range rec.Result().Cookies() {
		if strings.HasPrefix(cookie.Name, pendingInstallCookieName) {
			t.Fatalf("expected cross-origin picker to avoid pending install cookie, got %#v", cookie)
		}
	}
}

func TestInstallTargetSelectionAPIUsesRequestScopedPendingCookie(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-install-targets/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
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
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendBaseURL:        backend.URL,
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		OAuthStateSecret:      "oauth-state-secret",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pendingStateA, err := gateway.state.generatePendingInstall(pendingInstallState{
		RequestID: "install-request-a",
		Installation: slackInstallation{
			TeamID:           "T_workspace_a",
			InstallingUserID: "U_installer",
			BotAccessToken:   "xoxb-installed-a",
			BotUserID:        "U_bot",
			APIAppID:         "A_app_1",
		},
	})
	if err != nil {
		t.Fatalf("generate first pending install state failed: %v", err)
	}
	pendingStateB, err := gateway.state.generatePendingInstall(pendingInstallState{
		RequestID: "install-request-b",
		Installation: slackInstallation{
			TeamID:           "T_workspace_b",
			InstallingUserID: "U_installer",
			BotAccessToken:   "xoxb-installed-b",
			BotUserID:        "U_bot",
			APIAppID:         "A_app_1",
		},
	})
	if err != nil {
		t.Fatalf("generate second pending install state failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/slack/install/selection?requestId=install-request-a", nil)
	req.AddCookie(&http.Cookie{
		Name:  pendingInstallCookieNameForRequest("install-request-a"),
		Value: pendingStateA,
		Path:  "/api/slack/install/selection",
	})
	req.AddCookie(&http.Cookie{
		Name:  pendingInstallCookieNameForRequest("install-request-b"),
		Value: pendingStateB,
		Path:  "/api/slack/install/selection",
	})
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode selection payload: %v", err)
	}
	if payload["requestId"] != "install-request-a" || payload["teamId"] != "T_workspace_a" {
		t.Fatalf("expected first pending install, got %#v", payload)
	}
}

func TestWorkspaceManagementRequiresBrowserPrincipal(t *testing.T) {
	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: "https://backend.example.test",
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/slack/workspaces", nil)
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceManagementRendersManagedInstallations(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
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
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/slack/workspaces", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected React redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/settings/slack/workspaces" {
		t.Fatalf("expected workspace React route, got %q", location)
	}
}

func TestWorkspaceManagementAPIWritesNoStore(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "resolved",
			"installations": []map[string]any{},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/slack/workspaces", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("expected no-store cache policy, got %q", cacheControl)
	}
}

func TestChannelSettingsRendersManagedConnections(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"installations": []map[string]any{
				{
					"id": "ci_1",
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
					},
					"connections": []map[string]any{
						{
							"id":        "cc_1",
							"isDefault": true,
							"state":     "ready",
							"routes": []map[string]any{
								{
									"id":                "cr_1",
									"externalChannelId": "C_channel_1",
									"requireMention":    false,
									"enabled":           true,
								},
							},
						},
						{
							"id":    "cc_2",
							"state": "ready",
							"routes": []map[string]any{
								{
									"id":                "cr_2",
									"externalChannelId": "C_channel_2",
									"requireMention":    true,
									"enabled":           true,
								},
							},
						},
					},
					"allowedActions": []string{"changeTarget", "disconnect"},
				},
			},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	installationReq := httptest.NewRequest(http.MethodGet, "/settings/channels/installations/ci_1", nil)
	installationReq.Header.Set("X-Spritz-User-Id", "user-1")
	installationRec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(installationRec, installationReq)

	if installationRec.Code != http.StatusSeeOther {
		t.Fatalf("expected installation redirect, got %d: %s", installationRec.Code, installationRec.Body.String())
	}
	if location := installationRec.Header().Get("Location"); location != "/settings/slack/channels/installations/ci_1" {
		t.Fatalf("expected installation React route, got %q", location)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings/channels/installations/ci_1/connections/cc_2", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected connection redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/settings/slack/channels/installations/ci_1/connections/cc_2" {
		t.Fatalf("expected connection React route, got %q", location)
	}
}

func TestChannelSettingsListDoesNotInventLegacyConnectionIDs(t *testing.T) {
	requireMention := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"installations": []map[string]any{
				{
					"id": "ci_1",
					"route": map[string]any{
						"principalId":       "shared-slack-gateway",
						"provider":          "slack",
						"externalScopeType": "workspace",
						"externalTenantId":  "T_workspace_1",
					},
					"state": "ready",
					"installationConfig": installationConfig{
						ChannelPolicies: []installationChannelPolicy{
							{ExternalChannelID: "C_channel_1", RequireMention: &requireMention},
						},
					},
					"allowedActions": []string{"changeTarget", "disconnect"},
				},
			},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/settings/channels", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected React redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/settings/slack/channels" {
		t.Fatalf("expected channel settings React route, got %q", location)
	}
}

func TestChannelSettingsUpdatePostsRoutePolicies(t *testing.T) {
	var updatePayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-installations/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"installations": []map[string]any{
					{
						"id": "ci_1",
						"route": map[string]any{
							"principalId":       "shared-slack-gateway",
							"provider":          "slack",
							"externalScopeType": "workspace",
							"externalTenantId":  "T_workspace_1",
						},
						"state": "ready",
						"connections": []map[string]any{
							{
								"id":        "cc_1",
								"isDefault": true,
								"state":     "ready",
								"routes": []map[string]any{
									{
										"id":                "cr_1",
										"externalChannelId": "C_existing",
										"requireMention":    true,
										"enabled":           true,
									},
								},
							},
							{
								"id":    "cc_2",
								"state": "ready",
								"routes": []map[string]any{
									{
										"id":                "cr_2",
										"externalChannelId": "C_channel_2",
										"requireMention":    true,
										"enabled":           true,
									},
								},
							},
						},
						"allowedActions": []string{"changeTarget", "disconnect"},
					},
				},
			})
		case "/internal/v2/spritz/channel-installations/routes/update":
			if err := json.NewDecoder(r.Body).Decode(&updatePayload); err != nil {
				t.Fatalf("decode update payload: %v", err)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":            "resolved",
				"needsProvisioning": false,
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gateway.policies.remember("T_workspace_1", installationPolicySnapshot{
		config:    installationConfig{},
		botUserID: "U_bot",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/settings/channels/installations/ci_1/connections/cc_1",
		strings.NewReader("action=upsert&externalChannelId=C_new"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	if updatePayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id, got %#v", updatePayload["callerAuthId"])
	}
	if updatePayload["installationId"] != "ci_1" || updatePayload["connectionId"] != "cc_1" {
		t.Fatalf("expected installation and connection ids, got %#v", updatePayload)
	}
	policies, ok := updatePayload["channelPolicies"].([]any)
	if !ok || len(policies) != 2 {
		t.Fatalf("expected two channel policies, got %#v", updatePayload["channelPolicies"])
	}
	var newPolicy map[string]any
	for _, rawPolicy := range policies {
		policy, ok := rawPolicy.(map[string]any)
		if !ok {
			t.Fatalf("expected policy object, got %#v", rawPolicy)
		}
		if policy["externalChannelId"] == "C_new" {
			newPolicy = policy
		}
	}
	if newPolicy == nil || newPolicy["requireMention"] != false {
		t.Fatalf("expected no-mention policy for C_new, got %#v", policies)
	}
	if _, ok := gateway.policies.lookup("T_workspace_1"); ok {
		t.Fatalf("expected channel settings update to evict cached policy")
	}
}

func TestChannelSettingsAPIUpdatePostsRoutePolicies(t *testing.T) {
	var updatePayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-installations/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"installations": []map[string]any{
					{
						"id": "chinst_example",
						"route": map[string]any{
							"principalId":       "shared-slack-gateway",
							"provider":          "slack",
							"externalScopeType": "workspace",
							"externalTenantId":  "T_workspace_1",
						},
						"state": "ready",
						"connections": []map[string]any{
							{
								"id":        "chconn_example",
								"isDefault": true,
								"state":     "ready",
								"routes": []map[string]any{
									{
										"id":                "chroute_existing",
										"externalChannelId": "C_existing",
										"requireMention":    true,
										"enabled":           true,
									},
								},
							},
						},
						"allowedActions": []string{"changeTarget", "disconnect"},
					},
				},
			})
		case "/internal/v2/spritz/channel-installations/routes/update":
			if err := json.NewDecoder(r.Body).Decode(&updatePayload); err != nil {
				t.Fatalf("decode update payload: %v", err)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":            "resolved",
				"needsProvisioning": false,
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gateway.policies.remember("T_workspace_1", installationPolicySnapshot{
		config:    installationConfig{},
		botUserID: "U_bot",
	})

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/settings/channels/installations/chinst_example/connections/chconn_example",
		strings.NewReader(`{"channelPolicies":[{"externalChannelId":"C_new","externalChannelType":"channel","requireMention":false}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if updatePayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id, got %#v", updatePayload["callerAuthId"])
	}
	if updatePayload["installationId"] != "chinst_example" || updatePayload["connectionId"] != "chconn_example" {
		t.Fatalf("expected opaque installation and connection ids, got %#v", updatePayload)
	}
	policies, ok := updatePayload["channelPolicies"].([]any)
	if !ok || len(policies) != 1 {
		t.Fatalf("expected one channel policy, got %#v", updatePayload["channelPolicies"])
	}
	policy, ok := policies[0].(map[string]any)
	if !ok {
		t.Fatalf("expected policy object, got %#v", policies[0])
	}
	if policy["externalChannelId"] != "C_new" || policy["requireMention"] != false || policy["externalChannelType"] != "channel" {
		t.Fatalf("expected no-mention policy for C_new, got %#v", policy)
	}
	if _, ok := gateway.policies.lookup("T_workspace_1"); ok {
		t.Fatalf("expected channel settings update to evict cached policy")
	}
}

func TestChannelSettingsAPIMissingConnectionReturnsJSON(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"installations": []map[string]any{
				{
					"id": "chinst_example",
					"route": map[string]any{
						"principalId":       "shared-slack-gateway",
						"provider":          "slack",
						"externalScopeType": "workspace",
						"externalTenantId":  "T_workspace_1",
					},
					"state": "ready",
					"connections": []map[string]any{
						{
							"id":        "chconn_example",
							"isDefault": true,
							"state":     "ready",
						},
					},
				},
			},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/settings/channels/installations/chinst_example/connections/missing",
		nil,
	)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload["status"] != "error" || payload["message"] != "connection not found" {
		t.Fatalf("expected structured missing connection error, got %#v", payload)
	}
}

func TestWorkspaceManagementAcceptsConfiguredBrowserAuthHeaders(t *testing.T) {
	t.Setenv("SPRITZ_AUTH_HEADER_ID", "X-Forwarded-User")
	t.Setenv("SPRITZ_AUTH_HEADER_EMAIL", "X-Forwarded-Email")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
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
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/slack/workspaces", nil)
	req.Header.Set("X-Forwarded-User", "user-1")
	req.Header.Set("X-Forwarded-Email", "user@example.com")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected React redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/settings/slack/workspaces" {
		t.Fatalf("expected workspace React route, got %q", location)
	}
}

func TestWorkspaceTargetPickerUsesCurrentBrowserPrincipal(t *testing.T) {
	var listPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-install-targets/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&listPayload); err != nil {
			t.Fatalf("decode list payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"targets": []map[string]any{
				{
					"id": "ag_workspace",
					"profile": map[string]any{
						"name": "Workspace Helper",
					},
					"ownerLabel": "Personal",
					"presetInputs": map[string]any{
						"agentId": "ag_workspace",
					},
				},
			},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/slack/workspaces/target?teamId=T_workspace_1", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if listPayload["ownerAuthId"] != "user-1" {
		t.Fatalf("expected ownerAuthId to come from browser principal, got %#v", listPayload["ownerAuthId"])
	}
	if !strings.Contains(rec.Body.String(), "Workspace Helper") {
		t.Fatalf("expected target payload, got %q", rec.Body.String())
	}
}

func TestWorkspaceTargetUpdateRedirectsOnSuccess(t *testing.T) {
	var updatePayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/target/update" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&updatePayload); err != nil {
			t.Fatalf("decode update payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "resolved",
			"needsProvisioning": true,
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(
		http.MethodPost,
		"/slack/workspaces/target",
		strings.NewReader("teamId=T_workspace_1&requestId=req_manage_1&target=eyJhZ2VudElkIjoiYWdfd29ya3NwYWNlIn0"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	if updatePayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id to be forwarded, got %#v", updatePayload["callerAuthId"])
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "notice=target-updated") {
		t.Fatalf("expected success redirect notice, got %q", location)
	}
}

func TestWorkspaceDisconnectRedirectsOnSuccess(t *testing.T) {
	var disconnectPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/disconnect" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&disconnectPayload); err != nil {
			t.Fatalf("decode disconnect payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "resolved"})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(
		http.MethodPost,
		"/slack/workspaces/disconnect",
		strings.NewReader("teamId=T_workspace_1"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	if disconnectPayload["callerAuthId"] != "user-1" {
		t.Fatalf("expected caller auth id to be forwarded, got %#v", disconnectPayload["callerAuthId"])
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "notice=workspace-disconnected") {
		t.Fatalf("expected disconnect redirect notice, got %q", location)
	}
}

func TestWorkspaceTestRequiresBrowserPrincipal(t *testing.T) {
	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: "https://backend.example.test",
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/slack/workspaces/test?teamId=T_workspace_1", nil)
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceTestFormRendersForManageableWorkspace(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v2/spritz/channel-installations/list" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
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
					},
					"allowedActions": []string{"changeTarget", "disconnect"},
				},
			},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendInternalToken:  "backend-internal-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/slack/workspaces/test?teamId=T_workspace_1", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected React redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/settings/slack/workspaces/test?teamId=T_workspace_1" {
		t.Fatalf("expected workspace test React route, got %q", location)
	}
}

func TestWorkspaceTestSubmitDryRunSkipsSlackPostAndMarksPromptSynthetic(t *testing.T) {
	var promptPayload map[string]any
	slackPostHits := 0

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-installations/list":
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
						},
						"allowedActions": []string{"changeTarget", "disconnect"},
					},
				},
			})
		case "/internal/v1/spritz/channel-sessions/exchange":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-acme",
					"providerAuth": map[string]any{
						"teamId":         "T_workspace_1",
						"botUserId":      "U_bot",
						"botAccessToken": "xoxb-installed",
					},
				},
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackPostHits++
		t.Fatalf("dry-run synthetic test must not post to slack")
	}))
	defer slackAPI.Close()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			writeJSON(w, http.StatusCreated, map[string]any{
				"status": "success",
				"data": map[string]any{
					"conversation": map[string]any{
						"metadata": map[string]any{"name": "conv-1"},
					},
				},
			})
		case "/api/acp/conversations/conv-1/bootstrap":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success",
				"data": map[string]any{
					"effectiveSessionId": "session-1",
					"effectiveCwd":       "/home/dev",
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
					promptPayload = message
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

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendBaseURL:        backend.URL,
		BackendInternalToken:  "backend-internal-token",
		SlackAPIBaseURL:       slackAPI.URL,
		SpritzBaseURL:         spritz.URL,
		SpritzServiceToken:    "spritz-service-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
		DedupeTTL:             time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	form := url.Values{}
	form.Set("teamId", "T_workspace_1")
	form.Set("channelId", "C_workspace_1")
	form.Set("prompt", "synthetic smoke")
	form.Set("mode", "dry-run")
	req := httptest.NewRequest(http.MethodPost, "/slack/workspaces/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if slackPostHits != 0 {
		t.Fatalf("expected no slack posts during dry-run, got %d", slackPostHits)
	}
	if !strings.Contains(rec.Body.String(), "Outcome: dry_run") {
		t.Fatalf("expected dry_run outcome, got %q", rec.Body.String())
	}
	params, ok := promptPayload["params"].(map[string]any)
	if !ok {
		t.Fatalf("expected prompt params, got %#v", promptPayload)
	}
	chunks, ok := params["prompt"].([]any)
	if !ok || len(chunks) != 1 {
		t.Fatalf("expected one prompt chunk, got %#v", params["prompt"])
	}
	chunk, ok := chunks[0].(map[string]any)
	if !ok {
		t.Fatalf("expected prompt chunk object, got %#v", chunks[0])
	}
	promptText := fmt.Sprint(chunk["text"])
	if !strings.Contains(promptText, `"synthetic":true`) {
		t.Fatalf("expected synthetic marker in prompt context, got %q", promptText)
	}
}

func TestWorkspaceTestSubmitRealModePostsSlackReply(t *testing.T) {
	var slackPayload map[string]any

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-installations/list":
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
						},
						"allowedActions": []string{"changeTarget", "disconnect"},
					},
				},
			})
		case "/internal/v1/spritz/channel-sessions/exchange":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-acme",
					"providerAuth": map[string]any{
						"teamId":         "T_workspace_1",
						"botUserId":      "U_bot",
						"botAccessToken": "xoxb-installed",
					},
				},
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&slackPayload); err != nil {
			t.Fatalf("decode slack post payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": "1711387376.000100"})
	}))
	defer slackAPI.Close()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			writeJSON(w, http.StatusCreated, map[string]any{
				"status": "success",
				"data": map[string]any{
					"conversation": map[string]any{
						"metadata": map[string]any{"name": "conv-1"},
					},
				},
			})
		case "/api/acp/conversations/conv-1/bootstrap":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success",
				"data": map[string]any{
					"effectiveSessionId": "session-1",
					"effectiveCwd":       "/home/dev",
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

	gateway := newSlackGateway(config{
		BackendFastAPIBaseURL: backend.URL,
		BackendBaseURL:        backend.URL,
		BackendInternalToken:  "backend-internal-token",
		SlackAPIBaseURL:       slackAPI.URL,
		SpritzBaseURL:         spritz.URL,
		SpritzServiceToken:    "spritz-service-token",
		PrincipalID:           "shared-slack-gateway",
		HTTPTimeout:           5 * time.Second,
		DedupeTTL:             time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	form := url.Values{}
	form.Set("teamId", "T_workspace_1")
	form.Set("channelId", "C_workspace_1")
	form.Set("prompt", "synthetic smoke")
	form.Set("mode", "real")
	req := httptest.NewRequest(http.MethodPost, "/slack/workspaces/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if slackPayload["channel"] != "C_workspace_1" {
		t.Fatalf("expected slack post to target the requested channel, got %#v", slackPayload["channel"])
	}
	if !strings.Contains(fmt.Sprint(slackPayload["text"]), "Hello from concierge") {
		t.Fatalf("expected concierge reply to be posted, got %#v", slackPayload["text"])
	}
	if !strings.Contains(rec.Body.String(), "Outcome: delivered") {
		t.Fatalf("expected delivered outcome, got %q", rec.Body.String())
	}
}

func TestInstallTargetSelectionUsesSelectedPresetInputs(t *testing.T) {
	var upsertPayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-installations/upsert" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upsertPayload); err != nil {
			t.Fatalf("decode backend payload: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"installation": map[string]any{
				"providerInstallRef": "cred_slack_workspace_1",
			},
		})
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test/api",
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pendingState, err := gateway.state.generatePendingInstall(pendingInstallState{
		RequestID: "install-request-1",
		Installation: slackInstallation{
			TeamID:           "T_workspace_1",
			InstallingUserID: "U_installer",
			BotAccessToken:   "xoxb-installed",
			BotUserID:        "U_bot",
			APIAppID:         "A_app_1",
		},
	})
	if err != nil {
		t.Fatalf("generate pending install state failed: %v", err)
	}
	encodedTarget, err := encodeInstallTargetSelection(map[string]any{"agentId": "ag_456"})
	if err != nil {
		t.Fatalf("encode target selection failed: %v", err)
	}

	form := url.Values{}
	form.Set("state", pendingState)
	form.Set("target", encodedTarget)
	form.Set("requestId", "install-request-1")
	req := httptest.NewRequest(http.MethodPost, "/slack/install/select", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	gateway.handleInstallTargetSelection(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 after selection submit, got %d: %s", rec.Code, rec.Body.String())
	}
	redirectURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	if redirectURL.Query().Get("code") != "installed" {
		t.Fatalf("expected installed code, got %q", redirectURL.Query().Get("code"))
	}
	presetInputs, ok := upsertPayload["presetInputs"].(map[string]any)
	if !ok {
		t.Fatalf("expected presetInputs object, got %#v", upsertPayload["presetInputs"])
	}
	if presetInputs["agentId"] != "ag_456" {
		t.Fatalf("expected selected agentId ag_456, got %#v", presetInputs["agentId"])
	}
	providerAuth, ok := upsertPayload["providerAuth"].(map[string]any)
	if !ok {
		t.Fatalf("expected providerAuth object, got %#v", upsertPayload["providerAuth"])
	}
	if providerAuth["botAccessToken"] != "xoxb-installed" {
		t.Fatalf("expected stored provider auth to round-trip, got %#v", providerAuth["botAccessToken"])
	}
}

func TestInstallTargetSelectionAPIPreservesClassifiedUpsertFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-installations/upsert" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":"unresolved","field":"ownerRef","error":"external_identity_unresolved"}`))
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test/api",
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pendingState, err := gateway.state.generatePendingInstall(pendingInstallState{
		RequestID: "install-request-1",
		Installation: slackInstallation{
			TeamID:           "T_workspace_1",
			InstallingUserID: "U_installer",
			BotAccessToken:   "xoxb-installed",
			BotUserID:        "U_bot",
			APIAppID:         "A_app_1",
		},
	})
	if err != nil {
		t.Fatalf("generate pending install state failed: %v", err)
	}
	requestBody, err := json.Marshal(map[string]any{
		"requestId": "install-request-1",
		"presetInputs": map[string]any{
			"agentId": "ag_456",
		},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/slack/install/selection", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{
		Name:  pendingInstallCookieNameForRequest("install-request-1"),
		Value: pendingState,
		Path:  "/api/slack/install/selection",
	})
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected typed install result response, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode API payload: %v", err)
	}
	if payload["status"] != "error" {
		t.Fatalf("expected error status, got %#v", payload["status"])
	}
	if payload["code"] != "identity.unresolved" {
		t.Fatalf("expected identity.unresolved code, got %#v", payload["code"])
	}
	if payload["requestId"] != "install-request-1" {
		t.Fatalf("expected request id to round-trip, got %#v", payload["requestId"])
	}
	if payload["teamId"] != "T_workspace_1" {
		t.Fatalf("expected team id to round-trip, got %#v", payload["teamId"])
	}
	if strings.Contains(rec.Body.String(), "ownerRef") {
		t.Fatalf("expected API payload to hide backend field names, got %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "xoxb-installed") {
		t.Fatalf("expected API payload to hide bot token material, got %q", rec.Body.String())
	}
}

func TestInstallTargetSelectionAPIRejectsStaleRequestID(t *testing.T) {
	backendCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		t.Fatalf("backend should not be called for stale install selection")
	}))
	defer backend.Close()

	gateway := newSlackGateway(config{
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test/api",
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pendingState, err := gateway.state.generatePendingInstall(pendingInstallState{
		RequestID: "install-request-current",
		Installation: slackInstallation{
			TeamID:           "T_workspace_current",
			InstallingUserID: "U_installer",
			BotAccessToken:   "xoxb-installed",
			BotUserID:        "U_bot",
			APIAppID:         "A_app_1",
		},
	})
	if err != nil {
		t.Fatalf("generate pending install state failed: %v", err)
	}
	requestBody, err := json.Marshal(map[string]any{
		"requestId": "install-request-stale",
		"presetInputs": map[string]any{
			"agentId": "ag_456",
		},
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/slack/install/selection", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{
		Name:  pendingInstallCookieNameForRequest("install-request-current"),
		Value: pendingState,
		Path:  "/api/slack/install/selection",
	})
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for stale request id, got %d: %s", rec.Code, rec.Body.String())
	}
	if backendCalled {
		t.Fatalf("backend was called for stale install selection")
	}
}

func TestInstallRedirectUsesConfiguredSlackHost(t *testing.T) {
	cfg := config{
		PublicURL:          "https://gateway.example.test",
		SlackClientID:      "client-id",
		SlackAPIBaseURL:    "https://gov.slack.example.test/api",
		SlackBotScopes:     []string{"chat:write", "im:history"},
		OAuthStateSecret:   "oauth-state-secret",
		BackendBaseURL:     "https://backend.example.test",
		SpritzBaseURL:      "https://spritz.example.test",
		SpritzServiceToken: "spritz-service-token",
		PrincipalID:        "shared-slack-gateway",
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/slack/install", nil)
	rec := httptest.NewRecorder()
	gateway.handleInstallRedirect(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("expected redirect location")
	}
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if redirectURL.Scheme != "https" || redirectURL.Host != "gov.slack.example.test" {
		t.Fatalf("expected redirect host to follow configured Slack host, got %s", redirectURL.String())
	}
	if redirectURL.Path != "/oauth/v2/authorize" {
		t.Fatalf("expected authorize path, got %s", redirectURL.Path)
	}
	if got := redirectURL.Query().Get("client_id"); got != "client-id" {
		t.Fatalf("expected client_id query param, got %q", got)
	}
	if got := redirectURL.Query().Get("scope"); got != "chat:write,im:history" {
		t.Fatalf("expected scope query param, got %q", got)
	}
}

func TestRoutesServeSlackEndpointsUnderConfiguredPublicURLPathPrefix(t *testing.T) {
	cfg := config{
		PublicURL:          "https://gateway.example.test/spritz/slack-gateway",
		SlackClientID:      "client-id",
		SlackAPIBaseURL:    "https://slack.example.test/api",
		SlackBotScopes:     []string{"chat:write"},
		OAuthStateSecret:   "oauth-state-secret",
		BackendBaseURL:     "https://backend.example.test",
		SpritzBaseURL:      "https://spritz.example.test",
		SpritzServiceToken: "spritz-service-token",
		PrincipalID:        "shared-slack-gateway",
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/spritz/slack-gateway/slack/install", nil)
	rec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected prefixed install route to resolve, got %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if got := redirectURL.Query().Get("redirect_uri"); got != "https://gateway.example.test/spritz/slack-gateway/slack/oauth/callback" {
		t.Fatalf("expected redirect_uri to keep the public path prefix, got %q", got)
	}
}

func TestOAuthCallbackRedirectsToControlledRetryableErrorWhenBackendUpsertFails(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v2/spritz/channel-install-targets/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"targets": []map[string]any{
					{
						"id": "ag_123",
						"profile": map[string]any{
							"name": "Workspace Helper",
						},
						"presetInputs": map[string]any{
							"agentId": "ag_123",
						},
					},
				},
			})
		case "/internal/v1/spritz/channel-installations/upsert":
			http.Error(w, "backend unavailable", http.StatusServiceUnavailable)
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
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
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	var logBuffer bytes.Buffer
	gateway := newSlackGateway(
		cfg,
		slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})),
	)
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	redirectURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	if got := redirectURL.Query().Get("status"); got != "error" {
		t.Fatalf("expected error status, got %q", got)
	}
	if got := redirectURL.Query().Get("code"); got != "resolver.unavailable" {
		t.Fatalf("expected resolver.unavailable code, got %q", got)
	}

	resultReq := httptest.NewRequest(http.MethodGet, redirectURL.RequestURI(), nil)
	resultRec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(resultRec, resultReq)

	if resultRec.Code != http.StatusSeeOther {
		t.Fatalf("expected result redirect, got %d: %s", resultRec.Code, resultRec.Body.String())
	}
	if strings.Contains(resultRec.Header().Get("Location"), "backend unavailable") {
		t.Fatalf("expected retryable result redirect to hide backend body, got %q", resultRec.Header().Get("Location"))
	}
	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "slack oauth callback installation upsert failed") {
		t.Fatalf("expected upsert failure to be logged, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "backend unavailable") {
		t.Fatalf("expected backend error details in logs, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "installing_user_id=U_installer") {
		t.Fatalf("expected installing user id in logs, got %q", logOutput)
	}
}

func TestExtractACPTextSupportsResourceBlocks(t *testing.T) {
	resourceText := acptext.Extract(map[string]any{
		"resource": map[string]any{
			"text": "resource text",
		},
	})
	if resourceText != "resource text" {
		t.Fatalf("expected resource text, got %q", resourceText)
	}

	resourceURI := acptext.Extract(map[string]any{
		"resource": map[string]any{
			"uri": "file://workspace/report.txt",
		},
	})
	if resourceURI != "file://workspace/report.txt" {
		t.Fatalf("expected resource uri, got %q", resourceURI)
	}
}

func TestOAuthCallbackRedirectsToControlledOwnerResolutionError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":"unresolved","field":"ownerRef","error":"external_identity_unresolved"}`))
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
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=test-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	redirectURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	if got := redirectURL.Query().Get("code"); got != "identity.unresolved" {
		t.Fatalf("expected identity.unresolved code, got %q", got)
	}

	resultReq := httptest.NewRequest(http.MethodGet, redirectURL.RequestURI(), nil)
	resultRec := httptest.NewRecorder()
	gateway.routes().ServeHTTP(resultRec, resultReq)

	if resultRec.Code != http.StatusSeeOther {
		t.Fatalf("expected result redirect, got %d: %s", resultRec.Code, resultRec.Body.String())
	}
	location := resultRec.Header().Get("Location")
	if !strings.Contains(location, "identity.unresolved") {
		t.Fatalf("expected owner resolution code in result redirect, got %q", location)
	}
	if strings.Contains(location, "ownerRef") {
		t.Fatalf("expected result redirect to avoid leaking backend field names, got %q", location)
	}
}

func TestOAuthCallbackRedirectsDeniedProviderAuthToControlledResult(t *testing.T) {
	cfg := config{
		PublicURL:            "https://gateway.example.test",
		SlackClientID:        "client-id",
		SlackClientSecret:    "client-secret",
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test/api",
		SlackBotScopes:       []string{"chat:write"},
		BackendBaseURL:       "https://backend.example.test",
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state, err := gateway.state.generate()
	if err != nil {
		t.Fatalf("state generate failed: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/slack/oauth/callback?error=access_denied&state="+url.QueryEscape(state),
		nil,
	)
	rec := httptest.NewRecorder()
	gateway.handleOAuthCallback(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	redirectURL, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect: %v", err)
	}
	if got := redirectURL.Query().Get("code"); got != "auth.denied" {
		t.Fatalf("expected auth.denied code, got %q", got)
	}
}

func TestSlackEventRoutesToConversationAndReplies(t *testing.T) {
	var slackCalls struct {
		sync.Mutex
		payloads []map[string]any
	}
	var acpAuthHeaders struct {
		sync.Mutex
		values []string
	}
	var promptPayload struct {
		sync.Mutex
		value map[string]any
	}
	var channelConversationCall struct {
		sync.Mutex
		authHeaders []string
		payloads    []map[string]any
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
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": "1711387376.000100"})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/channel-conversations/upsert":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode channel conversation body: %v", err)
			}
			channelConversationCall.Lock()
			channelConversationCall.authHeaders = append(channelConversationCall.authHeaders, r.Header.Get("Authorization"))
			channelConversationCall.payloads = append(channelConversationCall.payloads, payload)
			channelConversationCall.Unlock()
			statusCode := http.StatusCreated
			created := true
			if strings.TrimSpace(fmt.Sprint(payload["conversationId"])) != "" {
				statusCode = http.StatusOK
				created = false
			}
			writeJSON(w, statusCode, map[string]any{
				"status": "success",
				"data": map[string]any{
					"created": created,
					"conversation": map[string]any{
						"metadata": map[string]any{"name": "conv-1"},
						"spec":     map[string]any{"cwd": "/home/dev"},
					},
				},
			})
		case r.URL.Path == "/api/acp/conversations/conv-1/bootstrap":
			acpAuthHeaders.Lock()
			acpAuthHeaders.values = append(acpAuthHeaders.values, r.Header.Get("Authorization"))
			acpAuthHeaders.Unlock()
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
			acpAuthHeaders.Lock()
			acpAuthHeaders.values = append(acpAuthHeaders.values, r.Header.Get("Authorization"))
			acpAuthHeaders.Unlock()
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
					promptPayload.Lock()
					promptPayload.value = message
					promptPayload.Unlock()
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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
			if _, ok := payload["thread_ts"]; ok {
				t.Fatalf("expected top-level channel reply, got %#v", payload["thread_ts"])
			}
			acpAuthHeaders.Lock()
			defer acpAuthHeaders.Unlock()
			if len(acpAuthHeaders.values) != 2 {
				t.Fatalf("expected bootstrap and connect auth headers, got %#v", acpAuthHeaders.values)
			}
			for _, authHeader := range acpAuthHeaders.values {
				if authHeader != "Bearer spritz-service-token" {
					t.Fatalf("expected service token for ACP calls, got %q", authHeader)
				}
			}
			channelConversationCall.Lock()
			defer channelConversationCall.Unlock()
			if len(channelConversationCall.authHeaders) != 1 {
				t.Fatalf("expected only the root upsert, got %#v", channelConversationCall.authHeaders)
			}
			for _, authHeader := range channelConversationCall.authHeaders {
				if authHeader != "Bearer owner-token" {
					t.Fatalf("expected owner token for channel conversation upsert, got %q", authHeader)
				}
			}
			if channelConversationCall.payloads[0]["principalId"] != "shared-slack-gateway" {
				t.Fatalf("expected shared gateway principal in first channel conversation payload, got %#v", channelConversationCall.payloads[0]["principalId"])
			}
			if channelConversationCall.payloads[0]["externalConversationId"] != "C_1" {
				t.Fatalf("expected channel-scoped conversation identity, got %#v", channelConversationCall.payloads[0]["externalConversationId"])
			}
			lookupIDs, ok := channelConversationCall.payloads[0]["lookupExternalConversationIds"].([]any)
			if !ok || len(lookupIDs) != 1 || fmt.Sprint(lookupIDs[0]) != "1711387375.000100" {
				t.Fatalf("expected legacy conversation lookup id to be sent, got %#v", channelConversationCall.payloads[0]["lookupExternalConversationIds"])
			}
			promptPayload.Lock()
			capturedPromptPayload := promptPayload.value
			promptPayload.Unlock()
			params, ok := capturedPromptPayload["params"].(map[string]any)
			if !ok {
				t.Fatalf("expected prompt params payload, got %#v", capturedPromptPayload)
			}
			promptItems, ok := params["prompt"].([]any)
			if !ok || len(promptItems) != 1 {
				t.Fatalf("expected a single prompt item, got %#v", params["prompt"])
			}
			item, ok := promptItems[0].(map[string]any)
			if !ok {
				t.Fatalf("expected prompt item object, got %#v", promptItems[0])
			}
			text := fmt.Sprint(item["text"])
			if !strings.Contains(text, "<spritz-channel-context>") {
				t.Fatalf("expected trusted channel context in prompt text, got %q", text)
			}
			if !strings.Contains(text, "\"actor_user_id\":\"U_1\"") {
				t.Fatalf("expected actor metadata in prompt text, got %q", text)
			}
			if !strings.HasSuffix(text, "\n\nhello") {
				t.Fatalf("expected normalized prompt body after metadata block, got %q", text)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected slack reply to be posted")
}

func TestSlackEventIgnoresTopLevelChannelMessagesWithoutMention(t *testing.T) {
	var backendCalls int
	var exchangePayload map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&exchangePayload); err != nil {
			t.Fatalf("decode session exchange body: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken":  "owner-token",
				"ownerAuthId":  "owner-123",
				"namespace":    "spritz-staging",
				"instanceId":   "zeno-acme",
				"providerAuth": map[string]any{"botUserId": "U_bot"},
				"installationConfig": map[string]any{
					"channelPolicies": []map[string]any{},
				},
			},
		})
	}))
	defer backend.Close()

	cfg := config{
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test",
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_plain_channel",
		"event":{"type":"message","user":"U_1","text":"good morning","channel":"C_1","channel_type":"channel","ts":"1711387375.000100"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	signSlackRequest(req.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	rec := httptest.NewRecorder()

	gateway.handleSlackEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gateway.waitForWorkers(drainCtx); err != nil {
		t.Fatalf("worker drain failed: %v", err)
	}
	if backendCalls != 1 {
		t.Fatalf("expected one backend policy lookup, got %d backend calls", backendCalls)
	}
	if exchangePayload["externalChannelId"] != "C_1" {
		t.Fatalf("expected session exchange to include message channel id, got %#v", exchangePayload)
	}
}

func TestSlackEventAcknowledgesBeforeBackgroundProcessingFails(t *testing.T) {
	cfg := config{
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test",
		BackendBaseURL:       "https://backend.example.test",
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
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

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gateway.waitForWorkers(drainCtx); err != nil {
		t.Fatalf("worker drain failed: %v", err)
	}
}

func TestSlackUninstallReturnsBadGatewayWhenDisconnectFails(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-installations/disconnect" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}`))
	}))
	defer backend.Close()

	cfg := config{
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      "https://slack.example.test",
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_uninstall_1",
		"event":{"type":"app_uninstalled"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	signSlackRequest(req.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	rec := httptest.NewRecorder()

	gateway.handleSlackEvents(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSlackEventAcknowledgesBeforeSlowACPWorkCompletes(t *testing.T) {
	var slackCalls struct {
		sync.Mutex
		count int
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		slackCalls.Lock()
		slackCalls.count++
		slackCalls.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	releasePrompt := make(chan struct{})
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
					<-releasePrompt
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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_async",
		"event":{"type":"app_mention","user":"U_1","text":"<@U_bot> hello","channel":"C_1","channel_type":"channel","ts":"1711387375.000100"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	signSlackRequest(req.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		gateway.handleSlackEvents(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected Slack event acknowledgement before ACP prompt completion")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 acknowledgement, got %d: %s", rec.Code, rec.Body.String())
	}

	slackCalls.Lock()
	countBeforeRelease := slackCalls.count
	slackCalls.Unlock()
	if countBeforeRelease != 0 {
		t.Fatalf("expected slack reply to wait for ACP prompt completion, got %d", countBeforeRelease)
	}

	close(releasePrompt)

	drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := gateway.waitForWorkers(drainCtx); err != nil {
		t.Fatalf("expected background worker to finish after ACP prompt completion: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		slackCalls.Lock()
		count := slackCalls.count
		slackCalls.Unlock()
		if count == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected asynchronous Slack reply to be posted")
}

func TestSlackEventContinuesAfterRequestContextCancellation(t *testing.T) {
	var slackCalls struct {
		sync.Mutex
		count int
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		slackCalls.Lock()
		slackCalls.count++
		slackCalls.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	releasePrompt := make(chan struct{})
	promptStarted := make(chan struct{}, 1)
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
					select {
					case promptStarted <- struct{}{}:
					default:
					}
					<-releasePrompt
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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_cancelled",
		"event":{"type":"app_mention","user":"U_1","text":"<@U_bot> hello","channel":"C_1","channel_type":"channel","ts":"1711387375.000100"}
	}`)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	signSlackRequest(req.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		gateway.handleSlackEvents(rec, req)
		close(done)
	}()

	select {
	case <-promptStarted:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected Slack event to reach the ACP prompt before cancellation")
	}

	cancelRequest()
	close(releasePrompt)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected detached Slack processing to finish after request cancellation")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 acknowledgement after detached processing, got %d: %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		slackCalls.Lock()
		count := slackCalls.count
		slackCalls.Unlock()
		if count == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected detached processing to post the Slack reply")
}

func TestSlackEventReturnsServiceUnavailableWhileMatchingDeliveryIsInFlight(t *testing.T) {
	var slackCalls struct {
		sync.Mutex
		count int
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		slackCalls.Lock()
		slackCalls.count++
		slackCalls.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	releasePrompt := make(chan struct{})
	promptStarted := make(chan struct{}, 1)
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
					select {
					case promptStarted <- struct{}{}:
					default:
					}
					<-releasePrompt
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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{
		"type":"event_callback",
		"team_id":"T_workspace_1",
		"api_app_id":"A_app_1",
		"event_id":"Ev_inflight",
		"event":{"type":"app_mention","user":"U_1","text":"<@U_bot> hello","channel":"C_1","channel_type":"channel","ts":"1711387375.000100"}
	}`)

	firstReq := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	firstReq.Header.Set("Content-Type", "application/json")
	signSlackRequest(firstReq.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	firstRec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		gateway.handleSlackEvents(firstRec, firstReq)
		close(done)
	}()

	select {
	case <-promptStarted:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected first delivery to reach the ACP prompt")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewReader(body))
	secondReq.Header.Set("Content-Type", "application/json")
	signSlackRequest(secondReq.Header, cfg.SlackSigningSecret, body, time.Now().UTC())
	secondRec := httptest.NewRecorder()

	gateway.handleSlackEvents(secondRec, secondReq)

	if secondRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for in-flight duplicate, got %d: %s", secondRec.Code, secondRec.Body.String())
	}

	close(releasePrompt)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("expected first delivery to finish after prompt release")
	}

	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected first delivery to succeed, got %d: %s", firstRec.Code, firstRec.Body.String())
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
		"",
		"D_workspace_bot",
		nil,
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

	firstLease, firstState := store.begin("message:T_workspace_1:C_1:1711387375.000100")
	if firstState != dedupeStateAcquired || firstLease == nil {
		t.Fatalf("expected first delivery to acquire a lease")
	}
	if secondLease, secondState := store.begin("message:T_workspace_1:C_1:1711387375.000100"); secondState != dedupeStateDuplicateInFlight || secondLease != nil {
		t.Fatalf("expected in-flight duplicate to be marked retryable")
	}

	firstLease.finish(false)

	retryLease, retryState := store.begin("message:T_workspace_1:C_1:1711387375.000100")
	if retryState != dedupeStateAcquired || retryLease == nil {
		t.Fatalf("expected retry after failure to reacquire the lease")
	}
	retryLease.finish(true)

	if duplicateLease, duplicateState := store.begin("message:T_workspace_1:C_1:1711387375.000100"); duplicateState != dedupeStateDuplicateDelivered || duplicateLease != nil {
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

func TestBuildSlackPromptTextPrependsTrustedContext(t *testing.T) {
	prompt := buildSlackPromptText(
		"T_workspace_1",
		slackEventInner{
			Type:        "app_mention",
			User:        "U_requester",
			Text:        "<@U_BOT> create a zeno for me",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
		"U_BOT",
	)

	const prefix = "<spritz-channel-context>"
	if !strings.HasPrefix(prompt, prefix) {
		t.Fatalf("expected trusted context prefix, got %q", prompt)
	}
	endIndex := strings.Index(prompt, "</spritz-channel-context>")
	if endIndex < 0 {
		t.Fatalf("expected trusted context suffix, got %q", prompt)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(prompt[len(prefix):endIndex]), &payload); err != nil {
		t.Fatalf("decode prompt context: %v", err)
	}
	if payload["source"] != "spritz-slack-gateway" {
		t.Fatalf("expected source metadata, got %#v", payload["source"])
	}
	if payload["provider"] != "slack" {
		t.Fatalf("expected slack provider, got %#v", payload["provider"])
	}
	if payload["workspace_id"] != "T_workspace_1" {
		t.Fatalf("expected workspace metadata, got %#v", payload["workspace_id"])
	}
	if payload["actor_user_id"] != "U_requester" {
		t.Fatalf("expected actor metadata, got %#v", payload["actor_user_id"])
	}
	if payload["conversation_id"] != "C_channel_1" {
		t.Fatalf("expected top-level channel conversation identity, got %#v", payload["conversation_id"])
	}
	if payload["direct_message"] != false {
		t.Fatalf("expected non-DM metadata, got %#v", payload["direct_message"])
	}
	if !strings.HasSuffix(prompt, "\n\ncreate a zeno for me") {
		t.Fatalf("expected normalized user text after metadata block, got %q", prompt)
	}
}

func TestShouldIgnoreSlackMessageEventRejectsSystemSubtypes(t *testing.T) {
	if !shouldIgnoreSlackMessageEvent(
		slackEventInner{Type: "message", Subtype: "channel_join"},
	) {
		t.Fatalf("expected channel_join subtype to be ignored")
	}
	if shouldIgnoreSlackMessageEvent(
		slackEventInner{Type: "message", Subtype: "file_share"},
	) {
		t.Fatalf("expected file_share messages to be processed")
	}
	if shouldIgnoreSlackMessageEvent(slackEventInner{Type: "message"}) {
		t.Fatalf("expected plain message events to be processed")
	}
}

func TestShouldProcessSlackMessageEventQueuesChannelMessagesForPolicyCheck(t *testing.T) {
	if !shouldProcessSlackMessageEvent(
		slackEventInner{
			Type:        "message",
			Channel:     "C_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	) {
		t.Fatalf("expected top-level channel messages to be queued for policy check")
	}
	if !shouldProcessSlackMessageEvent(
		slackEventInner{
			Type:        "app_mention",
			Channel:     "C_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	) {
		t.Fatalf("expected channel app_mention events to be processed")
	}
	if !shouldProcessSlackMessageEvent(
		slackEventInner{
			Type:        "message",
			Channel:     "C_1",
			ChannelType: "channel",
			ThreadTS:    "1711387375.000100",
			TS:          "1711387376.000100",
		},
	) {
		t.Fatalf("expected channel thread replies to be queued for policy check")
	}
	if !shouldProcessSlackMessageEvent(
		slackEventInner{
			Type:        "message",
			Channel:     "D_1",
			ChannelType: "im",
			TS:          "1711387375.000100",
		},
	) {
		t.Fatalf("expected DM messages to be processed")
	}
}

func TestShouldRelaySlackMessageEventUsesInstallationPolicy(t *testing.T) {
	requireMention := true
	withoutMention := false
	config := installationConfig{
		ChannelPolicies: []installationChannelPolicy{
			{ExternalChannelID: "C_requires", RequireMention: &requireMention},
			{ExternalChannelID: "C_open", RequireMention: &withoutMention},
		},
	}
	snapshot := installationPolicySnapshot{config: config, botUserID: "U_bot"}

	if shouldRelaySlackMessageEvent(
		slackEventInner{Type: "message", Channel: "C_requires", Text: "hello"},
		snapshot,
	) {
		t.Fatalf("expected configured requireMention channel to require the bot mention")
	}
	if !shouldRelaySlackMessageEvent(
		slackEventInner{Type: "message", Channel: "C_open", Text: "hello"},
		snapshot,
	) {
		t.Fatalf("expected requireMention=false channel to relay without mention")
	}
	if !shouldRelaySlackMessageEvent(
		slackEventInner{Type: "message", Channel: "C_requires", Text: "<@U_bot> hello"},
		snapshot,
	) {
		t.Fatalf("expected explicit bot mention to relay even when requireMention is true")
	}
}

func TestSlackDirectMessageHelpersReuseSharedDetection(t *testing.T) {
	fallbackDM := slackEventInner{
		Type:    "message",
		Channel: "D_workspace_bot",
		TS:      "1711387375.000100",
	}
	if !isSlackDirectMessageEvent(fallbackDM) {
		t.Fatalf("expected D-prefixed channels to be treated as DMs")
	}
	if slackExternalConversationID(fallbackDM) != "D_workspace_bot" {
		t.Fatalf("expected D-prefixed channels to key conversations by channel id")
	}
	if slackReplyThreadTS(fallbackDM) != "" {
		t.Fatalf("expected D-prefixed channels to reply inline")
	}
	if lookupIDs := slackLegacyConversationLookupIDs(fallbackDM); len(lookupIDs) != 0 {
		t.Fatalf("expected D-prefixed channels to omit legacy lookup ids, got %#v", lookupIDs)
	}

	groupDM := slackEventInner{
		Type:        "message",
		Channel:     "G_workspace_group",
		ChannelType: "mpim",
		TS:          "1711387375.000100",
	}
	if !isSlackDirectMessageEvent(groupDM) {
		t.Fatalf("expected mpim channels to be treated as direct-message style conversations")
	}
	if slackExternalConversationID(groupDM) != "G_workspace_group" {
		t.Fatalf("expected mpim conversations to key by channel id")
	}
	if slackReplyThreadTS(groupDM) != "" {
		t.Fatalf("expected mpim replies to stay inline")
	}
	if lookupIDs := slackLegacyConversationLookupIDs(groupDM); len(lookupIDs) != 0 {
		t.Fatalf("expected mpim conversations to omit legacy lookup ids, got %#v", lookupIDs)
	}

	topLevelChannel := slackEventInner{
		Type:        "app_mention",
		Channel:     "C_workspace_channel",
		ChannelType: "channel",
		TS:          "1711387375.000100",
	}
	if slackExternalConversationID(topLevelChannel) != "C_workspace_channel" {
		t.Fatalf("expected top-level channel messages to key by channel id")
	}
	if slackReplyThreadTS(topLevelChannel) != "" {
		t.Fatalf("expected top-level channel mentions to reply inline")
	}
	if lookupIDs := slackLegacyConversationLookupIDs(topLevelChannel); len(lookupIDs) != 1 || lookupIDs[0] != "1711387375.000100" {
		t.Fatalf("expected top-level channel mentions to expose the legacy root-message id, got %#v", lookupIDs)
	}

	threadedChannel := slackEventInner{
		Type:        "app_mention",
		Channel:     "C_workspace_channel",
		ChannelType: "channel",
		ThreadTS:    "1711387375.000100",
		TS:          "1711387376.000100",
	}
	if slackExternalConversationID(threadedChannel) != "C_workspace_channel" {
		t.Fatalf("expected threaded channel messages to key by channel id")
	}
	if slackReplyThreadTS(threadedChannel) != "1711387375.000100" {
		t.Fatalf("expected threaded channel mentions to reply in-thread")
	}
	if lookupIDs := slackLegacyConversationLookupIDs(threadedChannel); len(lookupIDs) != 1 || lookupIDs[0] != "1711387375.000100" {
		t.Fatalf("expected threaded channel mentions to expose the legacy thread id, got %#v", lookupIDs)
	}
}

func TestPromptConversationRejectsInteractivePermissionRequests(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	permissionResponse := make(chan map[string]any, 1)
	requestHeaders := make(chan http.Header, 1)
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/acp/conversations/conv-1/connect" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		defer conn.Close()
		requestHeaders <- r.Header.Clone()
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
					"id":      77,
					"method":  "session/request_permission",
					"params": map[string]any{
						"tool":    "bash",
						"command": "ls",
					},
				})
				_, responsePayload, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("read permission response: %v", err)
				}
				var response map[string]any
				if err := json.Unmarshal(responsePayload, &response); err != nil {
					t.Fatalf("decode permission response: %v", err)
				}
				permissionResponse <- response
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      message["id"],
					"error": map[string]any{
						"code":    -32000,
						"message": "Permission denied.",
					},
				})
				return
			default:
				t.Fatalf("unexpected ACP method %#v", message["method"])
			}
		}
	}))
	defer spritz.Close()

	cfg := config{
		SpritzBaseURL: spritz.URL,
		HTTPTimeout:   5 * time.Second,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := gateway.promptConversation(
		t.Context(),
		"owner-token",
		"spritz-staging",
		"conv-1",
		"session-1",
		"/home/dev",
		"hello",
	)
	if err == nil {
		t.Fatalf("expected promptConversation to fail when permission is denied")
	}
	if !result.promptSent {
		t.Fatalf("expected prompt delivery to be marked as sent")
	}
	if strings.TrimSpace(result.reply) != "" {
		t.Fatalf("expected no reply text on permission denial, got %q", result.reply)
	}
	select {
	case response := <-permissionResponse:
		if response["id"] != float64(77) {
			t.Fatalf("expected permission response to target request 77, got %#v", response["id"])
		}
		rpcError, ok := response["error"].(map[string]any)
		if !ok {
			t.Fatalf("expected error response, got %#v", response)
		}
		if rpcError["code"] != float64(-32000) {
			t.Fatalf("expected permission denial code -32000, got %#v", rpcError["code"])
		}
	default:
		t.Fatalf("expected gateway to answer the permission request")
	}
	select {
	case headers := <-requestHeaders:
		if got := headers.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("expected spritz websocket Authorization header, got %q", got)
		}
		if got := headers.Get("Origin"); got != "" {
			t.Fatalf("expected spritz websocket Origin header to be omitted, got %q", got)
		}
	default:
		t.Fatalf("expected websocket request headers to be captured")
	}
}

func TestPromptConversationPreservesChunkBoundaryWhitespaceAndNewlines(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/acp/conversations/conv-1/connect" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
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
				for _, chunk := range []string{
					"I'll ",
					"spawn a dedicated agent for you using the",
					"\nSpritz controls.\n\nThe",
					" Slack account could not be resolved.\n",
				} {
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": chunk,
								}},
							},
						},
					})
				}
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": map[string]any{}})
				return
			default:
				t.Fatalf("unexpected ACP method %#v", message["method"])
			}
		}
	}))
	defer spritz.Close()

	cfg := config{
		SpritzBaseURL: spritz.URL,
		HTTPTimeout:   5 * time.Second,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := gateway.promptConversation(
		t.Context(),
		"owner-token",
		"spritz-staging",
		"conv-1",
		"session-1",
		"/home/dev",
		"hello",
	)
	if err != nil {
		t.Fatalf("promptConversation returned error: %v", err)
	}
	if !result.promptSent {
		t.Fatalf("expected prompt delivery to be marked as sent")
	}
	want := "I'll spawn a dedicated agent for you using the\nSpritz controls.\n\nThe Slack account could not be resolved.\n"
	if result.typeName != promptDeliveryMessage {
		t.Fatalf("expected message delivery type, got %q", result.typeName)
	}
	if result.reply != want {
		t.Fatalf("expected reply %q, got %q", want, result.reply)
	}
}

func TestPromptConversationAllowsEmptyVisibleReply(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/acp/conversations/conv-1/connect" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
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
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": map[string]any{}})
				return
			default:
				t.Fatalf("unexpected ACP method %#v", message["method"])
			}
		}
	}))
	defer spritz.Close()

	cfg := config{
		SpritzBaseURL: spritz.URL,
		HTTPTimeout:   5 * time.Second,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := gateway.promptConversation(
		t.Context(),
		"owner-token",
		"spritz-staging",
		"conv-1",
		"session-1",
		"/home/dev",
		"hello",
	)
	if err != nil {
		t.Fatalf("expected empty visible reply to succeed, got %v", err)
	}
	if !result.promptSent {
		t.Fatalf("expected prompt delivery to be marked as sent")
	}
	if result.typeName != promptDeliveryNoReply {
		t.Fatalf("expected no-reply delivery type, got %q", result.typeName)
	}
	if result.reply != "" {
		t.Fatalf("expected empty reply text, got %q", result.reply)
	}
}

func TestPostSlackMessagePreservesTextWhitespace(t *testing.T) {
	var payload map[string]any
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": "1711387376.000100"})
	}))
	defer slackAPI.Close()

	gateway := newSlackGateway(
		config{
			SlackAPIBaseURL: slackAPI.URL,
			HTTPTimeout:     5 * time.Second,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	text := "\nFirst line\n\n- bullet\n"
	if _, err := gateway.postSlackMessage(t.Context(), "xoxb-installed", "C_1", text, ""); err != nil {
		t.Fatalf("postSlackMessage returned error: %v", err)
	}
	if payload["text"] != text {
		t.Fatalf("expected text %q, got %#v", text, payload["text"])
	}
}

func TestProcessMessageEventPostsFallbackAfterPromptTimeout(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "partial reply",
								}},
							},
						},
					})
					time.Sleep(40 * time.Millisecond)
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected fallback reply to be posted after timeout, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 1 {
		t.Fatalf("expected exactly one slack reply, got %d", len(slackPayloads.items))
	}
	if got := slackPayloads.items[0]["text"]; got != "I hit an internal error while processing that request." {
		t.Fatalf("expected fallback reply text, got %#v", got)
	}
	if _, ok := slackPayloads.items[0]["thread_ts"]; ok {
		t.Fatalf("expected top-level fallback reply, got %#v", slackPayloads.items[0]["thread_ts"])
	}
}

func TestProcessMessageEventDoesNotPersistReplyAliasAfterPromptTimeout(t *testing.T) {
	var channelConversationCalls struct {
		sync.Mutex
		payloads []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": "1711387376.000100"})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode channel conversation payload: %v", err)
			}
			channelConversationCalls.Lock()
			channelConversationCalls.payloads = append(channelConversationCalls.payloads, payload)
			channelConversationCalls.Unlock()
			statusCode := http.StatusCreated
			created := true
			if strings.TrimSpace(fmt.Sprint(payload["conversationId"])) != "" {
				statusCode = http.StatusOK
				created = false
			}
			writeJSON(w, statusCode, map[string]any{
				"status": "success",
				"data": map[string]any{
					"created": created,
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "partial reply",
								}},
							},
						},
					})
					time.Sleep(40 * time.Millisecond)
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected fallback reply flow to succeed, got %v", err)
	}

	channelConversationCalls.Lock()
	defer channelConversationCalls.Unlock()
	if len(channelConversationCalls.payloads) != 1 {
		t.Fatalf("expected only the root upsert, got %#v", channelConversationCalls.payloads)
	}
}

func TestProcessMessageEventPostsStatusMessageWhileSessionRecoveryIsInFlight(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		call := sessionExchangeCalls.Add(1)
		if call < 3 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    200 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected recovery flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up message and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Still waking up. I will continue here shortly." {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello from concierge" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if _, ok := slackPayloads.items[0]["thread_ts"]; ok {
		t.Fatalf("expected top-level wake-up message, got %#v", slackPayloads.items[0]["thread_ts"])
	}
	if _, ok := slackPayloads.items[1]["thread_ts"]; ok {
		t.Fatalf("expected top-level final reply, got %#v", slackPayloads.items[1]["thread_ts"])
	}
	if sessionExchangeCalls.Load() != 3 {
		t.Fatalf("expected 3 session exchange attempts, got %d", sessionExchangeCalls.Load())
	}
}

func TestProcessMessageEventRecoversAfterRuntimeDisappearsMidFlight(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		call := sessionExchangeCalls.Add(1)
		switch call {
		case 1, 2:
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token-old",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-old",
					"providerAuth": map[string]any{
						"providerInstallRef": "cred_slack_workspace_1",
						"apiAppId":           "A_app_1",
						"teamId":             "T_workspace_1",
						"botUserId":          "U_bot",
						"botAccessToken":     "xoxb-installed",
					},
				},
			})
		case 3:
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
		default:
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token-new",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-new",
					"providerAuth": map[string]any{
						"providerInstallRef": "cred_slack_workspace_1",
						"apiAppId":           "A_app_1",
						"teamId":             "T_workspace_1",
						"botUserId":          "U_bot",
						"botAccessToken":     "xoxb-installed",
					},
				},
			})
		}
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call <= 2 {
				http.Error(w, `{"status":"error","message":"spritz not found"}`, http.StatusNotFound)
				return
			}
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello from recovered concierge",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    200 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected missing-runtime recovery flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up message and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Still waking up. I will continue here shortly." {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello from recovered concierge" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 4 {
		t.Fatalf("expected 4 session exchange attempts, got %d", sessionExchangeCalls.Load())
	}
	if upsertCalls.Load() != 3 {
		t.Fatalf("expected only the root upserts across retries, got %d", upsertCalls.Load())
	}
}

func TestProcessMessageEventRecoversAfterRuntimeReusesSameInstanceID(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	var sessionExchangeForceRefresh []bool
	var sessionExchangeMu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		sessionExchangeCalls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode session exchange body: %v", err)
		}
		sessionExchangeMu.Lock()
		sessionExchangeForceRefresh = append(
			sessionExchangeForceRefresh,
			payload["forceRefresh"] == true,
		)
		sessionExchangeMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token-stable",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-stable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call == 1 {
				http.Error(w, `{"status":"error","message":"spritz not found"}`, http.StatusNotFound)
				return
			}
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello from stable concierge",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   time.Hour,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    200 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected stable-instance recovery flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 1 {
		t.Fatalf("expected only the final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Hello from stable concierge" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 2 {
		t.Fatalf("expected initial exchange plus one recovery exchange, got %d", sessionExchangeCalls.Load())
	}
	if len(sessionExchangeForceRefresh) != 2 {
		t.Fatalf("expected two session exchange payloads, got %#v", sessionExchangeForceRefresh)
	}
	if sessionExchangeForceRefresh[0] {
		t.Fatalf("expected initial session exchange to stay on fast path, got %#v", sessionExchangeForceRefresh)
	}
	if !sessionExchangeForceRefresh[1] {
		t.Fatalf("expected recovery exchange to force refresh, got %#v", sessionExchangeForceRefresh)
	}
	if upsertCalls.Load() != 2 {
		t.Fatalf("expected only the root upserts across recovery, got %d", upsertCalls.Load())
	}
}

func TestProcessMessageEventAllowsSlowForceRefreshExchangeWithinRecoveryBudget(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	var sessionExchangeForceRefresh []bool
	var sessionExchangeMu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		sessionExchangeCalls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode session exchange body: %v", err)
		}
		forceRefresh := payload["forceRefresh"] == true
		sessionExchangeMu.Lock()
		sessionExchangeForceRefresh = append(sessionExchangeForceRefresh, forceRefresh)
		sessionExchangeMu.Unlock()
		if forceRefresh {
			time.Sleep(300 * time.Millisecond)
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token-recovered",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-recovered",
					"providerAuth": map[string]any{
						"providerInstallRef": "cred_slack_workspace_1",
						"apiAppId":           "A_app_1",
						"teamId":             "T_workspace_1",
						"botUserId":          "U_bot",
						"botAccessToken":     "xoxb-installed",
					},
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token-stale",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-stale",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call == 1 {
				http.Error(w, `{"status":"error","message":"spritz not found"}`, http.StatusNotFound)
				return
			}
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after slow recovery exchange",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    time.Second,
		RecoveryTimeout:      800 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected slow recovery exchange to succeed within recovery budget, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up message and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello after slow recovery exchange" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 2 {
		t.Fatalf("expected initial exchange plus one force-refresh exchange, got %d", sessionExchangeCalls.Load())
	}
	if len(sessionExchangeForceRefresh) != 2 {
		t.Fatalf("expected two session exchange payloads, got %#v", sessionExchangeForceRefresh)
	}
	if sessionExchangeForceRefresh[0] {
		t.Fatalf("expected initial session exchange to stay on fast path, got %#v", sessionExchangeForceRefresh)
	}
	if !sessionExchangeForceRefresh[1] {
		t.Fatalf("expected recovery exchange to force refresh, got %#v", sessionExchangeForceRefresh)
	}
}

func TestProcessMessageEventForceRefreshesAfterSessionUnavailable(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	var sessionExchangeForceRefresh []bool
	var sessionExchangeMu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		sessionExchangeCalls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode session exchange body: %v", err)
		}
		forceRefresh := payload["forceRefresh"] == true
		sessionExchangeMu.Lock()
		sessionExchangeForceRefresh = append(sessionExchangeForceRefresh, forceRefresh)
		sessionExchangeMu.Unlock()
		if !forceRefresh {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token-recovered",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-recovered",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after unavailable session refresh",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   200 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    time.Second,
		RecoveryTimeout:      time.Second,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected unavailable-session recovery to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 1 {
		t.Fatalf("expected only the final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Hello after unavailable session refresh" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 2 {
		t.Fatalf("expected initial exchange plus one force-refresh exchange, got %d", sessionExchangeCalls.Load())
	}
	if len(sessionExchangeForceRefresh) != 2 {
		t.Fatalf("expected two session exchange payloads, got %#v", sessionExchangeForceRefresh)
	}
	if sessionExchangeForceRefresh[0] {
		t.Fatalf("expected initial session exchange to stay on fast path, got %#v", sessionExchangeForceRefresh)
	}
	if !sessionExchangeForceRefresh[1] {
		t.Fatalf("expected recovery exchange to force refresh, got %#v", sessionExchangeForceRefresh)
	}
}

func TestProcessMessageEventUsesOverallDeadlineForLateSessionRecovery(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	startedAt := time.Now()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		if time.Since(startedAt) < 120*time.Millisecond {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token-recovered",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-recovered",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after late recovery",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    300 * time.Millisecond,
		RecoveryTimeout:      50 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected late recovery inside the worker deadline to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up message plus final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Still waking up. I will continue here shortly." {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello after late recovery" {
		t.Fatalf("expected final recovered reply, got %#v", got)
	}
}

func TestProcessMessageEventPostsSingleWakeUpAcrossSessionAndRuntimeRecovery(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		call := sessionExchangeCalls.Add(1)
		switch call {
		case 1, 2, 4:
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
		case 3:
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token-old",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-old",
					"providerAuth": map[string]any{
						"providerInstallRef": "cred_slack_workspace_1",
						"apiAppId":           "A_app_1",
						"teamId":             "T_workspace_1",
						"botUserId":          "U_bot",
						"botAccessToken":     "xoxb-installed",
					},
				},
			})
		default:
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token-new",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-new",
					"providerAuth": map[string]any{
						"providerInstallRef": "cred_slack_workspace_1",
						"apiAppId":           "A_app_1",
						"teamId":             "T_workspace_1",
						"botUserId":          "U_bot",
						"botAccessToken":     "xoxb-installed",
					},
				},
			})
		}
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call == 1 {
				http.Error(w, `{"status":"error","message":"spritz not found"}`, http.StatusNotFound)
				return
			}
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after both recoveries",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		PromptRetryInitial:   time.Millisecond,
		PromptRetryMax:       5 * time.Millisecond,
		PromptRetryTimeout:   20 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected combined recovery flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected one wake-up message and one final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Still waking up. I will continue here shortly." {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello after both recoveries" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 5 {
		t.Fatalf("expected 5 session exchange attempts, got %d", sessionExchangeCalls.Load())
	}
	if upsertCalls.Load() != 2 {
		t.Fatalf("expected only the root upserts across recovery, got %d", upsertCalls.Load())
	}
}

func TestProcessMessageEventRetriesWakeUpAfterSlackPostFailure(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	var wakeUpAttempts atomic.Int32
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		if payload["text"] == slackRecoveryStatusText && wakeUpAttempts.Add(1) == 1 {
			http.Error(w, "slack unavailable", http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		call := sessionExchangeCalls.Add(1)
		if call <= 3 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		PromptRetryInitial:   time.Millisecond,
		PromptRetryMax:       5 * time.Millisecond,
		PromptRetryTimeout:   20 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected wake-up retry flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if wakeUpAttempts.Load() != 2 {
		t.Fatalf("expected two wake-up attempts, got %d", wakeUpAttempts.Load())
	}
	if len(slackPayloads.items) != 3 {
		t.Fatalf("expected failed wake-up, retried wake-up, and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected first payload to be the wake-up status, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected second payload to retry the wake-up status, got %#v", got)
	}
	if got := slackPayloads.items[2]["text"]; got != "Hello from concierge" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 4 {
		t.Fatalf("expected 4 session exchange attempts, got %d", sessionExchangeCalls.Load())
	}
}

func TestProcessMessageEventDoesNotPostWakeUpDuringSlowPromptOnHealthyRuntime(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

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
					time.Sleep(20 * time.Millisecond)
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello from slow concierge",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		PromptRetryInitial:   time.Millisecond,
		PromptRetryMax:       5 * time.Millisecond,
		PromptRetryTimeout:   20 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected slow prompt flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 1 {
		t.Fatalf("expected only the final reply on a healthy runtime, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Hello from slow concierge" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
}

func TestProcessMessageEventRetriesACPUnavailableBeforeRefreshingBinding(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	var sessionExchangeForceRefresh []bool
	var sessionExchangeMu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		sessionExchangeCalls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode backend payload: %v", err)
		}
		sessionExchangeMu.Lock()
		sessionExchangeForceRefresh = append(sessionExchangeForceRefresh, payload["forceRefresh"] == true)
		sessionExchangeMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call == 1 {
				writeJSON(w, http.StatusConflict, map[string]any{
					"status": "fail",
					"data": map[string]any{
						"message": "acp unavailable",
					},
				})
				return
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode channel conversation payload: %v", err)
			}
			statusCode := http.StatusCreated
			created := true
			if strings.TrimSpace(fmt.Sprint(payload["conversationId"])) != "" {
				statusCode = http.StatusOK
				created = false
			}
			writeJSON(w, statusCode, map[string]any{
				"status": "success",
				"data": map[string]any{
					"created": created,
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
					time.Sleep(20 * time.Millisecond)
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after ACP recovery",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		PromptRetryInitial:   time.Millisecond,
		PromptRetryMax:       5 * time.Millisecond,
		PromptRetryTimeout:   20 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected ACP-unavailable retry flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up status and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello after ACP recovery" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 1 {
		t.Fatalf("expected ACP retry to stay on the same runtime before refreshing, got %d session exchange calls", sessionExchangeCalls.Load())
	}
	sessionExchangeMu.Lock()
	defer sessionExchangeMu.Unlock()
	if len(sessionExchangeForceRefresh) != 1 || sessionExchangeForceRefresh[0] {
		t.Fatalf("expected no force-refresh session exchange during ACP retry, got %#v", sessionExchangeForceRefresh)
	}
	if upsertCalls.Load() != 2 {
		t.Fatalf("expected only the failed and successful root upserts, got %d", upsertCalls.Load())
	}
}

func TestProcessMessageEventKeepsSameRuntimePendingAcrossShortACPWarmup(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	var sessionExchangeForceRefresh []bool
	var sessionExchangeMu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		sessionExchangeCalls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode backend payload: %v", err)
		}
		sessionExchangeMu.Lock()
		sessionExchangeForceRefresh = append(sessionExchangeForceRefresh, payload["forceRefresh"] == true)
		sessionExchangeMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call <= 3 {
				writeJSON(w, http.StatusConflict, map[string]any{
					"status": "fail",
					"data": map[string]any{
						"message": "acp unavailable",
					},
				})
				return
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode channel conversation payload: %v", err)
			}
			statusCode := http.StatusCreated
			created := true
			if strings.TrimSpace(fmt.Sprint(payload["conversationId"])) != "" {
				statusCode = http.StatusOK
				created = false
			}
			writeJSON(w, statusCode, map[string]any{
				"status": "success",
				"data": map[string]any{
					"created": created,
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after ACP warmup",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   time.Hour,
		SessionRetryInterval: time.Millisecond,
		PromptRetryInitial:   time.Millisecond,
		PromptRetryMax:       5 * time.Millisecond,
		PromptRetryTimeout:   20 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected ACP warmup flow to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 1 {
		t.Fatalf("expected only the final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Hello after ACP warmup" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 1 {
		t.Fatalf("expected short ACP warmup to stay on the same runtime, got %d session exchange calls", sessionExchangeCalls.Load())
	}
	sessionExchangeMu.Lock()
	defer sessionExchangeMu.Unlock()
	if len(sessionExchangeForceRefresh) != 1 || sessionExchangeForceRefresh[0] {
		t.Fatalf("expected no force-refresh session exchange during short ACP warmup, got %#v", sessionExchangeForceRefresh)
	}
	if upsertCalls.Load() != 4 {
		t.Fatalf("expected only the root upserts across prompt attempts, got %d", upsertCalls.Load())
	}
}

func TestProcessMessageEventPostsFailureWhenLateACPRecoveryTimesOut(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			upsertCalls.Add(1)
			time.Sleep(40 * time.Millisecond)
			writeJSON(w, http.StatusConflict, map[string]any{
				"status": "fail",
				"data": map[string]any{
					"message": "acp unavailable",
				},
			})
		default:
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
	}))
	defer spritz.Close()

	cfg := config{
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   20 * time.Millisecond,
		SessionRetryInterval: 15 * time.Millisecond,
		PromptRetryInitial:   5 * time.Millisecond,
		PromptRetryMax:       10 * time.Millisecond,
		PromptRetryTimeout:   15 * time.Millisecond,
		ProcessingTimeout:    50 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected late recovery timeout to be handled with terminal Slack reply, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 1 {
		t.Fatalf("expected only the terminal recovery failure reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryFailureText {
		t.Fatalf("expected terminal recovery failure text, got %#v", got)
	}
	if upsertCalls.Load() < 1 {
		t.Fatalf("expected at least one prompt attempt before terminal recovery failure")
	}
}

func TestProcessMessageEventPostsWakeUpDuringACPRetryBackoff(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	var recoveryStartedAt atomic.Pointer[time.Time]
	var wakeUpPostedAt atomic.Pointer[time.Time]
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		if payload["text"] == slackRecoveryStatusText {
			now := time.Now()
			wakeUpPostedAt.Store(&now)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		sessionExchangeCalls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-conversations/upsert":
			call := upsertCalls.Add(1)
			if call == 1 {
				now := time.Now()
				recoveryStartedAt.Store(&now)
				writeJSON(w, http.StatusConflict, map[string]any{
					"status": "fail",
					"data": map[string]any{
						"message": "acp unavailable",
					},
				})
				return
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode channel conversation payload: %v", err)
			}
			statusCode := http.StatusCreated
			created := true
			if strings.TrimSpace(fmt.Sprint(payload["conversationId"])) != "" {
				statusCode = http.StatusOK
				created = false
			}
			writeJSON(w, statusCode, map[string]any{
				"status": "success",
				"data": map[string]any{
					"created": created,
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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after ACP backoff",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 60 * time.Millisecond,
		PromptRetryInitial:   20 * time.Millisecond,
		PromptRetryMax:       20 * time.Millisecond,
		PromptRetryTimeout:   40 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected ACP-unavailable retry flow to succeed, got %v", err)
	}

	recoveryStart := recoveryStartedAt.Load()
	if recoveryStart == nil {
		t.Fatal("expected to record recovery start time")
	}
	wakeUpTime := wakeUpPostedAt.Load()
	if wakeUpTime == nil {
		t.Fatal("expected wake-up status to be posted")
	}
	if delta := wakeUpTime.Sub(*recoveryStart); delta >= 30*time.Millisecond {
		t.Fatalf("expected wake-up status during retry backoff, got delay %s", delta)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up status and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello after ACP backoff" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 1 {
		t.Fatalf("expected ACP retry to stay on the same runtime before refreshing, got %d session exchange calls", sessionExchangeCalls.Load())
	}
}

func TestProcessMessageEventPostsFailureWhenRetriedACPRecoveryCallTimesOut(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var upsertCalls atomic.Int32
	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channel-conversations/upsert" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
		if upsertCalls.Add(1) == 1 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"status": "fail",
				"data": map[string]any{
					"message": "acp unavailable",
				},
			})
			return
		}
		time.Sleep(80 * time.Millisecond)
	}))
	defer spritz.Close()

	cfg := config{
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 5 * time.Millisecond,
		PromptRetryInitial:   5 * time.Millisecond,
		PromptRetryMax:       5 * time.Millisecond,
		PromptRetryTimeout:   10 * time.Millisecond,
		ProcessingTimeout:    50 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected timed-out retry after visible recovery to produce terminal Slack failure, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up status and terminal failure, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != slackRecoveryFailureText {
		t.Fatalf("expected terminal recovery failure text, got %#v", got)
	}
}

func TestProcessMessageEventDoesNotSwallowUnrelatedSetupErrorAfterRecoveryStarts(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		call := sessionExchangeCalls.Add(1)
		if call == 1 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "resolved",
			"session": map[string]any{
				"accessToken": "owner-token",
				"ownerAuthId": "owner-123",
				"namespace":   "spritz-staging",
				"instanceId":  "zeno-acme",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	spritz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channel-conversations/upsert" {
			t.Fatalf("unexpected spritz path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"status": "fail",
			"data": map[string]any{
				"message": "channel conversation is ambiguous",
			},
		})
	}))
	defer spritz.Close()

	cfg := config{
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   50 * time.Millisecond,
		SessionRetryInterval: 5 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err = gateway.processMessageEventWithDelivery(ctx, envelope, delivery)
	if err == nil || !strings.Contains(err.Error(), "channel conversation is ambiguous") {
		t.Fatalf("expected unrelated setup error to bubble out, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 0 {
		t.Fatalf("expected no Slack recovery message for unrelated setup error, got %#v", slackPayloads.items)
	}
}

func TestProcessMessageEventKeepsRecoveringAfterTransientExchangeError(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		call := sessionExchangeCalls.Add(1)
		switch call {
		case 1:
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			})
		case 2:
			http.Error(w, "backend unavailable", http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "resolved",
				"session": map[string]any{
					"accessToken": "owner-token",
					"ownerAuthId": "owner-123",
					"namespace":   "spritz-staging",
					"instanceId":  "zeno-acme",
					"providerAuth": map[string]any{
						"providerInstallRef": "cred_slack_workspace_1",
						"apiAppId":           "A_app_1",
						"teamId":             "T_workspace_1",
						"botUserId":          "U_bot",
						"botAccessToken":     "xoxb-installed",
					},
				},
			})
		}
	}))
	defer backend.Close()

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
					_ = conn.WriteJSON(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params": map[string]any{
							"update": map[string]any{
								"sessionUpdate": "agent_message_chunk",
								"content": []map[string]any{{
									"type": "text",
									"text": "Hello after transient exchange failure",
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
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    250 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected recovery after transient exchange error to succeed, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up status and final reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != slackRecoveryStatusText {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "Hello after transient exchange failure" {
		t.Fatalf("expected final reply text, got %#v", got)
	}
	if sessionExchangeCalls.Load() != 3 {
		t.Fatalf("expected recovery polling to continue through transient errors, got %d exchange attempts", sessionExchangeCalls.Load())
	}
}

func TestProcessMessageEventIgnoresMentionOnlyBeforeRecoveryStarts(t *testing.T) {
	var slackPostCalls atomic.Int32
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackPostCalls.Add(1)
		t.Fatalf("did not expect slack post for mention-only event")
	}))
	defer slackAPI.Close()

	var sessionExchangeCalls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionExchangeCalls.Add(1)
		t.Fatalf("did not expect session exchange for mention-only event")
	}))
	defer backend.Close()

	cfg := config{
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    200 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot>",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected mention-only app mention to be ignored cleanly, got %v", err)
	}
	if sessionExchangeCalls.Load() != 0 {
		t.Fatalf("expected no session exchange attempts, got %d", sessionExchangeCalls.Load())
	}
	if slackPostCalls.Load() != 0 {
		t.Fatalf("expected no slack posts, got %d", slackPostCalls.Load())
	}
}

func TestProcessMessageEventPostsTerminalErrorAfterRecoveryTimeout(t *testing.T) {
	var slackPayloads struct {
		sync.Mutex
		items []map[string]any
	}
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack post body: %v", err)
		}
		slackPayloads.Lock()
		slackPayloads.items = append(slackPayloads.items, payload)
		slackPayloads.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ts": fmt.Sprintf("1711387375.00010%d", len(slackPayloads.items))})
	}))
	defer slackAPI.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/spritz/channel-sessions/exchange" {
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unavailable",
			"providerAuth": map[string]any{
				"providerInstallRef": "cred_slack_workspace_1",
				"apiAppId":           "A_app_1",
				"teamId":             "T_workspace_1",
				"botUserId":          "U_bot",
				"botAccessToken":     "xoxb-installed",
			},
		})
	}))
	defer backend.Close()

	cfg := config{
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        "https://spritz.example.test",
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          200 * time.Millisecond,
		DedupeTTL:            time.Minute,
		StatusMessageDelay:   5 * time.Millisecond,
		SessionRetryInterval: 10 * time.Millisecond,
		ProcessingTimeout:    200 * time.Millisecond,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		APIAppID: "A_app_1",
		TeamID:   "T_workspace_1",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_user",
			Text:        "<@U_bot> hello",
			Channel:     "C_channel_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}
	delivery, process, err := gateway.beginMessageEventDelivery(envelope)
	if err != nil {
		t.Fatalf("beginMessageEventDelivery returned error: %v", err)
	}
	if !process {
		t.Fatal("expected app mention to be processed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := gateway.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
		t.Fatalf("expected recovery timeout to be handled with terminal Slack reply, got %v", err)
	}

	slackPayloads.Lock()
	defer slackPayloads.Unlock()
	if len(slackPayloads.items) != 2 {
		t.Fatalf("expected wake-up message and terminal error reply, got %#v", slackPayloads.items)
	}
	if got := slackPayloads.items[0]["text"]; got != "Still waking up. I will continue here shortly." {
		t.Fatalf("expected wake-up status text, got %#v", got)
	}
	if got := slackPayloads.items[1]["text"]; got != "I could not recover the channel runtime. Please try again." {
		t.Fatalf("expected terminal recovery error text, got %#v", got)
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var promptCalls atomic.Int32
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
					promptCalls.Add(1)
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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
		t.Fatalf("expected duplicate slack delivery to be suppressed after prompt side effects, got %v", err)
	}
	if promptCalls.Load() != 1 {
		t.Fatalf("expected ACP prompt to run once, got %d", promptCalls.Load())
	}
	if postCalls != 1 {
		t.Fatalf("expected one slack post attempt before dedupe suppression, got %d", postCalls)
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	var postCalls atomic.Int32
	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		postCalls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer slackAPI.Close()

	var promptCalls atomic.Int32
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
					promptCalls.Add(1)
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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
	if promptCalls.Load() != 2 {
		t.Fatalf("expected ACP prompt to run twice after retryable failures, got %d", promptCalls.Load())
	}
	if postCalls.Load() != 0 {
		t.Fatalf("expected no slack reply on undelivered prompt failure, got %d posts", postCalls.Load())
	}
}

func TestProcessMessageEventSuppressesSlackReplyWhenRuntimeHasNoVisibleOutput(t *testing.T) {
	var postCalls atomic.Int32
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
				"providerAuth": map[string]any{
					"providerInstallRef": "cred_slack_workspace_1",
					"apiAppId":           "A_app_1",
					"teamId":             "T_workspace_1",
					"botUserId":          "U_bot",
					"botAccessToken":     "xoxb-installed",
				},
			},
		})
	}))
	defer backend.Close()

	slackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("unexpected slack path %s", r.URL.Path)
		}
		postCalls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer slackAPI.Close()

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
		SlackSigningSecret:   "signing-secret",
		OAuthStateSecret:     "oauth-state-secret",
		SlackAPIBaseURL:      slackAPI.URL,
		BackendBaseURL:       backend.URL,
		BackendInternalToken: "backend-internal-token",
		SpritzBaseURL:        spritz.URL,
		SpritzServiceToken:   "spritz-service-token",
		PrincipalID:          "shared-slack-gateway",
		HTTPTimeout:          5 * time.Second,
		DedupeTTL:            time.Minute,
	}
	gateway := newSlackGateway(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	envelope := slackEnvelope{
		Type:     "event_callback",
		TeamID:   "T_workspace_1",
		APIAppID: "A_app_1",
		EventID:  "Ev_no_reply",
		Event: slackEventInner{
			Type:        "app_mention",
			User:        "U_1",
			Text:        "<@U_bot> hello",
			Channel:     "C_1",
			ChannelType: "channel",
			TS:          "1711387375.000100",
		},
	}

	if err := gateway.processMessageEvent(t.Context(), envelope); err != nil {
		t.Fatalf("expected empty visible output to be treated as success, got %v", err)
	}
	if postCalls.Load() != 0 {
		t.Fatalf("expected no slack post for empty visible output, got %d", postCalls.Load())
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

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "must be an absolute URL") {
		t.Fatalf("expected absolute-url validation error, got %v", err)
	}
}

func TestLoadConfigIncludesMPIMHistoryByDefault(t *testing.T) {
	t.Setenv("SPRITZ_SLACK_GATEWAY_PUBLIC_URL", "https://gateway.example.test")
	t.Setenv("SPRITZ_SLACK_CLIENT_ID", "client-id")
	t.Setenv("SPRITZ_SLACK_CLIENT_SECRET", "client-secret")
	t.Setenv("SPRITZ_SLACK_SIGNING_SECRET", "signing-secret")
	t.Setenv("SPRITZ_SLACK_OAUTH_STATE_SECRET", "oauth-state-secret")
	t.Setenv("SPRITZ_SLACK_BACKEND_BASE_URL", "https://backend.example.test")
	t.Setenv("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN", "backend-internal-token")
	t.Setenv("SPRITZ_SLACK_SPRITZ_BASE_URL", "https://spritz.example.test")
	t.Setenv("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN", "spritz-service-token")
	t.Setenv("SPRITZ_SLACK_PRINCIPAL_ID", "shared-slack-gateway")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if !containsString(cfg.SlackBotScopes, "mpim:history") {
		t.Fatalf("expected default Slack scopes to include mpim:history, got %#v", cfg.SlackBotScopes)
	}
	if cfg.PresetID != "zeno" {
		t.Fatalf("expected default preset id zeno, got %q", cfg.PresetID)
	}
}

func TestLoadConfigDefaultsBackendFastAPIBaseURLToBackendBaseURL(t *testing.T) {
	t.Setenv("SPRITZ_SLACK_GATEWAY_PUBLIC_URL", "https://gateway.example.test")
	t.Setenv("SPRITZ_SLACK_CLIENT_ID", "client-id")
	t.Setenv("SPRITZ_SLACK_CLIENT_SECRET", "client-secret")
	t.Setenv("SPRITZ_SLACK_SIGNING_SECRET", "signing-secret")
	t.Setenv("SPRITZ_SLACK_OAUTH_STATE_SECRET", "oauth-state-secret")
	t.Setenv("SPRITZ_SLACK_BACKEND_BASE_URL", "https://backend.example.test")
	t.Setenv("SPRITZ_SLACK_BACKEND_INTERNAL_TOKEN", "backend-internal-token")
	t.Setenv("SPRITZ_SLACK_SPRITZ_BASE_URL", "https://spritz.example.test")
	t.Setenv("SPRITZ_SLACK_SPRITZ_SERVICE_TOKEN", "spritz-service-token")
	t.Setenv("SPRITZ_SLACK_PRINCIPAL_ID", "shared-slack-gateway")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.BackendFastAPIBaseURL != "https://backend.example.test" {
		t.Fatalf(
			"expected backend fastapi base url fallback to match backend base url, got %q",
			cfg.BackendFastAPIBaseURL,
		)
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

func TestReactRouteURLUsesSpritzBaseURL(t *testing.T) {
	gateway := newSlackGateway(
		config{SpritzBaseURL: "https://spritz.example.test/app"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	target := gateway.reactRouteURL("/settings/slack/workspaces/test?teamId=T_workspace_1")
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse react route url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "spritz.example.test" {
		t.Fatalf("expected Spritz host, got %s", target)
	}
	if parsed.Path != "/app/settings/slack/workspaces/test" {
		t.Fatalf("expected Spritz base path to be preserved, got %q", parsed.Path)
	}
	if parsed.Query().Get("teamId") != "T_workspace_1" {
		t.Fatalf("expected query to be preserved, got %q", parsed.RawQuery)
	}
}

func TestReactRoutesShareGatewayOrigin(t *testing.T) {
	sameOrigin := newSlackGateway(
		config{
			PublicURL:     "https://spritz.example.test/slack-gateway",
			SpritzBaseURL: "https://spritz.example.test",
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if !sameOrigin.reactRoutesShareGatewayOrigin() {
		t.Fatal("expected same host and scheme to share gateway origin")
	}

	crossOrigin := newSlackGateway(
		config{
			PublicURL:     "https://gateway.example.test",
			SpritzBaseURL: "https://spritz.example.test",
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if crossOrigin.reactRoutesShareGatewayOrigin() {
		t.Fatal("expected different hosts not to share gateway origin")
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

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
