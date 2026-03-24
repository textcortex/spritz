package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	slackProvider          = "slack"
	slackWorkspaceScope    = "workspace"
	defaultConversationCWD = "/home/dev"
)

type slackGateway struct {
	cfg        config
	httpClient *http.Client
	state      *oauthStateManager
	dedupe     *dedupeStore
	logger     *slog.Logger
	workers    sync.WaitGroup
}

type dedupeStore struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]dedupeEntry
}

type dedupeEntry struct {
	seenAt   time.Time
	inFlight bool
}

type dedupeLease struct {
	store *dedupeStore
	key   string
}

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

type slackEnvelope struct {
	Type      string          `json:"type"`
	Challenge string          `json:"challenge,omitempty"`
	APIAppID  string          `json:"api_app_id,omitempty"`
	TeamID    string          `json:"team_id,omitempty"`
	EventID   string          `json:"event_id,omitempty"`
	Event     slackEventInner `json:"event,omitempty"`
}

type slackEventInner struct {
	Type        string `json:"type,omitempty"`
	Subtype     string `json:"subtype,omitempty"`
	User        string `json:"user,omitempty"`
	BotID       string `json:"bot_id,omitempty"`
	Text        string `json:"text,omitempty"`
	Channel     string `json:"channel,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	TS          string `json:"ts,omitempty"`
	ThreadTS    string `json:"thread_ts,omitempty"`
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

type acpRPCMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

func newDedupeStore(ttl time.Duration) *dedupeStore {
	return &dedupeStore{
		ttl:     ttl,
		now:     time.Now,
		entries: map[string]dedupeEntry{},
	}
}

func (d *dedupeStore) begin(key string) (*dedupeLease, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now().UTC()
	cutoff := now.Add(-d.ttl)
	for candidate, entry := range d.entries {
		if entry.seenAt.Before(cutoff) {
			delete(d.entries, candidate)
		}
	}
	if entry, ok := d.entries[key]; ok && now.Sub(entry.seenAt) <= d.ttl {
		return nil, true
	}
	d.entries[key] = dedupeEntry{seenAt: now, inFlight: true}
	return &dedupeLease{store: d, key: key}, false
}

func (l *dedupeLease) finish(success bool) {
	if l == nil || l.store == nil || l.key == "" {
		return
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	entry, ok := l.store.entries[l.key]
	if !ok || !entry.inFlight {
		return
	}
	if !success {
		delete(l.store.entries, l.key)
		return
	}
	entry.inFlight = false
	entry.seenAt = l.store.now().UTC()
	l.store.entries[l.key] = entry
}

func newSlackGateway(cfg config, logger *slog.Logger) *slackGateway {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 15 * time.Second
	}
	if cfg.DedupeTTL <= 0 {
		cfg.DedupeTTL = 10 * time.Minute
	}
	if cfg.ProcessingTimeout <= 0 {
		cfg.ProcessingTimeout = 60 * time.Second
	}
	return &slackGateway{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.HTTPTimeout},
		state:      newOAuthStateManager(cfg.OAuthStateSecret, 15*time.Minute),
		dedupe:     newDedupeStore(cfg.DedupeTTL),
		logger:     logger,
	}
}

func (g *slackGateway) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", g.handleHealthz)
	mux.HandleFunc("/slack/install", g.handleInstallRedirect)
	mux.HandleFunc("/slack/oauth/callback", g.handleOAuthCallback)
	mux.HandleFunc("/slack/events", g.handleSlackEvents)
	return mux
}

func (g *slackGateway) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (g *slackGateway) handleInstallRedirect(w http.ResponseWriter, r *http.Request) {
	state, err := g.state.generate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target, err := url.Parse("https://slack.com/oauth/v2/authorize")
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

func (g *slackGateway) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if err := g.state.validate(state); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if code == "" {
		http.Error(w, "code is required", http.StatusBadRequest)
		return
	}

	installation, err := g.exchangeSlackOAuthCode(r.Context(), code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := g.upsertInstallation(r.Context(), &installation); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "installed",
		"teamId":             installation.TeamID,
		"providerInstallRef": installation.ProviderInstallRef,
	})
}

func (g *slackGateway) handleSlackEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if err := g.verifySlackSignature(r.Header, body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	var envelope slackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	switch envelope.Type {
	case "url_verification":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(envelope.Challenge))
		return
	case "event_callback":
		g.processSlackEnvelopeAsync(envelope)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}
}

func (g *slackGateway) processSlackEnvelopeAsync(envelope slackEnvelope) {
	g.workers.Add(1)
	go func() {
		defer g.workers.Done()
		ctx, cancel := context.WithTimeout(context.Background(), g.cfg.ProcessingTimeout)
		defer cancel()
		if err := g.processSlackEnvelope(ctx, envelope); err != nil {
			g.logger.Error(
				"slack event failed",
				"error",
				err,
				"team_id",
				envelope.TeamID,
				"event_id",
				envelope.EventID,
				"event_type",
				envelope.Event.Type,
			)
		}
	}()
}

func (g *slackGateway) processSlackEnvelope(ctx context.Context, envelope slackEnvelope) error {
	switch envelope.Event.Type {
	case "app_uninstalled":
		return g.disconnectInstallation(ctx, envelope.TeamID)
	case "message", "app_mention":
		return g.processMessageEvent(ctx, envelope)
	default:
		return nil
	}
}

func (g *slackGateway) processMessageEvent(ctx context.Context, envelope slackEnvelope) error {
	event := envelope.Event
	if shouldIgnoreSlackMessageEvent(event) {
		return nil
	}
	if !shouldProcessSlackMessageEvent(event) {
		return nil
	}
	if strings.TrimSpace(event.BotID) != "" || strings.TrimSpace(event.User) == "" {
		return nil
	}
	if strings.TrimSpace(event.Channel) == "" || strings.TrimSpace(event.TS) == "" || strings.TrimSpace(envelope.TeamID) == "" {
		return nil
	}

	messageKey := strings.Join([]string{envelope.TeamID, event.Channel, event.TS}, ":")
	messageLease, duplicated := g.dedupe.begin("message:" + messageKey)
	if duplicated {
		return nil
	}
	success := false
	defer func() {
		messageLease.finish(success)
	}()

	var eventLease *dedupeLease
	if eventID := strings.TrimSpace(envelope.EventID); eventID != "" {
		eventLease, duplicated = g.dedupe.begin("event:" + eventID)
		if duplicated {
			return nil
		}
		defer func() {
			eventLease.finish(success)
		}()
	}

	session, err := g.exchangeChannelSession(ctx, envelope.TeamID)
	if err != nil {
		return err
	}
	if session.ProviderAuth.APIAppID != "" && strings.TrimSpace(envelope.APIAppID) != "" && session.ProviderAuth.APIAppID != strings.TrimSpace(envelope.APIAppID) {
		return fmt.Errorf("slack api_app_id mismatch for team %s", envelope.TeamID)
	}

	promptText := normalizeSlackPromptText(event.Type, event.Text, session.ProviderAuth.BotUserID)
	if promptText == "" {
		return nil
	}

	conversationID, err := g.upsertChannelConversation(ctx, session, event, envelope.TeamID)
	if err != nil {
		return err
	}
	sessionID, cwd, err := g.bootstrapConversation(ctx, session.AccessToken, session.Namespace, conversationID)
	if err != nil {
		return err
	}
	reply, promptSent, err := g.promptConversation(ctx, session.AccessToken, session.Namespace, conversationID, sessionID, cwd, promptText)
	if err != nil {
		if !promptSent {
			return err
		}
		reply = "I hit an internal error while processing that request."
		success = true
		g.logger.Error("acp prompt failed", "error", err, "conversation_id", conversationID)
	} else {
		success = true
	}
	if err := g.postSlackMessage(ctx, session.ProviderAuth.BotAccessToken, event.Channel, reply, slackReplyThreadTS(event)); err != nil {
		return err
	}
	return nil
}

func shouldIgnoreSlackMessageEvent(event slackEventInner) bool {
	subtype := strings.TrimSpace(event.Subtype)
	return subtype != "" && subtype != "file_share"
}

func shouldProcessSlackMessageEvent(event slackEventInner) bool {
	eventType := strings.TrimSpace(event.Type)
	if isSlackDirectMessageEvent(event) {
		return eventType == "message"
	}
	if eventType == "app_mention" {
		return true
	}
	return strings.TrimSpace(event.ThreadTS) != ""
}

func isSlackDirectMessageEvent(event slackEventInner) bool {
	switch strings.TrimSpace(event.ChannelType) {
	case "im", "mpim":
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(event.Channel), "D")
}

func (g *slackGateway) waitForWorkers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizeSlackPromptText(eventType, text, botUserID string) string {
	normalized := strings.TrimSpace(text)
	if strings.TrimSpace(eventType) == "app_mention" {
		botUserID = strings.TrimSpace(botUserID)
		if botUserID != "" {
			mentionToken := "<@" + botUserID + ">"
			if index := strings.Index(normalized, mentionToken); index >= 0 {
				normalized = strings.TrimSpace(
					normalized[:index] + normalized[index+len(mentionToken):],
				)
			}
		}
	}
	return normalized
}

func slackReplyThreadTS(event slackEventInner) string {
	if strings.TrimSpace(event.ThreadTS) != "" {
		return strings.TrimSpace(event.ThreadTS)
	}
	if isSlackDirectMessageEvent(event) {
		return ""
	}
	return strings.TrimSpace(event.TS)
}

func slackExternalConversationID(event slackEventInner) string {
	if isSlackDirectMessageEvent(event) {
		return strings.TrimSpace(event.Channel)
	}
	return firstNonEmpty(strings.TrimSpace(event.ThreadTS), strings.TrimSpace(event.TS))
}

func (g *slackGateway) verifySlackSignature(header http.Header, body []byte) error {
	timestampRaw := strings.TrimSpace(header.Get("X-Slack-Request-Timestamp"))
	signature := strings.TrimSpace(header.Get("X-Slack-Signature"))
	if timestampRaw == "" || signature == "" {
		return errors.New("missing slack signature")
	}
	timestamp, err := strconv.ParseInt(timestampRaw, 10, 64)
	if err != nil {
		return errors.New("invalid slack timestamp")
	}
	now := time.Now().UTC()
	requestTime := time.Unix(timestamp, 0).UTC()
	if now.Sub(requestTime) > 5*time.Minute || requestTime.Sub(now) > 5*time.Minute {
		return errors.New("stale slack request")
	}
	base := "v0:" + timestampRaw + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(g.cfg.SlackSigningSecret))
	_, _ = mac.Write([]byte(base))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return errors.New("invalid slack signature")
	}
	return nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.SlackAPIBaseURL+"/oauth.v2.access", strings.NewReader(form.Encode()))
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

func (g *slackGateway) upsertChannelConversation(ctx context.Context, session channelSession, event slackEventInner, teamID string) (string, error) {
	body := map[string]any{
		"namespace":              session.Namespace,
		"instanceId":             session.InstanceID,
		"ownerId":                session.OwnerAuthID,
		"provider":               slackProvider,
		"externalScopeType":      slackWorkspaceScope,
		"externalTenantId":       strings.TrimSpace(teamID),
		"externalChannelId":      strings.TrimSpace(event.Channel),
		"externalConversationId": slackExternalConversationID(event),
		"title":                  fmt.Sprintf("Slack %s", strings.TrimSpace(event.Channel)),
		"cwd":                    defaultConversationCWD,
	}
	var payload spritzConversationUpsertResponse
	if err := g.postSpritzJSON(ctx, http.MethodPost, "/api/channel-conversations/upsert", g.cfg.SpritzServiceToken, body, &payload, nil); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Data.Conversation.Metadata.Name), nil
}

func (g *slackGateway) bootstrapConversation(ctx context.Context, ownerToken, namespace, conversationID string) (string, string, error) {
	var payload spritzBootstrapResponse
	if err := g.postSpritzJSON(ctx, http.MethodPost, "/api/acp/conversations/"+url.PathEscape(conversationID)+"/bootstrap", ownerToken, nil, &payload, map[string]string{"namespace": namespace}); err != nil {
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

func (g *slackGateway) promptConversation(ctx context.Context, ownerToken, namespace, conversationID, sessionID, cwd, prompt string) (string, bool, error) {
	wsURL, err := g.spritzWebSocketURL("/api/acp/conversations/"+url.PathEscape(conversationID)+"/connect", map[string]string{"namespace": namespace})
	if err != nil {
		return "", false, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: g.cfg.HTTPTimeout}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+ownerToken)
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()

	client := &acpPromptClient{conn: conn}
	if _, _, err := client.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo": map[string]any{
			"name":    "slack-gateway",
			"title":   "Slack Gateway",
			"version": "1.0.0",
		},
	}, nil); err != nil {
		return "", false, err
	}
	if _, _, err := client.call(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        cwd,
		"mcpServers": []any{},
	}, nil); err != nil {
		return "", false, err
	}
	var reply strings.Builder
	if _, promptSent, err := client.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": prompt,
		}},
	}, func(message *acpRPCMessage) {
		if strings.TrimSpace(message.Method) != "session/update" || len(message.Params) == 0 {
			return
		}
		var payload struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return
		}
		if strings.TrimSpace(stringValue(payload.Update["sessionUpdate"])) != "agent_message_chunk" {
			return
		}
		reply.WriteString(extractACPText(payload.Update["content"]))
	}); err != nil {
		return strings.TrimSpace(reply.String()), promptSent, err
	}
	text := strings.TrimSpace(reply.String())
	if text == "" {
		return "", true, fmt.Errorf("agent returned an empty reply")
	}
	return text, true, nil
}

type acpPromptClient struct {
	conn   *websocket.Conn
	nextID int64
}

func (c *acpPromptClient) writeJSON(ctx context.Context, payload any) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	}
	return c.conn.WriteJSON(payload)
}

func (c *acpPromptClient) respondError(ctx context.Context, id any, code int, message string) error {
	return c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *acpPromptClient) call(ctx context.Context, method string, params any, onNotification func(*acpRPCMessage)) (json.RawMessage, bool, error) {
	c.nextID++
	requestID := fmt.Sprintf("rpc-%d", c.nextID)
	delivered := false
	if err := c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, false, err
	}
	for {
		if deadline, ok := ctx.Deadline(); ok {
			_ = c.conn.SetReadDeadline(deadline)
		}
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return nil, delivered, err
		}
		var message acpRPCMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return nil, delivered, err
		}
		if message.Method == "session/request_permission" && message.ID != nil {
			delivered = true
			if err := c.respondError(
				ctx,
				message.ID,
				-32000,
				"Permission denied: interactive approvals are unavailable in the Slack gateway.",
			); err != nil {
				return nil, delivered, err
			}
			continue
		}
		if message.Method != "" && message.ID == nil {
			delivered = true
			if onNotification != nil {
				onNotification(&message)
			}
			continue
		}
		if fmt.Sprint(message.ID) != requestID {
			continue
		}
		delivered = true
		if message.Error != nil {
			return nil, delivered, fmt.Errorf("%s", strings.TrimSpace(message.Error.Message))
		}
		return message.Result, delivered, nil
	}
}

func extractACPText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractACPText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text := stringValue(typed["text"]); text != "" {
			return text
		}
		if content, ok := typed["content"]; ok {
			return extractACPText(content)
		}
	}
	return ""
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func (g *slackGateway) postSlackMessage(ctx context.Context, token, channel, text, threadTS string) error {
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
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("slack chat.postMessage failed: %s", resp.Status)
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("slack chat.postMessage failed: %s", strings.TrimSpace(result.Error))
	}
	return nil
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
