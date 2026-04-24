package main

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type channelSettingsNotice struct {
	Title   string
	Message string
}

type channelSettingsListRow struct {
	InstallationID string
	ConnectionID   string
	TeamID         string
	State          string
	TargetName     string
	RouteSummary   string
	SettingsHref   string
}

type channelSettingsPageData struct {
	Notice *channelSettingsNotice
	Rows   []channelSettingsListRow
}

type channelRouteSettingsRow struct {
	ExternalChannelID string
	RequireMention    bool
	ModeLabel         string
}

type channelConnectionSettingsPageData struct {
	Notice         *channelSettingsNotice
	InstallationID string
	ConnectionID   string
	TeamID         string
	State          string
	TargetName     string
	Rows           []channelRouteSettingsRow
	UpdateAction   string
	BackHref       string
}

var channelSettingsListTemplate = template.Must(template.New("channel-settings-list").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Channel settings</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f7f8fa;
        --surface: #ffffff;
        --border: #d7dce3;
        --text: #17202a;
        --muted: #627083;
        --primary: #1f6feb;
        --primary-text: #ffffff;
        --secondary: #eef2f7;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: var(--bg);
        color: var(--text);
        padding: 24px;
      }
      main {
        width: min(960px, 100%);
        margin: 0 auto;
        display: grid;
        gap: 16px;
      }
      header, .panel, .notice {
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: 8px;
      }
      header, .notice, .panel { padding: 20px; }
      h1, h2, p { margin: 0; }
      h1 { font-size: 26px; line-height: 1.15; }
      p, .meta { color: var(--muted); }
      .panel {
        display: grid;
        gap: 14px;
      }
      .row {
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 16px;
      }
      .state {
        display: inline-flex;
        align-items: center;
        border-radius: 6px;
        padding: 5px 8px;
        background: var(--secondary);
        color: var(--muted);
        font-size: 13px;
      }
      a.button {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        border-radius: 6px;
        padding: 9px 12px;
        background: var(--primary);
        color: var(--primary-text);
        text-decoration: none;
        font-weight: 650;
      }
      .empty { color: var(--muted); }
    </style>
  </head>
  <body>
    <main>
      <header>
        <h1>Channel settings</h1>
      </header>
      {{ if .Notice }}
      <section class="notice">
        <h2>{{ .Notice.Title }}</h2>
        <p>{{ .Notice.Message }}</p>
      </section>
      {{ end }}
      {{ if .Rows }}
      {{ range .Rows }}
      <section class="panel">
        <div class="row">
          <div>
            <strong>{{ .TeamID }}</strong>
            <div class="meta">{{ .TargetName }}</div>
            <div class="meta">{{ .RouteSummary }}</div>
          </div>
          <span class="state">{{ .State }}</span>
        </div>
        <div>
          <a class="button" href="{{ .SettingsHref }}">Open settings</a>
        </div>
      </section>
      {{ end }}
      {{ else }}
      <section class="panel empty">No manageable channel installations are connected for this account.</section>
      {{ end }}
    </main>
  </body>
</html>`))

var channelConnectionSettingsTemplate = template.Must(template.New("channel-connection-settings").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Channel settings</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f7f8fa;
        --surface: #ffffff;
        --border: #d7dce3;
        --text: #17202a;
        --muted: #627083;
        --primary: #1f6feb;
        --primary-text: #ffffff;
        --secondary: #eef2f7;
        --danger: #b42318;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: var(--bg);
        color: var(--text);
        padding: 24px;
      }
      main {
        width: min(980px, 100%);
        margin: 0 auto;
        display: grid;
        gap: 16px;
      }
      header, .panel, .notice {
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: 8px;
        padding: 20px;
      }
      h1, h2, p { margin: 0; }
      h1 { font-size: 26px; line-height: 1.15; }
      p, .meta, label { color: var(--muted); }
      .topline, .route-row, .form-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 16px;
      }
      .route-list {
        display: grid;
        gap: 10px;
      }
      .route-row {
        border: 1px solid var(--border);
        border-radius: 8px;
        padding: 12px;
      }
      .badge {
        display: inline-flex;
        align-items: center;
        border-radius: 6px;
        padding: 5px 8px;
        background: var(--secondary);
        color: var(--muted);
        font-size: 13px;
      }
      .actions {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
      }
      button, a.button {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-height: 36px;
        border-radius: 6px;
        padding: 8px 12px;
        border: 0;
        text-decoration: none;
        cursor: pointer;
        font-weight: 650;
      }
      .primary { background: var(--primary); color: var(--primary-text); }
      .secondary { background: var(--secondary); color: var(--text); }
      .danger { color: var(--danger); }
      input[type="text"] {
        width: min(320px, 100%);
        min-height: 36px;
        border: 1px solid var(--border);
        border-radius: 6px;
        padding: 8px 10px;
        font: inherit;
      }
      input[type="checkbox"] {
        width: 18px;
        height: 18px;
      }
      form { margin: 0; }
      .form-row { flex-wrap: wrap; justify-content: flex-start; }
      .empty { color: var(--muted); }
    </style>
  </head>
  <body>
    <main>
      <header>
        <div class="topline">
          <div>
            <h1>{{ .TeamID }}</h1>
            <p>{{ .TargetName }}</p>
          </div>
          <a class="button secondary" href="{{ .BackHref }}">Back</a>
        </div>
      </header>
      {{ if .Notice }}
      <section class="notice">
        <h2>{{ .Notice.Title }}</h2>
        <p>{{ .Notice.Message }}</p>
      </section>
      {{ end }}
      <section class="panel">
        <form method="post" action="{{ .UpdateAction }}">
          <input type="hidden" name="action" value="upsert">
          <div class="form-row">
            <input type="text" name="externalChannelId" placeholder="C12345678" required>
            <label><input type="checkbox" name="requireMention" checked> Require mention</label>
            <button class="primary" type="submit">Save channel</button>
          </div>
        </form>
      </section>
      <section class="panel">
        {{ if .Rows }}
        <div class="route-list">
          {{ range .Rows }}
          <div class="route-row">
            <div>
              <strong>{{ .ExternalChannelID }}</strong>
              <div class="meta">{{ .ModeLabel }}</div>
            </div>
            <div class="actions">
              <form method="post" action="{{ $.UpdateAction }}">
                <input type="hidden" name="action" value="toggle">
                <input type="hidden" name="externalChannelId" value="{{ .ExternalChannelID }}">
                {{ if .RequireMention }}
                <input type="hidden" name="requireMention" value="false">
                <button class="secondary" type="submit">Allow without mention</button>
                {{ else }}
                <input type="hidden" name="requireMention" value="true">
                <button class="secondary" type="submit">Require mention</button>
                {{ end }}
              </form>
              <form method="post" action="{{ $.UpdateAction }}">
                <input type="hidden" name="action" value="delete">
                <input type="hidden" name="externalChannelId" value="{{ .ExternalChannelID }}">
                <button class="secondary danger" type="submit">Remove</button>
              </form>
            </div>
          </div>
          {{ end }}
        </div>
        {{ else }}
        <div class="empty">No channel overrides are configured.</div>
        {{ end }}
      </section>
    </main>
  </body>
</html>`))

func (g *slackGateway) channelSettingsPath() string {
	return g.publicPathPrefix() + "/settings/channels"
}

func (g *slackGateway) channelSettingsInstallationPath(installationID string) string {
	return g.publicPathPrefix() + "/settings/channels/installations/" + url.PathEscape(strings.TrimSpace(installationID))
}

func (g *slackGateway) channelSettingsConnectionPath(installationID, connectionID string) string {
	return g.channelSettingsInstallationPath(installationID) + "/connections/" + url.PathEscape(strings.TrimSpace(connectionID))
}

func (g *slackGateway) relativeGatewayPath(requestPath string) string {
	requestPath = strings.TrimSpace(requestPath)
	prefix := g.publicPathPrefix()
	if prefix != "" {
		if requestPath == prefix {
			return "/"
		}
		if strings.HasPrefix(requestPath, prefix+"/") {
			return strings.TrimPrefix(requestPath, prefix)
		}
	}
	return requestPath
}

func channelSettingsNoticeFromRequest(r *http.Request) *channelSettingsNotice {
	if r == nil {
		return nil
	}
	switch strings.TrimSpace(r.URL.Query().Get("notice")) {
	case "routes-updated":
		return &channelSettingsNotice{Title: "Channel settings updated", Message: "The channel routing settings were saved."}
	case "routes-update-failed":
		return &channelSettingsNotice{Title: "Channel settings update failed", Message: "The channel routing settings could not be saved right now."}
	default:
		return nil
	}
}

func primaryManagedConnection(installation backendManagedInstallation) backendManagedConnection {
	for _, connection := range installation.Connections {
		if connection.IsDefault {
			return connection
		}
	}
	if len(installation.Connections) > 0 {
		return installation.Connections[0]
	}
	connectionID := ""
	if strings.HasPrefix(strings.TrimSpace(installation.ID), "ci_") {
		connectionID = "cc_" + strings.TrimPrefix(strings.TrimSpace(installation.ID), "ci_")
	}
	return backendManagedConnection{
		ID:        connectionID,
		IsDefault: true,
		State:     installation.State,
		Routes:    routesFromInstallationConfig(installation.InstallationConfig),
	}
}

func managedConnectionByID(installation backendManagedInstallation, connectionID string) (backendManagedConnection, bool) {
	connectionID = strings.TrimSpace(connectionID)
	for _, connection := range installation.Connections {
		if strings.TrimSpace(connection.ID) == connectionID {
			return connection, true
		}
	}
	if len(installation.Connections) == 0 {
		connection := primaryManagedConnection(installation)
		if strings.TrimSpace(connection.ID) == connectionID {
			return connection, true
		}
	}
	return backendManagedConnection{}, false
}

func routesFromInstallationConfig(config installationConfig) []backendManagedChannelRoute {
	routes := make([]backendManagedChannelRoute, 0, len(config.ChannelPolicies))
	for _, policy := range config.ChannelPolicies {
		requireMention := true
		if policy.RequireMention != nil {
			requireMention = *policy.RequireMention
		}
		routes = append(routes, backendManagedChannelRoute{
			ExternalChannelID: strings.TrimSpace(policy.ExternalChannelID),
			RequireMention:    requireMention,
			Enabled:           true,
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].ExternalChannelID < routes[j].ExternalChannelID
	})
	return routes
}

func channelPoliciesFromConnection(connection backendManagedConnection) []installationChannelPolicy {
	policies := make([]installationChannelPolicy, 0, len(connection.Routes))
	for _, route := range connection.Routes {
		if !route.Enabled {
			continue
		}
		channelID := strings.TrimSpace(route.ExternalChannelID)
		if channelID == "" {
			continue
		}
		requireMention := route.RequireMention
		policies = append(policies, installationChannelPolicy{
			ExternalChannelID: channelID,
			RequireMention:    &requireMention,
		})
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].ExternalChannelID < policies[j].ExternalChannelID
	})
	return policies
}

func channelRouteSettingsRows(connection backendManagedConnection) []channelRouteSettingsRow {
	policies := channelPoliciesFromConnection(connection)
	rows := make([]channelRouteSettingsRow, 0, len(policies))
	for _, policy := range policies {
		requireMention := true
		if policy.RequireMention != nil {
			requireMention = *policy.RequireMention
		}
		modeLabel := "Mentions required"
		if !requireMention {
			modeLabel = "Relays without mention"
		}
		rows = append(rows, channelRouteSettingsRow{
			ExternalChannelID: policy.ExternalChannelID,
			RequireMention:    requireMention,
			ModeLabel:         modeLabel,
		})
	}
	return rows
}

func routeSummary(connection backendManagedConnection) string {
	count := len(channelPoliciesFromConnection(connection))
	switch count {
	case 0:
		return "No channel overrides"
	case 1:
		return "1 channel override"
	default:
		return fmt.Sprintf("%d channel overrides", count)
	}
}

func channelSettingsRows(installations []backendManagedInstallation) []channelSettingsListRow {
	rows := make([]channelSettingsListRow, 0, len(installations))
	for _, installation := range installations {
		connection := primaryManagedConnection(installation)
		targetName := "No target selected"
		if installation.CurrentTarget != nil {
			targetName = strings.TrimSpace(installation.CurrentTarget.Profile.Name)
		}
		rows = append(rows, channelSettingsListRow{
			InstallationID: installation.ID,
			ConnectionID:   connection.ID,
			TeamID:         strings.TrimSpace(installation.Route.ExternalTenantID),
			State:          strings.TrimSpace(installation.State),
			TargetName:     targetName,
			RouteSummary:   routeSummary(connection),
			SettingsHref:   "",
		})
	}
	return rows
}

func (g *slackGateway) renderChannelSettingsList(w http.ResponseWriter, r *http.Request, installations []backendManagedInstallation) {
	rows := channelSettingsRows(installations)
	for index := range rows {
		rows[index].SettingsHref = g.channelSettingsConnectionPath(rows[index].InstallationID, rows[index].ConnectionID)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = channelSettingsListTemplate.Execute(w, channelSettingsPageData{
		Notice: channelSettingsNoticeFromRequest(r),
		Rows:   rows,
	})
}

func (g *slackGateway) renderChannelConnectionSettings(w http.ResponseWriter, r *http.Request, installation backendManagedInstallation, connection backendManagedConnection) {
	targetName := "No target selected"
	if installation.CurrentTarget != nil {
		targetName = strings.TrimSpace(installation.CurrentTarget.Profile.Name)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = channelConnectionSettingsTemplate.Execute(w, channelConnectionSettingsPageData{
		Notice:         channelSettingsNoticeFromRequest(r),
		InstallationID: installation.ID,
		ConnectionID:   connection.ID,
		TeamID:         strings.TrimSpace(installation.Route.ExternalTenantID),
		State:          strings.TrimSpace(installation.State),
		TargetName:     targetName,
		Rows:           channelRouteSettingsRows(connection),
		UpdateAction:   g.channelSettingsConnectionPath(installation.ID, connection.ID),
		BackHref:       g.channelSettingsPath(),
	})
}
