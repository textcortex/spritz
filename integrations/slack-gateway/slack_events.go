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
	"strconv"
	"strings"
	"time"
)

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

	session, err := g.exchangeChannelSession(ctx, envelope.TeamID)
	if err != nil {
		return err
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
		return nil
	}

	externalConversationID := slackExternalConversationID(event)
	conversationID, err := g.upsertChannelConversation(ctx, session, event, envelope.TeamID, "", externalConversationID)
	if err != nil {
		return err
	}
	sessionID, cwd, err := g.bootstrapConversation(ctx, g.cfg.SpritzServiceToken, session.Namespace, conversationID)
	if err != nil {
		return err
	}
	reply, promptSent, err := g.promptConversation(ctx, g.cfg.SpritzServiceToken, session.Namespace, conversationID, sessionID, cwd, promptText)
	if err != nil {
		if !promptSent {
			return err
		}
		reply = "I hit an internal error while processing that request."
		g.logger.Error("acp prompt failed", "error", err, "conversation_id", conversationID)
	}
	replyThreadTS := slackReplyThreadTS(event)
	replyCtx, cancelReply := context.WithTimeout(context.WithoutCancel(ctx), g.cfg.HTTPTimeout)
	defer cancelReply()
	replyMessageTS, err := g.postSlackMessage(replyCtx, session.ProviderAuth.BotAccessToken, event.Channel, reply, replyThreadTS)
	if err != nil {
		// Once the ACP prompt has already been delivered, suppress duplicate
		// Slack retries from re-running the same agent side effects.
		success = promptSent
		return err
	}
	if replyThreadTS == "" && !isSlackDirectMessageEvent(event) && strings.TrimSpace(replyMessageTS) != "" {
		aliasCtx, cancelAlias := context.WithTimeout(context.WithoutCancel(ctx), g.cfg.HTTPTimeout)
		if _, err := g.upsertChannelConversation(
			aliasCtx,
			session,
			event,
			envelope.TeamID,
			conversationID,
			replyMessageTS,
		); err != nil {
			cancelAlias()
			g.logger.Error(
				"slack reply alias persistence failed",
				"error", err,
				"conversation_id", conversationID,
				"reply_message_ts", replyMessageTS,
			)
		} else {
			cancelAlias()
		}
	}
	success = true
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
		}
	}
	return normalized
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
