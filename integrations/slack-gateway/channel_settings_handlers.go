package main

import (
	"net/http"
	"sort"
	"strings"
)

func (g *slackGateway) handleChannelSettings(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	installations, err := g.listManagedInstallations(r.Context(), principal.ID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"channel settings list failed",
			"err",
			err,
			"caller_auth_id",
			principal.ID,
		)
		http.Error(w, "channel settings unavailable", http.StatusBadGateway)
		return
	}

	relativePath := strings.TrimRight(g.relativeGatewayPath(r.URL.Path), "/")
	if relativePath == "/settings/channels" {
		g.renderChannelSettingsList(w, r, installations)
		return
	}

	installation, connection, ok := g.matchChannelSettingsConnectionPath(
		w,
		r,
		installations,
		relativePath,
	)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.renderChannelConnectionSettings(w, r, installation, connection)
	case http.MethodPost:
		g.handleChannelSettingsUpdate(w, r, principal.ID, installation, connection)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *slackGateway) matchChannelSettingsConnectionPath(
	w http.ResponseWriter,
	r *http.Request,
	installations []backendManagedInstallation,
	relativePath string,
) (backendManagedInstallation, backendManagedConnection, bool) {
	trimmed := strings.Trim(strings.TrimPrefix(relativePath, "/settings/channels/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 2 && parts[0] == "installations" {
		installationID := strings.TrimSpace(parts[1])
		for _, installation := range installations {
			if strings.TrimSpace(installation.ID) != installationID {
				continue
			}
			connection := primaryManagedConnection(installation)
			http.Redirect(
				w,
				r,
				g.channelSettingsConnectionPath(installation.ID, connection.ID),
				http.StatusSeeOther,
			)
			return backendManagedInstallation{}, backendManagedConnection{}, false
		}
		http.NotFound(w, r)
		return backendManagedInstallation{}, backendManagedConnection{}, false
	}
	if len(parts) != 4 || parts[0] != "installations" || parts[2] != "connections" {
		http.NotFound(w, r)
		return backendManagedInstallation{}, backendManagedConnection{}, false
	}
	installationID := strings.TrimSpace(parts[1])
	connectionID := strings.TrimSpace(parts[3])
	for _, installation := range installations {
		if strings.TrimSpace(installation.ID) != installationID {
			continue
		}
		connection, found := managedConnectionByID(installation, connectionID)
		if !found {
			http.NotFound(w, r)
			return backendManagedInstallation{}, backendManagedConnection{}, false
		}
		return installation, connection, true
	}
	http.NotFound(w, r)
	return backendManagedInstallation{}, backendManagedConnection{}, false
}

func (g *slackGateway) handleChannelSettingsUpdate(
	w http.ResponseWriter,
	r *http.Request,
	callerAuthID string,
	installation backendManagedInstallation,
	connection backendManagedConnection,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form payload", http.StatusBadRequest)
		return
	}
	channelID := strings.TrimSpace(r.FormValue("externalChannelId"))
	if channelID == "" {
		http.Error(w, "externalChannelId is required", http.StatusBadRequest)
		return
	}

	policiesByChannel := map[string]installationChannelPolicy{}
	for _, policy := range channelPoliciesFromConnection(connection) {
		policiesByChannel[policy.ExternalChannelID] = policy
	}

	switch strings.TrimSpace(r.FormValue("action")) {
	case "delete":
		delete(policiesByChannel, channelID)
	case "toggle":
		requireMention := strings.EqualFold(strings.TrimSpace(r.FormValue("requireMention")), "true")
		policiesByChannel[channelID] = installationChannelPolicy{
			ExternalChannelID: channelID,
			RequireMention:    &requireMention,
		}
	default:
		requireMention := r.FormValue("requireMention") == "on"
		policiesByChannel[channelID] = installationChannelPolicy{
			ExternalChannelID: channelID,
			RequireMention:    &requireMention,
		}
	}

	policies := make([]installationChannelPolicy, 0, len(policiesByChannel))
	for _, policy := range policiesByChannel {
		policies = append(policies, policy)
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].ExternalChannelID < policies[j].ExternalChannelID
	})
	requestID := newInstallRequestID()
	if err := g.updateManagedInstallationRoutes(
		r.Context(),
		callerAuthID,
		installation.ID,
		connection.ID,
		requestID,
		policies,
	); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"channel settings update failed",
			"err",
			err,
			"caller_auth_id",
			callerAuthID,
			"installation_id",
			installation.ID,
			"connection_id",
			connection.ID,
			"request_id",
			requestID,
		)
		http.Redirect(
			w,
			r,
			g.channelSettingsConnectionNoticeURL(
				installation.ID,
				connection.ID,
				"routes-update-failed",
			),
			http.StatusSeeOther,
		)
		return
	}
	http.Redirect(
		w,
		r,
		g.channelSettingsConnectionNoticeURL(
			installation.ID,
			connection.ID,
			"routes-updated",
		),
		http.StatusSeeOther,
	)
}

func (g *slackGateway) channelSettingsConnectionNoticeURL(installationID, connectionID, notice string) string {
	target := g.channelSettingsConnectionPath(installationID, connectionID)
	if strings.TrimSpace(notice) == "" {
		return target
	}
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	return target + separator + "notice=" + strings.TrimSpace(notice)
}
