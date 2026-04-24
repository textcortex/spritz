package main

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

type workspaceManagementNotice struct {
	Title   string
	Message string
}

type workspaceManagementRow struct {
	TeamID              string
	State               string
	CurrentTargetName   string
	CurrentTargetOwner  string
	TargetStatus        string
	ChangeTargetHref    string
	ChannelSettingsHref string
	TestHref            string
	ReconnectHref       string
	ShowReconnect       bool
	ShowDisconnect      bool
	ShowTest            bool
}

type workspaceManagementPageData struct {
	Notice           *workspaceManagementNotice
	Rows             []workspaceManagementRow
	DisconnectAction string
}

var workspaceManagementTemplate = template.Must(template.New("workspace-management").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Slack workspaces</title>
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
      .hero, .notice {
        padding: 24px;
      }
      .hero h1, .notice h2 {
        margin: 0 0 10px;
        font-size: 30px;
        line-height: 1.1;
      }
      .hero p, .notice p {
        margin: 0;
        color: var(--muted);
        line-height: 1.6;
      }
      .card {
        padding: 20px;
        display: grid;
        gap: 12px;
      }
      .row {
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 16px;
      }
      .meta {
        color: var(--muted);
        font-size: 14px;
      }
      .state {
        display: inline-flex;
        align-items: center;
        padding: 6px 10px;
        border-radius: 999px;
        background: #ede6da;
        font-size: 13px;
        color: var(--muted);
      }
      .actions {
        display: flex;
        flex-wrap: wrap;
        gap: 10px;
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
      .empty {
        color: var(--muted);
      }
      form { margin: 0; }
    </style>
  </head>
  <body>
    <main>
      <section class="hero">
        <h1>Slack workspaces</h1>
        <p>Manage which target each connected Slack workspace uses.</p>
      </section>
      {{ if .Notice }}
      <section class="notice">
        <h2>{{ .Notice.Title }}</h2>
        <p>{{ .Notice.Message }}</p>
      </section>
      {{ end }}
      {{ if .Rows }}
      {{ range .Rows }}
      <section class="card">
        <div class="row">
          <div>
            <div><strong>{{ .TeamID }}</strong></div>
            <div class="meta">{{ .CurrentTargetName }}</div>
            {{ if .CurrentTargetOwner }}
            <div class="meta">{{ .CurrentTargetOwner }}</div>
            {{ end }}
            {{ if .TargetStatus }}
            <div class="meta">{{ .TargetStatus }}</div>
            {{ end }}
          </div>
          <div class="state">{{ .State }}</div>
        </div>
        <div class="actions">
          <a class="primary" href="{{ .ChangeTargetHref }}">Change target</a>
          {{ if .ChannelSettingsHref }}
          <a class="secondary" href="{{ .ChannelSettingsHref }}">Channel settings</a>
          {{ end }}
          {{ if .ShowTest }}
          <a class="secondary" href="{{ .TestHref }}">Send test</a>
          {{ end }}
          {{ if .ShowReconnect }}
          <a class="secondary" href="{{ .ReconnectHref }}">Reconnect</a>
          {{ end }}
          {{ if .ShowDisconnect }}
          <form method="post" action="{{ $.DisconnectAction }}">
            <input type="hidden" name="teamId" value="{{ .TeamID }}">
            <button class="secondary" type="submit">Disconnect</button>
          </form>
          {{ end }}
        </div>
      </section>
      {{ end }}
      {{ else }}
      <section class="card empty">
        No manageable Slack workspaces are connected for this account yet.
      </section>
      {{ end }}
    </main>
  </body>
</html>`))

func (g *slackGateway) workspacesPath() string {
	return g.publicPathPrefix() + "/slack/workspaces"
}

func (g *slackGateway) workspaceTargetPath() string {
	return g.publicPathPrefix() + "/slack/workspaces/target"
}

func (g *slackGateway) workspaceDisconnectPath() string {
	return g.publicPathPrefix() + "/slack/workspaces/disconnect"
}

func (g *slackGateway) buildWorkspaceTestHref(teamID string) string {
	target := url.URL{Path: g.workspaceTestPath()}
	query := target.Query()
	query.Set("teamId", strings.TrimSpace(teamID))
	target.RawQuery = query.Encode()
	return target.String()
}

func workspaceNoticeFromRequest(r *http.Request) *workspaceManagementNotice {
	if r == nil {
		return nil
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	switch strings.TrimSpace(r.URL.Query().Get("notice")) {
	case "target-updated":
		return &workspaceManagementNotice{
			Title:   "Workspace target updated",
			Message: "The selected target was updated for " + teamID + ".",
		}
	case "workspace-disconnected":
		return &workspaceManagementNotice{
			Title:   "Workspace disconnected",
			Message: "Routing has been disabled for " + teamID + ".",
		}
	case "target-update-failed":
		return &workspaceManagementNotice{
			Title:   "Workspace target update failed",
			Message: "The selected target could not be updated right now. Try again shortly.",
		}
	case "workspace-disconnect-failed":
		return &workspaceManagementNotice{
			Title:   "Workspace disconnect failed",
			Message: "The workspace could not be disconnected right now. Try again shortly.",
		}
	default:
		return nil
	}
}

func workspaceTargetStatus(installation backendManagedInstallation) string {
	if installation.ProblemCode == "install.target.invalid" {
		return "Repair needed: the saved target is no longer valid."
	}
	if installation.CurrentTarget == nil {
		return "Legacy install: no explicit saved target is recorded yet."
	}
	return ""
}

func hasAllowedAction(installation backendManagedInstallation, action string) bool {
	for _, candidate := range installation.AllowedActions {
		if strings.EqualFold(strings.TrimSpace(candidate), action) {
			return true
		}
	}
	return false
}

func (g *slackGateway) buildWorkspaceTargetHref(teamID string) string {
	target := url.URL{Path: g.workspaceTargetPath()}
	query := target.Query()
	query.Set("teamId", strings.TrimSpace(teamID))
	target.RawQuery = query.Encode()
	return target.String()
}

func (g *slackGateway) buildWorkspaceNoticeURL(notice, teamID string) string {
	target := url.URL{Path: g.workspacesPath()}
	query := target.Query()
	query.Set("notice", strings.TrimSpace(notice))
	if strings.TrimSpace(teamID) != "" {
		query.Set("teamId", strings.TrimSpace(teamID))
	}
	target.RawQuery = query.Encode()
	return target.String()
}

func (g *slackGateway) renderWorkspaceManagementPage(w http.ResponseWriter, r *http.Request, installations []backendManagedInstallation) {
	rows := make([]workspaceManagementRow, 0, len(installations))
	for _, installation := range installations {
		currentTargetName := "No saved target"
		currentTargetOwner := ""
		if installation.CurrentTarget != nil {
			currentTargetName = strings.TrimSpace(installation.CurrentTarget.Profile.Name)
			currentTargetOwner = strings.TrimSpace(installation.CurrentTarget.OwnerLabel)
		}
		settingsHref := ""
		connection := primaryManagedConnection(installation)
		if strings.TrimSpace(installation.ID) != "" && strings.TrimSpace(connection.ID) != "" {
			settingsHref = g.channelSettingsConnectionPath(installation.ID, connection.ID)
		}
		rows = append(rows, workspaceManagementRow{
			TeamID:              strings.TrimSpace(installation.Route.ExternalTenantID),
			State:               strings.TrimSpace(installation.State),
			CurrentTargetName:   currentTargetName,
			CurrentTargetOwner:  currentTargetOwner,
			TargetStatus:        workspaceTargetStatus(installation),
			ChangeTargetHref:    g.buildWorkspaceTargetHref(installation.Route.ExternalTenantID),
			ChannelSettingsHref: settingsHref,
			TestHref:            g.buildWorkspaceTestHref(installation.Route.ExternalTenantID),
			ReconnectHref:       g.installRedirectPath(),
			ShowReconnect:       hasAllowedAction(installation, "reconnect"),
			ShowDisconnect:      hasAllowedAction(installation, "disconnect"),
			ShowTest:            !strings.EqualFold(strings.TrimSpace(installation.State), "disconnected"),
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = workspaceManagementTemplate.Execute(w, workspaceManagementPageData{
		Notice:           workspaceNoticeFromRequest(r),
		Rows:             rows,
		DisconnectAction: g.workspaceDisconnectPath(),
	})
}
