package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const syntheticSlackActorUserID = "U_SYNTHETIC"

func inferSyntheticSlackEvent(channelID, text, threadTS string) (slackEnvelope, error) {
	normalizedChannelID := strings.TrimSpace(channelID)
	if normalizedChannelID == "" {
		return slackEnvelope{}, fmt.Errorf("channel id is required")
	}
	normalizedText := strings.TrimSpace(text)
	if normalizedText == "" {
		return slackEnvelope{}, fmt.Errorf("prompt is required")
	}
	eventType := "app_mention"
	channelType := "channel"
	if strings.HasPrefix(normalizedChannelID, "D") {
		eventType = "message"
		channelType = "im"
	} else if strings.HasPrefix(normalizedChannelID, "G") {
		eventType = "message"
		channelType = "mpim"
	}
	now := time.Now().UTC()
	messageTS := fmt.Sprintf("%d.%06d", now.Unix(), now.Nanosecond()/1000)
	return slackEnvelope{
		Type:    "event_callback",
		EventID: "synthetic-" + strings.ReplaceAll(messageTS, ".", ""),
		Event: slackEventInner{
			Type:        eventType,
			User:        syntheticSlackActorUserID,
			Text:        normalizedText,
			Channel:     normalizedChannelID,
			ChannelType: channelType,
			TS:          messageTS,
			ThreadTS:    strings.TrimSpace(threadTS),
		},
	}, nil
}

func (g *slackGateway) lookupManagedWorkspace(
	r *http.Request,
	callerAuthID string,
	teamID string,
) (*backendManagedInstallation, error) {
	installations, err := g.listManagedInstallations(r.Context(), callerAuthID)
	if err != nil {
		return nil, err
	}
	normalizedTeamID := strings.TrimSpace(teamID)
	for _, installation := range installations {
		if strings.TrimSpace(installation.Route.ExternalTenantID) != normalizedTeamID {
			continue
		}
		installationCopy := installation
		return &installationCopy, nil
	}
	return nil, nil
}

func (g *slackGateway) handleWorkspaceTest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.handleWorkspaceTestForm(w, r)
	case http.MethodPost:
		http.Error(w, "legacy workspace test form removed; use /api/slack/workspaces/test", http.StatusGone)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *slackGateway) handleWorkspaceTestForm(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	g.redirectToReactRoute(w, r, reactSlackWorkspaceTestPath(r.URL.Query()))
	_ = principal
}
