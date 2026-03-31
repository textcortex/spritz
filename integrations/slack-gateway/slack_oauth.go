package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type slackOAuthAccessResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	AppID       string `json:"app_id,omitempty"`
	Scope       string `json:"scope,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	BotUserID   string `json:"bot_user_id,omitempty"`
	Team        struct {
		ID string `json:"id"`
	} `json:"team"`
	Enterprise *struct {
		ID string `json:"id"`
	} `json:"enterprise,omitempty"`
	AuthedUser struct {
		ID string `json:"id"`
	} `json:"authed_user"`
}

type slackInstallation struct {
	ProviderInstallRef string   `json:"providerInstallRef,omitempty"`
	APIAppID           string   `json:"apiAppId,omitempty"`
	TeamID             string   `json:"teamId,omitempty"`
	EnterpriseID       string   `json:"enterpriseId,omitempty"`
	InstallingUserID   string   `json:"installingUserId,omitempty"`
	BotUserID          string   `json:"botUserId,omitempty"`
	ScopeSet           []string `json:"scopeSet,omitempty"`
	BotAccessToken     string   `json:"botAccessToken,omitempty"`
}

func (g *slackGateway) handleInstallRedirect(w http.ResponseWriter, r *http.Request) {
	state, err := g.state.generate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target, err := slackOAuthAuthorizeURL(g.cfg.SlackAPIBaseURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	query := target.Query()
	query.Set("client_id", g.cfg.SlackClientID)
	query.Set("scope", strings.Join(g.cfg.SlackBotScopes, ","))
	query.Set("redirect_uri", g.oauthCallbackURL())
	query.Set("state", state)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func slackOAuthAuthorizeURL(apiBaseURL string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(apiBaseURL))
	if err != nil {
		return nil, err
	}
	target.Path = "/oauth/v2/authorize"
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	return target, nil
}

func (g *slackGateway) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if err := g.state.validate(state); err != nil {
		g.logger.ErrorContext(r.Context(), "slack oauth callback state validation failed", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if code == "" {
		g.logger.ErrorContext(r.Context(), "slack oauth callback missing code")
		http.Error(w, "code is required", http.StatusBadRequest)
		return
	}

	installation, err := g.exchangeSlackOAuthCode(r.Context(), code)
	if err != nil {
		g.logger.ErrorContext(r.Context(), "slack oauth callback code exchange failed", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := g.upsertInstallation(r.Context(), &installation); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"slack oauth callback installation upsert failed",
			"err",
			err,
			"team_id",
			installation.TeamID,
			"installing_user_id",
			installation.InstallingUserID,
		)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	g.logger.InfoContext(
		r.Context(),
		"slack oauth callback installed workspace",
		"team_id",
		installation.TeamID,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "installed",
		"teamId":             installation.TeamID,
		"providerInstallRef": installation.ProviderInstallRef,
	})
}

func (g *slackGateway) oauthCallbackURL() string {
	return g.cfg.PublicURL + "/slack/oauth/callback"
}

func (g *slackGateway) exchangeSlackOAuthCode(ctx context.Context, code string) (slackInstallation, error) {
	form := url.Values{}
	form.Set("client_id", g.cfg.SlackClientID)
	form.Set("client_secret", g.cfg.SlackClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", g.oauthCallbackURL())
	reqCtx, cancel := g.requestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, g.cfg.SlackAPIBaseURL+"/oauth.v2.access", strings.NewReader(form.Encode()))
	if err != nil {
		return slackInstallation{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return slackInstallation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return slackInstallation{}, fmt.Errorf("slack oauth exchange failed: %s", resp.Status)
	}
	var payload slackOAuthAccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return slackInstallation{}, err
	}
	if !payload.OK || strings.TrimSpace(payload.AccessToken) == "" || strings.TrimSpace(payload.Team.ID) == "" {
		return slackInstallation{}, fmt.Errorf("slack oauth exchange failed: %s", strings.TrimSpace(payload.Error))
	}
	record := slackInstallation{
		ProviderInstallRef: "slack-install:" + strings.TrimSpace(payload.Team.ID),
		APIAppID:           strings.TrimSpace(payload.AppID),
		TeamID:             strings.TrimSpace(payload.Team.ID),
		InstallingUserID:   strings.TrimSpace(payload.AuthedUser.ID),
		BotUserID:          strings.TrimSpace(payload.BotUserID),
		ScopeSet:           splitCSV(payload.Scope),
		BotAccessToken:     strings.TrimSpace(payload.AccessToken),
	}
	if payload.Enterprise != nil {
		record.EnterpriseID = strings.TrimSpace(payload.Enterprise.ID)
	}
	return record, nil
}

func (g *slackGateway) upsertInstallation(ctx context.Context, installation *slackInstallation) error {
	if installation == nil {
		return fmt.Errorf("installation is required")
	}
	body := map[string]any{
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  installation.TeamID,
		"presetId":          g.presetID(),
		"ownerRef": map[string]any{
			"type":     "external",
			"provider": slackProvider,
			"subject":  installation.InstallingUserID,
			"tenant":   installation.TeamID,
		},
		"providerAuth": map[string]any{
			"apiAppId":         installation.APIAppID,
			"teamId":           installation.TeamID,
			"enterpriseId":     installation.EnterpriseID,
			"installingUserId": installation.InstallingUserID,
			"botUserId":        installation.BotUserID,
			"botAccessToken":   installation.BotAccessToken,
			"scopeSet":         installation.ScopeSet,
		},
	}
	var payload backendInstallationUpsertResponse
	if err := g.postBackendJSON(ctx, "/internal/v1/spritz/channel-installations/upsert", body, &payload); err != nil {
		return err
	}
	if payload.Status != "resolved" {
		return fmt.Errorf("channel installation was not resolved")
	}
	if providerInstallRef := strings.TrimSpace(payload.Installation.ProviderInstallRef); providerInstallRef != "" {
		installation.ProviderInstallRef = providerInstallRef
	}
	return nil
}

func (g *slackGateway) disconnectInstallation(ctx context.Context, teamID string) error {
	body := map[string]any{
		"principalId":       g.cfg.PrincipalID,
		"provider":          slackProvider,
		"externalScopeType": slackWorkspaceScope,
		"externalTenantId":  strings.TrimSpace(teamID),
	}
	return g.postBackendJSON(ctx, "/internal/v1/spritz/channel-installations/disconnect", body, nil)
}
