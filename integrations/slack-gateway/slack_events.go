package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	slackRecoveryStatusText  = "Still waking up. I will continue here shortly."
	slackRecoveryFailureText = "I could not recover the channel runtime. Please try again."
)

var slackMentionTokenPattern = regexp.MustCompile(`<@[^>]+>`)

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

type channelSessionRecoveryState struct {
	mu              sync.Mutex
	startedAt       time.Time
	statusAuth      slackInstallation
	statusAuthReady bool
	statusPosting   bool
	statusVisible   bool
}

func newChannelSessionRecoveryState() *channelSessionRecoveryState {
	return &channelSessionRecoveryState{}
}

func (state *channelSessionRecoveryState) rememberProviderAuth(providerAuth slackInstallation) {
	if state == nil || strings.TrimSpace(providerAuth.BotAccessToken) == "" {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.statusAuth = providerAuth
	state.statusAuthReady = true
}

func (state *channelSessionRecoveryState) startRecovery() {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.startedAt.IsZero() {
		state.startedAt = time.Now()
	}
}

func (state *channelSessionRecoveryState) recoveryStarted() bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return !state.startedAt.IsZero()
}

// remainingStatusDelay reports how much longer the gateway should wait before
// posting the provider-authored wake-up status message.
func (state *channelSessionRecoveryState) remainingStatusDelay(delay time.Duration) time.Duration {
	if state == nil {
		return delay
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.startedAt.IsZero() {
		return delay
	}
	remaining := delay - time.Since(state.startedAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (state *channelSessionRecoveryState) hasProviderAuth() bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.statusAuthReady
}

func (state *channelSessionRecoveryState) maybePostStatus(
	ctx context.Context,
	g *slackGateway,
	event slackEventInner,
) error {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	if state.startedAt.IsZero() || !state.statusAuthReady || state.statusVisible || state.statusPosting {
		state.mu.Unlock()
		return nil
	}
	if time.Since(state.startedAt) < g.cfg.StatusMessageDelay {
		state.mu.Unlock()
		return nil
	}
	token := state.statusAuth.BotAccessToken
	state.statusPosting = true
	state.mu.Unlock()

	if err := g.postGatewaySlackMessage(
		ctx,
		token,
		event,
		slackRecoveryStatusText,
	); err != nil {
		state.mu.Lock()
		state.statusPosting = false
		state.mu.Unlock()
		return err
	}

	state.mu.Lock()
	state.statusPosting = false
	state.statusVisible = true
	state.mu.Unlock()
	return nil
}

func (state *channelSessionRecoveryState) maybePostFailure(
	ctx context.Context,
	g *slackGateway,
	event slackEventInner,
	force bool,
) (bool, error) {
	if state == nil {
		return false, nil
	}
	state.mu.Lock()
	if state.startedAt.IsZero() || !state.statusAuthReady {
		state.mu.Unlock()
		return false, nil
	}
	if !force && !state.statusVisible && time.Since(state.startedAt) < g.cfg.StatusMessageDelay {
		state.mu.Unlock()
		return false, nil
	}
	token := state.statusAuth.BotAccessToken
	state.mu.Unlock()
	if err := g.postGatewaySlackMessage(
		ctx,
		token,
		event,
		slackRecoveryFailureText,
	); err != nil {
		return false, err
	}
	return true, nil
}

// startRecoveryStatusTimer posts the provider-authored wake-up status once the
// visible-delay threshold is crossed while recovery is still in progress.
func (g *slackGateway) startRecoveryStatusTimer(
	ctx context.Context,
	event slackEventInner,
	recoveryState *channelSessionRecoveryState,
) func() {
	if recoveryState == nil || !recoveryState.recoveryStarted() {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		remaining := recoveryState.remainingStatusDelay(g.cfg.StatusMessageDelay)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		default:
		}
		if err := recoveryState.maybePostStatus(ctx, g, event); err != nil {
			g.logger.Error(
				"slack recovery status post failed",
				"error", err,
				"channel_id", strings.TrimSpace(event.Channel),
				"message_ts", strings.TrimSpace(event.TS),
			)
		}
	}()
	return func() {
		close(done)
	}
}

type promptRecoveryMode int

const (
	promptRecoveryNone promptRecoveryMode = iota
	promptRecoveryRetrySameRuntime
	promptRecoveryRefreshBinding
)

func classifyPromptRecoveryMode(
	err error,
	promptSent bool,
	sameRuntimeRetryAttempted bool,
) promptRecoveryMode {
	if err == nil || promptSent {
		return promptRecoveryNone
	}
	if isSpritzRuntimeMissingError(err) {
		return promptRecoveryRefreshBinding
	}
	if isACPUnavailableError(err) {
		if sameRuntimeRetryAttempted {
			return promptRecoveryRefreshBinding
		}
		return promptRecoveryRetrySameRuntime
	}
	return promptRecoveryNone
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
		switch strings.TrimSpace(envelope.Event.Type) {
		case "app_uninstalled":
			ctx, cancel := context.WithTimeout(
				context.WithoutCancel(r.Context()),
				g.cfg.ProcessingTimeout,
			)
			defer cancel()
			if err := g.processSlackEnvelope(ctx, envelope); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		case "message", "app_mention":
			delivery, process, err := g.beginMessageEventDelivery(envelope)
			if err != nil {
				statusCode := http.StatusBadGateway
				if errors.Is(err, errSlackEventInFlight) {
					statusCode = http.StatusServiceUnavailable
				}
				http.Error(w, err.Error(), statusCode)
				return
			}
			if !process {
				writeJSON(w, http.StatusOK, map[string]any{"ok": true})
				return
			}
			ctx, cancel := context.WithTimeout(
				context.WithoutCancel(r.Context()),
				g.cfg.ProcessingTimeout,
			)
			g.startSlackEventWorker(ctx, cancel, envelope, delivery)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		default:
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
			return
		}
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}
}

func (g *slackGateway) startSlackEventWorker(
	ctx context.Context,
	cancel context.CancelFunc,
	envelope slackEnvelope,
	delivery *slackMessageDelivery,
) {
	g.workers.Add(1)
	go func() {
		defer g.workers.Done()
		defer cancel()
		if err := g.processMessageEventWithDelivery(ctx, envelope, delivery); err != nil {
			g.logger.Error(
				"slack event processing failed",
				"error", err,
				"event_id", strings.TrimSpace(envelope.EventID),
				"event_type", strings.TrimSpace(envelope.Event.Type),
				"team_id", strings.TrimSpace(envelope.TeamID),
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
	delivery, process, err := g.beginMessageEventDelivery(envelope)
	if err != nil || !process {
		return err
	}
	return g.processMessageEventWithDelivery(ctx, envelope, delivery)
}

func (g *slackGateway) beginMessageEventDelivery(
	envelope slackEnvelope,
) (*slackMessageDelivery, bool, error) {
	event := envelope.Event
	if shouldIgnoreSlackMessageEvent(event) {
		return nil, false, nil
	}
	if !shouldProcessSlackMessageEvent(event) {
		return nil, false, nil
	}
	if strings.TrimSpace(event.BotID) != "" || strings.TrimSpace(event.User) == "" {
		return nil, false, nil
	}
	if strings.TrimSpace(event.Channel) == "" || strings.TrimSpace(event.TS) == "" || strings.TrimSpace(envelope.TeamID) == "" {
		return nil, false, nil
	}

	messageKey := strings.Join([]string{envelope.TeamID, event.Channel, event.TS}, ":")
	messageLease, messageState := g.dedupe.begin("message:" + messageKey)
	if messageState == dedupeStateDuplicateDelivered {
		return nil, false, nil
	}
	if messageState == dedupeStateDuplicateInFlight {
		return nil, false, errSlackEventInFlight
	}
	delivery := &slackMessageDelivery{messageLease: messageLease}

	if eventID := strings.TrimSpace(envelope.EventID); eventID != "" {
		var eventState dedupeState
		eventLease, eventState := g.dedupe.begin("event:" + eventID)
		if eventState == dedupeStateDuplicateDelivered {
			delivery.finish(false)
			return nil, false, nil
		}
		if eventState == dedupeStateDuplicateInFlight {
			delivery.finish(false)
			return nil, false, errSlackEventInFlight
		}
		delivery.eventLease = eventLease
	}
	return delivery, true, nil
}

func (g *slackGateway) processMessageEventWithDelivery(
	ctx context.Context,
	envelope slackEnvelope,
	delivery *slackMessageDelivery,
) error {
	success := false
	defer func() {
		delivery.finish(success)
	}()

	event := envelope.Event
	if normalizeSlackPromptText(event.Type, event.Text, "") == "" {
		success = true
		return nil
	}

	recoveryState := newChannelSessionRecoveryState()
	session, terminalHandled, err := g.awaitChannelSession(
		ctx,
		envelope,
		event,
		recoveryState,
		false,
	)
	if err != nil {
		return err
	}
	if terminalHandled {
		success = true
		return nil
	}
	if session.ProviderAuth.APIAppID != "" && strings.TrimSpace(envelope.APIAppID) != "" && session.ProviderAuth.APIAppID != strings.TrimSpace(envelope.APIAppID) {
		return fmt.Errorf("slack api_app_id mismatch for team %s", envelope.TeamID)
	}

	promptText := buildSlackPromptText(
		envelope.TeamID,
		event,
		session.ProviderAuth.BotUserID,
	)
	if promptText == "" {
		success = true
		return nil
	}

	result, err := g.executeConversationPrompt(ctx, envelope, event, session, promptText)
	sameRuntimeRetryAttempted := false
	stopRecoveryStatusTimer := func() {}
	defer func() {
		stopRecoveryStatusTimer()
	}()
	recoveryStatusTimerStarted := false
	for recoveryMode := classifyPromptRecoveryMode(err, result.promptSent, sameRuntimeRetryAttempted); recoveryMode != promptRecoveryNone; recoveryMode = classifyPromptRecoveryMode(err, result.promptSent, sameRuntimeRetryAttempted) {
		recoveryState.startRecovery()
		recoveryState.rememberProviderAuth(session.ProviderAuth)
		if !recoveryStatusTimerStarted {
			stopRecoveryStatusTimer = g.startRecoveryStatusTimer(ctx, event, recoveryState)
			recoveryStatusTimerStarted = true
		}
		if postErr := recoveryState.maybePostStatus(ctx, g, event); postErr != nil {
			g.logger.Error(
				"slack recovery status post failed",
				"error", postErr,
				"team_id", strings.TrimSpace(envelope.TeamID),
				"channel_id", strings.TrimSpace(event.Channel),
				"message_ts", strings.TrimSpace(event.TS),
			)
		}
		if sleepErr := sleepWithContext(ctx, g.cfg.SessionRetryInterval); sleepErr != nil {
			terminalHandled, postErr := recoveryState.maybePostFailure(ctx, g, event, true)
			if postErr != nil {
				g.logger.Error(
					"slack recovery failure reply failed",
					"error", postErr,
					"team_id", strings.TrimSpace(envelope.TeamID),
					"channel_id", strings.TrimSpace(event.Channel),
					"message_ts", strings.TrimSpace(event.TS),
				)
				return postErr
			}
			if terminalHandled {
				success = true
				return nil
			}
			return sleepErr
		}
		if recoveryMode == promptRecoveryRetrySameRuntime {
			sameRuntimeRetryAttempted = true
		} else {
			sameRuntimeRetryAttempted = false
			recoveredSession, recoveredTerminalHandled, recoveryErr := g.awaitChannelSession(
				ctx,
				envelope,
				event,
				recoveryState,
				true,
			)
			if recoveryErr != nil {
				return recoveryErr
			}
			if recoveredTerminalHandled {
				success = true
				return nil
			}
			session = recoveredSession
			promptText = buildSlackPromptText(
				envelope.TeamID,
				event,
				session.ProviderAuth.BotUserID,
			)
			if promptText == "" {
				success = true
				return nil
			}
		}
		result, err = g.executeConversationPrompt(ctx, envelope, event, session, promptText)
	}
	if err != nil {
		if !result.promptSent {
			return err
		}
		result.reply = "I hit an internal error while processing that request."
		g.logger.Error("acp prompt failed", "error", err, "conversation_id", result.conversationID)
	}
	replyThreadTS := slackReplyThreadTS(event)
	replyCtx, cancelReply := context.WithTimeout(context.WithoutCancel(ctx), g.cfg.HTTPTimeout)
	defer cancelReply()
	replyMessageTS, err := g.postSlackMessage(replyCtx, session.ProviderAuth.BotAccessToken, event.Channel, result.reply, replyThreadTS)
	if err != nil {
		// Once the ACP prompt has already been delivered, suppress duplicate
		// Slack retries from re-running the same agent side effects.
		success = result.promptSent
		return err
	}
	if replyThreadTS == "" && !isSlackDirectMessageEvent(event) && strings.TrimSpace(replyMessageTS) != "" {
		aliasCtx, cancelAlias := context.WithTimeout(context.WithoutCancel(ctx), g.cfg.HTTPTimeout)
		if _, err := g.upsertChannelConversation(
			aliasCtx,
			session,
			event,
			envelope.TeamID,
			result.conversationID,
			replyMessageTS,
		); err != nil {
			cancelAlias()
			g.logger.Error(
				"slack reply alias persistence failed",
				"error", err,
				"conversation_id", result.conversationID,
				"reply_message_ts", replyMessageTS,
			)
		} else {
			cancelAlias()
		}
	}
	success = true
	return nil
}

type conversationPromptResult struct {
	conversationID string
	reply          string
	promptSent     bool
}

func (g *slackGateway) executeConversationPrompt(
	ctx context.Context,
	envelope slackEnvelope,
	event slackEventInner,
	session channelSession,
	promptText string,
) (conversationPromptResult, error) {
	externalConversationID := slackExternalConversationID(event)
	conversationID, err := g.upsertChannelConversation(
		ctx,
		session,
		event,
		envelope.TeamID,
		"",
		externalConversationID,
	)
	if err != nil {
		return conversationPromptResult{}, err
	}
	sessionID, cwd, err := g.bootstrapConversation(
		ctx,
		g.cfg.SpritzServiceToken,
		session.Namespace,
		conversationID,
	)
	if err != nil {
		return conversationPromptResult{conversationID: conversationID}, err
	}
	reply, promptSent, err := g.promptConversation(
		ctx,
		g.cfg.SpritzServiceToken,
		session.Namespace,
		conversationID,
		sessionID,
		cwd,
		promptText,
	)
	return conversationPromptResult{
		conversationID: conversationID,
		reply:          reply,
		promptSent:     promptSent,
	}, err
}

func (g *slackGateway) awaitChannelSession(
	ctx context.Context,
	envelope slackEnvelope,
	event slackEventInner,
	recoveryState *channelSessionRecoveryState,
	forceRefresh bool,
) (channelSession, bool, error) {
	if recoveryState == nil {
		recoveryState = newChannelSessionRecoveryState()
	}
	stopRecoveryStatusTimer := func() {}
	defer func() {
		stopRecoveryStatusTimer()
	}()
	recoveryStatusTimerStarted := false

	for {
		session, err := g.exchangeChannelSession(ctx, envelope.TeamID, forceRefresh)
		if err == nil {
			recoveryState.rememberProviderAuth(session.ProviderAuth)
			return session, false, nil
		}

		providerAuth, recoverable := channelSessionUnavailableProviderAuth(err)
		if !recoverable {
			if recoveryState.hasProviderAuth() {
				g.logger.Error(
					"slack session recovery poll failed",
					"error", err,
					"team_id", strings.TrimSpace(envelope.TeamID),
					"channel_id", strings.TrimSpace(event.Channel),
					"message_ts", strings.TrimSpace(event.TS),
				)
			} else {
				if terminalHandled, postErr := recoveryState.maybePostFailure(ctx, g, event, false); postErr != nil {
					g.logger.Error(
						"slack recovery failure reply failed",
						"error", postErr,
						"team_id", strings.TrimSpace(envelope.TeamID),
						"channel_id", strings.TrimSpace(event.Channel),
						"message_ts", strings.TrimSpace(event.TS),
					)
					return channelSession{}, false, postErr
				} else if terminalHandled {
					return channelSession{}, true, nil
				}
				return channelSession{}, false, err
			}
		} else {
			recoveryState.rememberProviderAuth(providerAuth)
		}
		recoveryState.startRecovery()
		if !recoveryStatusTimerStarted {
			stopRecoveryStatusTimer = g.startRecoveryStatusTimer(ctx, event, recoveryState)
			recoveryStatusTimerStarted = true
		}

		if postErr := recoveryState.maybePostStatus(ctx, g, event); postErr != nil {
			g.logger.Error(
				"slack recovery status post failed",
				"error", postErr,
				"team_id", strings.TrimSpace(envelope.TeamID),
				"channel_id", strings.TrimSpace(event.Channel),
				"message_ts", strings.TrimSpace(event.TS),
			)
		}

		if err := sleepWithContext(ctx, g.cfg.SessionRetryInterval); err != nil {
			if terminalHandled, postErr := recoveryState.maybePostFailure(ctx, g, event, true); postErr != nil {
				g.logger.Error(
					"slack recovery failure reply failed",
					"error", postErr,
					"team_id", strings.TrimSpace(envelope.TeamID),
					"channel_id", strings.TrimSpace(event.Channel),
					"message_ts", strings.TrimSpace(event.TS),
				)
				return channelSession{}, false, postErr
			} else if terminalHandled {
				return channelSession{}, true, nil
			}
			return channelSession{}, false, err
		}
	}
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
	return eventType == "app_mention"
}

func isSlackDirectMessageEvent(event slackEventInner) bool {
	switch strings.TrimSpace(event.ChannelType) {
	case "im", "mpim":
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(event.Channel), "D")
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
		} else {
			normalized = slackMentionTokenPattern.ReplaceAllString(normalized, " ")
		}
	}
	return strings.TrimSpace(normalized)
}

type slackPromptContext struct {
	Source         string `json:"source"`
	Provider       string `json:"provider"`
	WorkspaceID    string `json:"workspace_id"`
	ActorUserID    string `json:"actor_user_id"`
	ChannelID      string `json:"channel_id"`
	ChannelType    string `json:"channel_type,omitempty"`
	MessageTS      string `json:"message_ts"`
	ThreadTS       string `json:"thread_ts,omitempty"`
	ConversationID string `json:"conversation_id"`
	DirectMessage  bool   `json:"direct_message"`
}

func buildSlackPromptText(teamID string, event slackEventInner, botUserID string) string {
	normalized := normalizeSlackPromptText(event.Type, event.Text, botUserID)
	if normalized == "" {
		return ""
	}

	payload, err := json.Marshal(
		slackPromptContext{
			Source:         "spritz-slack-gateway",
			Provider:       slackProvider,
			WorkspaceID:    strings.TrimSpace(teamID),
			ActorUserID:    strings.TrimSpace(event.User),
			ChannelID:      strings.TrimSpace(event.Channel),
			ChannelType:    strings.TrimSpace(event.ChannelType),
			MessageTS:      strings.TrimSpace(event.TS),
			ThreadTS:       strings.TrimSpace(event.ThreadTS),
			ConversationID: slackExternalConversationID(event),
			DirectMessage:  isSlackDirectMessageEvent(event),
		},
	)
	if err != nil {
		return normalized
	}
	return "<spritz-channel-context>" + string(payload) + "</spritz-channel-context>\n\n" + normalized
}

func slackReplyThreadTS(event slackEventInner) string {
	if strings.TrimSpace(event.ThreadTS) != "" {
		return strings.TrimSpace(event.ThreadTS)
	}
	return ""
}

func slackExternalConversationID(event slackEventInner) string {
	if isSlackDirectMessageEvent(event) {
		return strings.TrimSpace(event.Channel)
	}
	if threadTS := strings.TrimSpace(event.ThreadTS); threadTS != "" {
		return threadTS
	}
	return strings.TrimSpace(event.TS)
}

func (g *slackGateway) postGatewaySlackMessage(
	ctx context.Context,
	token string,
	event slackEventInner,
	text string,
) error {
	replyCtx, cancelReply := context.WithTimeout(context.WithoutCancel(ctx), g.cfg.HTTPTimeout)
	defer cancelReply()
	_, err := g.postSlackMessage(
		replyCtx,
		token,
		event.Channel,
		text,
		slackReplyThreadTS(event),
	)
	return err
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
