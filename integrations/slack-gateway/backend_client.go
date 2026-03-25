package main

import (
	"bytes"
	"context"
	"encoding/json"
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
		AccessToken  string            `json:"accessToken"`
		ExpiresAt    string            `json:"expiresAt"`
		OwnerAuthID  string            `json:"ownerAuthId"`
		Namespace    string            `json:"namespace"`
		InstanceID   string            `json:"instanceId"`
		ProviderAuth slackInstallation `json:"providerAuth"`
	} `json:"session"`
}

type backendInstallationUpsertResponse struct {
	Status       string `json:"status"`
	Installation struct {
		ProviderInstallRef string `json:"providerInstallRef"`
	} `json:"installation"`
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
		Conversation       struct {
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

type channelSession struct {
	AccessToken  string
	OwnerAuthID  string
	Namespace    string
	InstanceID   string
	ProviderAuth slackInstallation
}

func (g *slackGateway) exchangeChannelSession(ctx context.Context, teamID string) (channelSession, error) {
	body := map[string]any{
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  strings.TrimSpace(teamID),
	}
	var payload backendChannelSessionResponse
	if err := g.postBackendJSON(ctx, "/internal/v1/spritz/channel-sessions/exchange", body, &payload); err != nil {
		return channelSession{}, err
	}
	if payload.Status != "resolved" {
		return channelSession{}, fmt.Errorf("channel session was not resolved")
	}
	return channelSession{
		AccessToken:  payload.Session.AccessToken,
		OwnerAuthID:  payload.Session.OwnerAuthID,
		Namespace:    payload.Session.Namespace,
		InstanceID:   payload.Session.InstanceID,
		ProviderAuth: payload.Session.ProviderAuth,
	}, nil
}

func (g *slackGateway) upsertChannelConversation(ctx context.Context, session channelSession, event slackEventInner, teamID, externalConversationID string) (string, error) {
	body := map[string]any{
		"namespace":              session.Namespace,
		"principalId":            g.cfg.PrincipalID,
		"instanceId":             session.InstanceID,
		"ownerId":                session.OwnerAuthID,
		"provider":               slackProvider,
		"externalScopeType":      slackWorkspaceScope,
		"externalTenantId":       strings.TrimSpace(teamID),
		"externalChannelId":      strings.TrimSpace(event.Channel),
		"externalConversationId": strings.TrimSpace(externalConversationID),
		"title":                  fmt.Sprintf("Slack %s", strings.TrimSpace(event.Channel)),
		"cwd":                    defaultConversationCWD,
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
	cwd := strings.TrimSpace(payload.Data.Conversation.Spec.CWD)
	if cwd == "" {
		cwd = defaultConversationCWD
	}
	if sessionID == "" {
		return "", "", fmt.Errorf("bootstrap did not return a session id")
	}
	return sessionID, cwd, nil
}

func (g *slackGateway) postSlackMessage(ctx context.Context, token, channel, text, threadTS string) (string, error) {
	body := map[string]any{
		"channel": strings.TrimSpace(channel),
		"text":    strings.TrimSpace(text),
	}
	if threadTS = strings.TrimSpace(threadTS); threadTS != "" {
		body["thread_ts"] = threadTS
	}
	target := g.cfg.SlackAPIBaseURL + "/chat.postMessage"
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
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
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
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
