package main

import (
	"net/http"
	"net/url"
	"strings"
)

func reactSlackInstallResultPath() string {
	return "/settings/slack/install/result"
}

func reactSlackInstallSelectPath(requestID string) string {
	target := url.URL{Path: "/settings/slack/install/select"}
	if requestID := strings.TrimSpace(requestID); requestID != "" {
		query := target.Query()
		query.Set("requestId", requestID)
		target.RawQuery = query.Encode()
	}
	return target.String()
}

func reactSlackWorkspacesPath(query url.Values) string {
	target := url.URL{Path: "/settings/slack/workspaces"}
	target.RawQuery = query.Encode()
	return target.String()
}

func reactSlackWorkspaceTargetPath(query url.Values) string {
	target := url.URL{Path: "/settings/slack/workspaces/target"}
	target.RawQuery = query.Encode()
	return target.String()
}

func reactSlackWorkspaceTestPath(query url.Values) string {
	target := url.URL{Path: "/settings/slack/workspaces/test"}
	target.RawQuery = query.Encode()
	return target.String()
}

func reactSlackChannelSettingsPath(relativeGatewayPath string, query url.Values) string {
	relative := strings.TrimPrefix(strings.TrimSpace(relativeGatewayPath), "/settings/channels")
	target := url.URL{Path: "/settings/slack/channels" + relative}
	target.RawQuery = query.Encode()
	return target.String()
}

func redirectToReactRoute(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, target, http.StatusSeeOther)
}
