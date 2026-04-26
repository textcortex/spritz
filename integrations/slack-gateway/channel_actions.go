package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

type slackReactionActionRequest struct {
	TeamID    string `json:"teamId"`
	ChannelID string `json:"channelId"`
	MessageTS string `json:"messageTs"`
	Reaction  string `json:"reaction"`
	Remove    bool   `json:"remove,omitempty"`
}

func (g *slackGateway) handleSlackReactionAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	if len(g.cfg.ChannelActionTokens) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "channel_actions_disabled"})
		return
	}
	defer r.Body.Close()
	var payload slackReactionActionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	payload.TeamID = strings.TrimSpace(payload.TeamID)
	payload.ChannelID = strings.TrimSpace(payload.ChannelID)
	payload.MessageTS = strings.TrimSpace(payload.MessageTS)
	payload.Reaction = normalizeSlackReactionName(payload.Reaction)
	if payload.TeamID == "" || payload.ChannelID == "" || payload.MessageTS == "" || payload.Reaction == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_required_field"})
		return
	}
	if !g.authorizeChannelActionRequest(r, payload.TeamID, payload.ChannelID) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.cfg.HTTPTimeout)
	defer cancel()
	session, err := g.exchangeChannelSession(ctx, payload.TeamID, payload.ChannelID, false)
	if err != nil {
		g.logger.Error(
			"slack reaction action session exchange failed",
			"error", err,
			"team_id", payload.TeamID,
			"channel_id", payload.ChannelID,
		)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "session_exchange_failed"})
		return
	}
	if payload.Remove {
		err = g.removeSlackReaction(ctx, session.ProviderAuth.BotAccessToken, payload.ChannelID, payload.MessageTS, payload.Reaction)
		if slackAPIErrorCode(err) == "no_reaction" {
			err = nil
		}
	} else {
		err = g.addSlackReaction(ctx, session.ProviderAuth.BotAccessToken, payload.ChannelID, payload.MessageTS, payload.Reaction)
		if slackAPIErrorCode(err) == "already_reacted" {
			err = nil
		}
	}
	if err != nil {
		g.logger.Error(
			"slack reaction action failed",
			"error", err,
			"team_id", payload.TeamID,
			"channel_id", payload.ChannelID,
			"message_ts", payload.MessageTS,
			"reaction", payload.Reaction,
			"remove", payload.Remove,
		)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "reaction_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (g *slackGateway) authorizeChannelActionRequest(r *http.Request, teamID, channelID string) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.TrimSpace(teamID) == "" || strings.TrimSpace(channelID) == "" {
		return false
	}
	for _, candidate := range g.cfg.ChannelActionTokens {
		expected := strings.TrimSpace(candidate.Token)
		if expected == "" || len(token) != len(expected) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
			continue
		}
		if strings.TrimSpace(candidate.Target.TeamID) == strings.TrimSpace(teamID) &&
			strings.TrimSpace(candidate.Target.ChannelID) == strings.TrimSpace(channelID) {
			return true
		}
	}
	return false
}
