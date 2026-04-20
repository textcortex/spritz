package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

const syntheticSlackActorUserID = "U_SYNTHETIC"

type workspaceTestPageData struct {
	TeamID          string
	ChannelID       string
	ThreadTS        string
	Prompt          string
	Mode            string
	CurrentTarget   string
	Outcome         string
	Reply           string
	ConversationID  string
	PostedMessageTS string
	ErrorMessage    string
}

var workspaceTestTemplate = template.Must(template.New("workspace-test").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Slack workspace test</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f4f1ea;
        --surface: #fffdf9;
        --border: #ddd5c8;
        --text: #1f1b16;
        --muted: #675d50;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: linear-gradient(180deg, var(--bg), #ebe4d7);
        color: var(--text);
        padding: 24px;
      }
      main {
        width: min(920px, 100%);
        margin: 0 auto;
        display: grid;
        gap: 16px;
      }
      .hero, .card, .notice {
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: 24px;
        box-shadow: 0 24px 80px rgba(31, 27, 22, 0.08);
      }
      .hero, .card, .notice {
        padding: 24px;
      }
      .hero h1, .notice h2 {
        margin: 0 0 10px;
        font-size: 30px;
        line-height: 1.1;
      }
      p, label, .meta {
        line-height: 1.6;
      }
      .meta {
        color: var(--muted);
        margin: 0;
      }
      form {
        display: grid;
        gap: 14px;
      }
      label {
        display: grid;
        gap: 6px;
        font-weight: 600;
      }
      input, textarea, select, button {
        font: inherit;
      }
      input, textarea, select {
        width: 100%;
        border: 1px solid var(--border);
        border-radius: 14px;
        padding: 10px 12px;
        background: #fff;
        color: var(--text);
      }
      textarea {
        min-height: 120px;
        resize: vertical;
      }
      .inline {
        display: flex;
        gap: 12px;
        flex-wrap: wrap;
      }
      .inline label {
        display: inline-flex;
        align-items: center;
        gap: 8px;
        font-weight: 500;
      }
      .actions {
        display: flex;
        gap: 12px;
        flex-wrap: wrap;
      }
      .actions a, .actions button {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        border-radius: 999px;
        padding: 10px 14px;
        border: 0;
        text-decoration: none;
        cursor: pointer;
        font-weight: 600;
      }
      .primary {
        background: var(--text);
        color: #fff;
      }
      .secondary {
        background: #ede6da;
        color: var(--text);
      }
      .value {
        font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
        white-space: pre-wrap;
        word-break: break-word;
      }
    </style>
  </head>
  <body>
    <main>
      <section class="hero">
        <h1>Send a synthetic Slack test message</h1>
        <p class="meta">Workspace {{ .TeamID }}{{ if .CurrentTarget }} · target {{ .CurrentTarget }}{{ end }}</p>
      </section>

      {{ if .Outcome }}
      <section class="notice">
        <h2>Test completed</h2>
        <p class="meta">Outcome: {{ .Outcome }}</p>
        {{ if .ConversationID }}<p class="meta">Conversation: <span class="value">{{ .ConversationID }}</span></p>{{ end }}
        {{ if .PostedMessageTS }}<p class="meta">Posted Slack TS: <span class="value">{{ .PostedMessageTS }}</span></p>{{ end }}
        {{ if .Reply }}<p class="meta">Reply:</p><p class="value">{{ .Reply }}</p>{{ end }}
      </section>
      {{ end }}

      {{ if .ErrorMessage }}
      <section class="notice">
        <h2>Test failed</h2>
        <p>{{ .ErrorMessage }}</p>
      </section>
      {{ end }}

      <section class="card">
        <form method="post" action="">
          <input type="hidden" name="teamId" value="{{ .TeamID }}">
          <label>
            Channel ID
            <input type="text" name="channelId" value="{{ .ChannelID }}" placeholder="C12345678" required>
          </label>
          <label>
            Thread TS
            <input type="text" name="threadTs" value="{{ .ThreadTS }}" placeholder="Optional existing thread timestamp">
          </label>
          <label>
            Prompt
            <textarea name="prompt" required>{{ .Prompt }}</textarea>
          </label>
          <div class="inline">
            <label><input type="radio" name="mode" value="real" {{ if ne .Mode "dry-run" }}checked{{ end }}> Real reply</label>
            <label><input type="radio" name="mode" value="dry-run" {{ if eq .Mode "dry-run" }}checked{{ end }}> Dry run</label>
          </div>
          <div class="actions">
            <button class="primary" type="submit">Run test</button>
            <a class="secondary" href="{{ .BackHref }}">Back to workspaces</a>
          </div>
        </form>
      </section>
    </main>
  </body>
</html>`))

type workspaceSyntheticTestPageData struct {
	workspaceTestPageData
	BackHref string
}

func (g *slackGateway) workspaceTestPath() string {
	return g.publicPathPrefix() + "/slack/workspaces/test"
}

func defaultWorkspaceTestPrompt() string {
	return fmt.Sprintf("spritz-slack-smoke-%d", time.Now().UTC().Unix())
}

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

func (g *slackGateway) renderWorkspaceTestPage(
	w http.ResponseWriter,
	data workspaceTestPageData,
) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = workspaceTestTemplate.Execute(w, workspaceSyntheticTestPageData{
		workspaceTestPageData: data,
		BackHref:              g.workspacesPath(),
	})
}

func (g *slackGateway) handleWorkspaceTest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.handleWorkspaceTestForm(w, r)
	case http.MethodPost:
		g.handleWorkspaceTestSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *slackGateway) handleWorkspaceTestForm(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	if teamID == "" {
		http.Error(w, "teamId is required", http.StatusBadRequest)
		return
	}
	installation, err := g.lookupManagedWorkspace(r, principal.ID, teamID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace test lookup failed",
			"err", err,
			"caller_auth_id", principal.ID,
			"team_id", teamID,
		)
		http.Error(w, "workspace lookup unavailable", http.StatusBadGateway)
		return
	}
	if installation == nil {
		http.Error(w, "workspace not manageable", http.StatusForbidden)
		return
	}
	currentTarget := ""
	if installation.CurrentTarget != nil {
		currentTarget = strings.TrimSpace(installation.CurrentTarget.Profile.Name)
	}
	g.renderWorkspaceTestPage(w, workspaceTestPageData{
		TeamID:        teamID,
		Prompt:        defaultWorkspaceTestPrompt(),
		Mode:          "real",
		CurrentTarget: currentTarget,
	})
}

func (g *slackGateway) handleWorkspaceTestSubmit(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form payload", http.StatusBadRequest)
		return
	}
	teamID := strings.TrimSpace(r.FormValue("teamId"))
	if teamID == "" {
		http.Error(w, "teamId is required", http.StatusBadRequest)
		return
	}
	installation, err := g.lookupManagedWorkspace(r, principal.ID, teamID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace test lookup failed",
			"err", err,
			"caller_auth_id", principal.ID,
			"team_id", teamID,
		)
		http.Error(w, "workspace lookup unavailable", http.StatusBadGateway)
		return
	}
	if installation == nil {
		http.Error(w, "workspace not manageable", http.StatusForbidden)
		return
	}
	channelID := strings.TrimSpace(r.FormValue("channelId"))
	threadTS := strings.TrimSpace(r.FormValue("threadTs"))
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode == "" {
		mode = "real"
	}
	pageData := workspaceTestPageData{
		TeamID:        teamID,
		ChannelID:     channelID,
		ThreadTS:      threadTS,
		Prompt:        prompt,
		Mode:          mode,
		CurrentTarget: "",
	}
	if installation.CurrentTarget != nil {
		pageData.CurrentTarget = strings.TrimSpace(installation.CurrentTarget.Profile.Name)
	}
	envelope, err := inferSyntheticSlackEvent(channelID, prompt, threadTS)
	if err != nil {
		pageData.ErrorMessage = err.Error()
		g.renderWorkspaceTestPage(w, pageData)
		return
	}
	envelope.TeamID = teamID

	delivery, process, err := g.beginMessageEventDelivery(envelope)
	if err != nil {
		pageData.ErrorMessage = err.Error()
		g.renderWorkspaceTestPage(w, pageData)
		return
	}
	if !process {
		pageData.Outcome = messageEventOutcomeIgnored
		g.renderWorkspaceTestPage(w, pageData)
		return
	}
	result, err := g.processMessageEventWithDeliveryOptions(
		r.Context(),
		envelope,
		delivery,
		messageEventProcessOptions{
			Synthetic: true,
			DryRun:    mode == "dry-run",
		},
	)
	if err != nil {
		pageData.ErrorMessage = err.Error()
		g.renderWorkspaceTestPage(w, pageData)
		return
	}
	pageData.Outcome = result.Outcome
	pageData.Reply = result.Reply
	pageData.ConversationID = result.ConversationID
	pageData.PostedMessageTS = result.PostedMessageTS
	g.renderWorkspaceTestPage(w, pageData)
}
