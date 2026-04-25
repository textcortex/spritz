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
	ConnectionName string
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

type channelInstallationSettingsPageData struct {
	Notice         *channelSettingsNotice
	InstallationID string
	TeamID         string
	State          string
	TargetName     string
	Rows           []channelSettingsListRow
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
            {{ if .ConnectionName }}<div class="meta">{{ .ConnectionName }}</div>{{ end }}
            <div class="meta">{{ .RouteSummary }}</div>
          </div>
          <span class="state">{{ .State }}</span>
        </div>
        <div>
          {{ if .SettingsHref }}
          <a class="button" href="{{ .SettingsHref }}">Open settings</a>
          {{ else }}
          <span class="state">Settings unavailable</span>
          {{ end }}
        </div>
      </section>
      {{ end }}
      {{ else }}
      <section class="panel empty">No manageable channel installations are connected for this account.</section>
      {{ end }}
    </main>
  </body>
</html>`))

var channelInstallationSettingsTemplate = template.Must(template.New("channel-installation-settings").Parse(`<!doctype html>
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
        padding: 20px;
      }
      h1, h2, p { margin: 0; }
      h1 { font-size: 26px; line-height: 1.15; }
      p, .meta { color: var(--muted); }
      .topline, .row {
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 16px;
      }
      .panel {
        display: grid;
        gap: 14px;
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
      a.secondary {
        background: var(--secondary);
        color: var(--text);
      }
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
      {{ if .Rows }}
      {{ range .Rows }}
      <section class="panel">
        <div class="row">
          <div>
            <strong>{{ if .ConnectionName }}{{ .ConnectionName }}{{ else }}{{ .ConnectionID }}{{ end }}</strong>
            <div class="meta">{{ .RouteSummary }}</div>
          </div>
          <span class="state">{{ .State }}</span>
        </div>
        <div>
          {{ if .SettingsHref }}
          <a class="button" href="{{ .SettingsHref }}">Open settings</a>
          {{ else }}
          <span class="state">Settings unavailable</span>
          {{ end }}
        </div>
      </section>
      {{ end }}
      {{ else }}
      <section class="panel empty">No manageable connections are connected for this installation.</section>
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
	return backendManagedConnection{
		ID:        "",
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
	return backendManagedConnection{}, false
}

func installationTargetName(installation backendManagedInstallation) string {
	if installation.CurrentTarget != nil {
		if name := strings.TrimSpace(installation.CurrentTarget.Profile.Name); name != "" {
			return name
		}
	}
	return "No target selected"
}

func managedConnectionName(connection backendManagedConnection) string {
	if name := strings.TrimSpace(connection.DisplayName); name != "" {
		return name
	}
	if id := strings.TrimSpace(connection.ID); id != "" {
		return id
	}
	return "Connection"
}

func managedConnectionState(installation backendManagedInstallation, connection backendManagedConnection) string {
	if state := strings.TrimSpace(connection.State); state != "" {
		return state
	}
	return strings.TrimSpace(installation.State)
}

func routesFromInstallationConfig(config installationConfig) []backendManagedChannelRoute {
	routes := make([]backendManagedChannelRoute, 0, len(config.ChannelPolicies))
	for _, policy := range config.ChannelPolicies {
		requireMention := true
		if policy.RequireMention != nil {
			requireMention = *policy.RequireMention
		}
		enabled := true
		routes = append(routes, backendManagedChannelRoute{
			ExternalChannelID: strings.TrimSpace(policy.ExternalChannelID),
			RequireMention:    &requireMention,
			Enabled:           &enabled,
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].ExternalChannelID < routes[j].ExternalChannelID
	})
	return routes
}

func managedRouteEnabled(route backendManagedChannelRoute) bool {
	return route.Enabled == nil || *route.Enabled
}

func managedRouteRequireMention(route backendManagedChannelRoute) bool {
	return route.RequireMention == nil || *route.RequireMention
}

func channelPoliciesFromConnection(connection backendManagedConnection) []installationChannelPolicy {
	policies := make([]installationChannelPolicy, 0, len(connection.Routes))
	for _, route := range connection.Routes {
		if !managedRouteEnabled(route) {
			continue
		}
		channelID := strings.TrimSpace(route.ExternalChannelID)
		if channelID == "" {
			continue
		}
		requireMention := managedRouteRequireMention(route)
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

func channelSettingsRowsForInstallation(installation backendManagedInstallation) []channelSettingsListRow {
	connections := installation.Connections
	if len(connections) == 0 {
		connections = []backendManagedConnection{primaryManagedConnection(installation)}
	}
	targetName := installationTargetName(installation)
	rows := make([]channelSettingsListRow, 0, len(connections))
	for _, connection := range connections {
		rows = append(rows, channelSettingsListRow{
			InstallationID: strings.TrimSpace(installation.ID),
			ConnectionID:   strings.TrimSpace(connection.ID),
			ConnectionName: managedConnectionName(connection),
			TeamID:         strings.TrimSpace(installation.Route.ExternalTenantID),
			State:          managedConnectionState(installation, connection),
			TargetName:     targetName,
			RouteSummary:   routeSummary(connection),
			SettingsHref:   "",
		})
	}
	return rows
}

func channelSettingsRows(installations []backendManagedInstallation) []channelSettingsListRow {
	rows := []channelSettingsListRow{}
	for _, installation := range installations {
		rows = append(rows, channelSettingsRowsForInstallation(installation)...)
	}
	return rows
}

func (g *slackGateway) applyChannelSettingsRowLinks(rows []channelSettingsListRow) {
	for index := range rows {
		if strings.TrimSpace(rows[index].InstallationID) == "" || strings.TrimSpace(rows[index].ConnectionID) == "" {
			continue
		}
		rows[index].SettingsHref = g.channelSettingsConnectionPath(rows[index].InstallationID, rows[index].ConnectionID)
	}
}

func (g *slackGateway) renderChannelSettingsList(w http.ResponseWriter, r *http.Request, installations []backendManagedInstallation) {
	rows := channelSettingsRows(installations)
	g.applyChannelSettingsRowLinks(rows)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = channelSettingsListTemplate.Execute(w, channelSettingsPageData{
		Notice: channelSettingsNoticeFromRequest(r),
		Rows:   rows,
	})
}

func (g *slackGateway) renderChannelInstallationSettings(w http.ResponseWriter, r *http.Request, installation backendManagedInstallation) {
	rows := channelSettingsRowsForInstallation(installation)
	g.applyChannelSettingsRowLinks(rows)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = channelInstallationSettingsTemplate.Execute(w, channelInstallationSettingsPageData{
		Notice:         channelSettingsNoticeFromRequest(r),
		InstallationID: strings.TrimSpace(installation.ID),
		TeamID:         strings.TrimSpace(installation.Route.ExternalTenantID),
		State:          strings.TrimSpace(installation.State),
		TargetName:     installationTargetName(installation),
		Rows:           rows,
		BackHref:       g.channelSettingsPath(),
	})
}

func (g *slackGateway) renderChannelConnectionSettings(w http.ResponseWriter, r *http.Request, installation backendManagedInstallation, connection backendManagedConnection) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = channelConnectionSettingsTemplate.Execute(w, channelConnectionSettingsPageData{
		Notice:         channelSettingsNoticeFromRequest(r),
		InstallationID: installation.ID,
		ConnectionID:   connection.ID,
		TeamID:         strings.TrimSpace(installation.Route.ExternalTenantID),
		State:          strings.TrimSpace(installation.State),
		TargetName:     installationTargetName(installation),
		Rows:           channelRouteSettingsRows(connection),
		UpdateAction:   g.channelSettingsConnectionPath(installation.ID, connection.ID),
		BackHref:       g.channelSettingsInstallationPath(installation.ID),
	})
}
