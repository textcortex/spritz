package main

import (
	"net/http"
	"strings"
)

func (g *slackGateway) handleChannelSettings(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		g.redirectToReactRoute(
			w,
			r,
			reactSlackChannelSettingsPath(g.relativeGatewayPath(r.URL.Path), r.URL.Query()),
		)
		return
	}
	_ = principal
	if r.Method == http.MethodPost {
		http.Error(w, "legacy channel settings form removed; use /api/settings/channels", http.StatusGone)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func channelSettingsInstallationPathID(relativePath string) (string, bool) {
	trimmed := strings.Trim(strings.TrimPrefix(relativePath, "/settings/channels/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] != "installations" {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

func matchManagedChannelSettingsConnection(
	installations []backendManagedInstallation,
	relativePath string,
) (backendManagedInstallation, backendManagedConnection, bool) {
	trimmed := strings.Trim(strings.TrimPrefix(relativePath, "/settings/channels/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 4 || parts[0] != "installations" || parts[2] != "connections" {
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
			return backendManagedInstallation{}, backendManagedConnection{}, false
		}
		return installation, connection, true
	}
	return backendManagedInstallation{}, backendManagedConnection{}, false
}
