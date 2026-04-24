package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

type httpStatusError struct {
	method     string
	endpoint   string
	statusCode int
	body       string
}

func (err *httpStatusError) Error() string {
	return fmt.Sprintf(
		"%s %s failed: %d %s",
		err.method,
		err.endpoint,
		err.statusCode,
		strings.TrimSpace(err.body),
	)
}

type backendChannelSessionResponse struct {
	Status  string `json:"status"`
	Session struct {
		AccessToken        string             `json:"accessToken"`
		ExpiresAt          string             `json:"expiresAt"`
		OwnerAuthID        string             `json:"ownerAuthId"`
		Namespace          string             `json:"namespace"`
		InstanceID         string             `json:"instanceId"`
		ProviderAuth       slackInstallation  `json:"providerAuth"`
		InstallationConfig installationConfig `json:"installationConfig"`
	} `json:"session"`
}

type backendChannelSessionUnavailableResponse struct {
	Status             string             `json:"status"`
	ProviderAuth       slackInstallation  `json:"providerAuth"`
	InstallationConfig installationConfig `json:"installationConfig"`
}

type backendInstallationUpsertResponse struct {
	Status       string `json:"status"`
	Installation struct {
		ProviderInstallRef string `json:"providerInstallRef"`
	} `json:"installation"`
}

type backendInstallTargetProfile struct {
	Name     string `json:"name"`
	ImageURL string `json:"imageUrl,omitempty"`
}

type backendInstallTarget struct {
	ID           string                      `json:"id"`
	Profile      backendInstallTargetProfile `json:"profile"`
	OwnerLabel   string                      `json:"ownerLabel,omitempty"`
	PresetInputs map[string]any              `json:"presetInputs"`
}

type backendInstallTargetListResponse struct {
	Status  string                 `json:"status"`
	Targets []backendInstallTarget `json:"targets"`
}

type backendInstallationTargetSummary struct {
	ID         string                      `json:"id"`
	Profile    backendInstallTargetProfile `json:"profile"`
	OwnerLabel string                      `json:"ownerLabel,omitempty"`
}

type backendManagedInstallationRoute struct {
	PrincipalID       string `json:"principalId"`
	Provider          string `json:"provider"`
	ExternalScopeType string `json:"externalScopeType"`
	ExternalTenantID  string `json:"externalTenantId"`
}

type backendManagedInstallation struct {
	Route              backendManagedInstallationRoute   `json:"route"`
	State              string                            `json:"state"`
	CurrentTarget      *backendInstallationTargetSummary `json:"currentTarget,omitempty"`
	InstallationConfig installationConfig                `json:"installationConfig"`
	AllowedActions     []string                          `json:"allowedActions"`
	ProblemCode        string                            `json:"problemCode,omitempty"`
	DisconnectedAt     string                            `json:"disconnectedAt,omitempty"`
}

type backendManagedInstallationListResponse struct {
	Status        string                       `json:"status"`
	Installations []backendManagedInstallation `json:"installations"`
}

type backendManagedInstallationUpdateResponse struct {
	Status            string `json:"status"`
	NeedsProvisioning bool   `json:"needsProvisioning"`
}

type spritzConversationUpsertResponse struct {
	Status string `json:"status"`
	Data   struct {
		Conversation struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				SessionID string `json:"sessionId"`
				CWD       string `json:"cwd"`
			} `json:"spec"`
		} `json:"conversation"`
	} `json:"data"`
}

type spritzBootstrapResponse struct {
	Status string `json:"status"`
	Data   struct {
		EffectiveSessionID string `json:"effectiveSessionId"`
		EffectiveCWD       string `json:"effectiveCwd"`
		Conversation       struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				SessionID string `json:"sessionId"`
				CWD       string `json:"cwd"`
			} `json:"spec"`
			Status struct {
				EffectiveCWD string `json:"effectiveCwd"`
			} `json:"status"`
		} `json:"conversation"`
	} `json:"data"`
}

type channelSession struct {
	AccessToken        string
	OwnerAuthID        string
	Namespace          string
	InstanceID         string
	ProviderAuth       slackInstallation
	InstallationConfig installationConfig
}

type channelSessionUnavailableError struct {
	providerAuth       slackInstallation
	installationConfig installationConfig
	cause              *httpStatusError
}

func (err *channelSessionUnavailableError) Error() string {
	if err == nil {
		return "channel session unavailable"
	}
	if err.cause == nil {
		return "channel session unavailable"
	}
	return err.cause.Error()
}

func (err *channelSessionUnavailableError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func channelSessionUnavailableProviderAuth(err error) (slackInstallation, bool) {
	var unavailableErr *channelSessionUnavailableError
	if !errors.As(err, &unavailableErr) {
		return slackInstallation{}, false
	}
	if strings.TrimSpace(unavailableErr.providerAuth.BotAccessToken) == "" {
		return slackInstallation{}, false
	}
	return unavailableErr.providerAuth, true
}

func channelSessionUnavailablePolicySnapshot(err error) (installationPolicySnapshot, bool) {
	var unavailableErr *channelSessionUnavailableError
	if !errors.As(err, &unavailableErr) {
		return installationPolicySnapshot{}, false
	}
	return installationPolicySnapshot{
		config:    unavailableErr.installationConfig,
		botUserID: strings.TrimSpace(unavailableErr.providerAuth.BotUserID),
	}, true
}

func isSpritzRuntimeMissingError(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	if statusErr.statusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(statusErr.body), "spritz not found")
}

func isACPUnavailableError(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	if statusErr.statusCode != http.StatusConflict {
		return false
	}
	return strings.Contains(strings.ToLower(statusErr.body), "acp unavailable")
}

func (g *slackGateway) exchangeChannelSession(ctx context.Context, teamID string, forceRefresh bool) (channelSession, error) {
	body := map[string]any{
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  strings.TrimSpace(teamID),
	}
	if forceRefresh {
		body["forceRefresh"] = true
	}
	var payload backendChannelSessionResponse
	if err := g.postBackendJSON(ctx, "/internal/v1/spritz/channel-sessions/exchange", body, &payload); err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusServiceUnavailable {
			var unavailablePayload backendChannelSessionUnavailableResponse
			if json.Unmarshal([]byte(statusErr.body), &unavailablePayload) == nil && strings.TrimSpace(unavailablePayload.Status) == "unavailable" {
				return channelSession{}, &channelSessionUnavailableError{
					providerAuth:       unavailablePayload.ProviderAuth,
					installationConfig: unavailablePayload.InstallationConfig,
					cause:              statusErr,
				}
			}
			return channelSession{}, &channelSessionUnavailableError{cause: statusErr}
		}
		return channelSession{}, err
	}
	if payload.Status != "resolved" {
		return channelSession{}, fmt.Errorf("channel session was not resolved")
	}
	return channelSession{
		AccessToken:        payload.Session.AccessToken,
		OwnerAuthID:        payload.Session.OwnerAuthID,
		Namespace:          payload.Session.Namespace,
		InstanceID:         payload.Session.InstanceID,
		ProviderAuth:       payload.Session.ProviderAuth,
		InstallationConfig: payload.Session.InstallationConfig,
	}, nil
}

func (g *slackGateway) listInstallTargets(ctx context.Context, installation *slackInstallation, requestID string) ([]backendInstallTarget, error) {
	if installation == nil {
		return nil, fmt.Errorf("installation is required")
	}
	return g.listInstallTargetsForOwnerAuthID(
		ctx,
		strings.TrimSpace(installation.TeamID),
		"",
		requestID,
		map[string]any{
			"type":     "external",
			"provider": slackProvider,
			"subject":  installation.InstallingUserID,
			"tenant":   installation.TeamID,
		},
	)
}

func (g *slackGateway) listInstallTargetsForOwnerAuthID(ctx context.Context, teamID, ownerAuthID, requestID string, ownerRef map[string]any) ([]backendInstallTarget, error) {
	body := map[string]any{
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  strings.TrimSpace(teamID),
		"presetId":          g.presetID(),
		"requestId":         strings.TrimSpace(requestID),
	}
	if strings.TrimSpace(ownerAuthID) != "" {
		body["ownerAuthId"] = strings.TrimSpace(ownerAuthID)
	} else if ownerRef != nil {
		body["ownerRef"] = ownerRef
	} else {
		return nil, fmt.Errorf("owner auth id or owner ref is required")
	}
	var payload backendInstallTargetListResponse
	if err := g.postBackendFastAPIJSON(ctx, "/internal/v2/spritz/channel-install-targets/list", body, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "resolved" {
		return nil, fmt.Errorf("channel install targets were not resolved")
	}
	return payload.Targets, nil
}

func (g *slackGateway) listManagedInstallations(ctx context.Context, callerAuthID string) ([]backendManagedInstallation, error) {
	body := map[string]any{
		"callerAuthId":      strings.TrimSpace(callerAuthID),
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
	}
	var payload backendManagedInstallationListResponse
	if err := g.postBackendFastAPIJSON(ctx, "/internal/v2/spritz/channel-installations/list", body, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "resolved" {
		return nil, fmt.Errorf("channel installations were not resolved")
	}
	return payload.Installations, nil
}

func (g *slackGateway) updateManagedInstallationTarget(ctx context.Context, callerAuthID, teamID, requestID string, presetInputs map[string]any) error {
	body := map[string]any{
		"callerAuthId":      strings.TrimSpace(callerAuthID),
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  strings.TrimSpace(teamID),
		"presetInputs":      presetInputs,
		"requestId":         strings.TrimSpace(requestID),
	}
	var payload backendManagedInstallationUpdateResponse
	if err := g.postBackendFastAPIJSON(ctx, "/internal/v2/spritz/channel-installations/target/update", body, &payload); err != nil {
		return err
	}
	if payload.Status != "resolved" {
		return fmt.Errorf("channel installation target was not updated")
	}
	return nil
}

func (g *slackGateway) updateManagedInstallationConfig(ctx context.Context, callerAuthID, teamID, requestID string, installationConfig installationConfig) error {
	body := map[string]any{
		"callerAuthId":       strings.TrimSpace(callerAuthID),
		"principalId":        g.cfg.PrincipalID,
		"provider":           slackProvider,
		"externalScopeType":  slackWorkspaceScope,
		"externalTenantId":   strings.TrimSpace(teamID),
		"installationConfig": installationConfig,
		"requestId":          strings.TrimSpace(requestID),
	}
	var payload backendManagedInstallationUpdateResponse
	if err := g.postBackendFastAPIJSON(ctx, "/internal/v2/spritz/channel-installations/config/update", body, &payload); err != nil {
		return err
	}
	if payload.Status != "resolved" {
		return fmt.Errorf("channel installation config was not updated")
	}
	return nil
}

func (g *slackGateway) disconnectManagedInstallation(ctx context.Context, callerAuthID, teamID string) error {
	body := map[string]any{
		"callerAuthId":      strings.TrimSpace(callerAuthID),
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  strings.TrimSpace(teamID),
	}
	return g.postBackendFastAPIJSON(ctx, "/internal/v2/spritz/channel-installations/disconnect", body, nil)
}

func (g *slackGateway) upsertChannelConversation(ctx context.Context, session channelSession, event slackEventInner, teamID, conversationID, externalConversationID string, lookupExternalConversationIDs []string) (string, error) {
	body := map[string]any{
		"namespace":              session.Namespace,
		"conversationId":         strings.TrimSpace(conversationID),
		"principalId":            g.cfg.PrincipalID,
		"instanceId":             session.InstanceID,
		"ownerId":                session.OwnerAuthID,
		"provider":               slackProvider,
		"externalScopeType":      slackWorkspaceScope,
		"externalTenantId":       strings.TrimSpace(teamID),
		"externalChannelId":      strings.TrimSpace(event.Channel),
		"externalConversationId": strings.TrimSpace(externalConversationID),
		"title":                  fmt.Sprintf("Slack %s", strings.TrimSpace(event.Channel)),
	}
	if len(lookupExternalConversationIDs) > 0 {
		body["lookupExternalConversationIds"] = lookupExternalConversationIDs
	}
	var payload spritzConversationUpsertResponse
	if err := g.postSpritzJSON(ctx, http.MethodPost, "/api/channel-conversations/upsert", session.AccessToken, body, &payload, nil); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Data.Conversation.Metadata.Name), nil
}

func (g *slackGateway) bootstrapConversation(ctx context.Context, serviceToken, namespace, conversationID string) (string, string, error) {
	var payload spritzBootstrapResponse
	if err := g.postSpritzJSON(ctx, http.MethodPost, "/api/acp/conversations/"+url.PathEscape(conversationID)+"/bootstrap", serviceToken, nil, &payload, map[string]string{"namespace": namespace}); err != nil {
		return "", "", err
	}
	sessionID := strings.TrimSpace(payload.Data.EffectiveSessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(payload.Data.Conversation.Spec.SessionID)
	}
	cwd := firstNonEmpty(
		payload.Data.EffectiveCWD,
		payload.Data.Conversation.Status.EffectiveCWD,
		payload.Data.Conversation.Spec.CWD,
	)
	if sessionID == "" {
		return "", "", fmt.Errorf("bootstrap did not return a session id")
	}
	if cwd == "" {
		return "", "", fmt.Errorf("bootstrap did not return a cwd")
	}
	return sessionID, cwd, nil
}

func (g *slackGateway) postSlackMessage(ctx context.Context, token, channel, text, threadTS string) (string, error) {
	body := map[string]any{
		"channel": strings.TrimSpace(channel),
		"text":    text,
	}
	if threadTS = strings.TrimSpace(threadTS); threadTS != "" {
		body["thread_ts"] = threadTS
	}
	target := g.cfg.SlackAPIBaseURL + "/chat.postMessage"
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	reqCtx, cancel := g.requestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("slack chat.postMessage failed: %s", resp.Status)
	}
	var result struct {
		OK    bool   `json:"ok"`
		TS    string `json:"ts,omitempty"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("slack chat.postMessage failed: %s", strings.TrimSpace(result.Error))
	}
	return strings.TrimSpace(result.TS), nil
}

func (g *slackGateway) postBackendJSON(ctx context.Context, path string, body any, target any) error {
	return g.postJSON(ctx, g.cfg.BackendBaseURL+path, g.cfg.BackendInternalToken, body, target)
}

func (g *slackGateway) postBackendFastAPIJSON(ctx context.Context, path string, body any, target any) error {
	baseURL := g.cfg.BackendFastAPIBaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = g.cfg.BackendBaseURL
	}
	return g.postJSON(ctx, baseURL+path, g.cfg.BackendInternalToken, body, target)
}

func (g *slackGateway) postSpritzJSON(ctx context.Context, method, path, bearer string, body any, target any, query map[string]string) error {
	endpoint := g.cfg.SpritzBaseURL + path
	if len(query) > 0 {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return err
		}
		values := parsed.Query()
		for key, value := range query {
			values.Set(key, value)
		}
		parsed.RawQuery = values.Encode()
		endpoint = parsed.String()
	}
	return g.postJSONWithMethod(ctx, method, endpoint, bearer, body, target)
}

func (g *slackGateway) postJSON(ctx context.Context, endpoint, bearer string, body any, target any) error {
	return g.postJSONWithMethod(ctx, http.MethodPost, endpoint, bearer, body, target)
}

func (g *slackGateway) postJSONWithMethod(ctx context.Context, method, endpoint, bearer string, body any, target any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	} else {
		payload = []byte("{}")
	}
	reqCtx, cancel := g.requestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpStatusError{
			method:     method,
			endpoint:   endpoint,
			statusCode: resp.StatusCode,
			body:       string(bodyBytes),
		}
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (g *slackGateway) spritzWebSocketURL(routePath string, query map[string]string) (string, error) {
	parsed, err := url.Parse(g.cfg.SpritzBaseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported spritz url scheme %q", parsed.Scheme)
	}
	parsed.Path = path.Join(parsed.Path, routePath)
	values := parsed.Query()
	for key, value := range query {
		values.Set(key, value)
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}
