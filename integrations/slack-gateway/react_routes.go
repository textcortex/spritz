package main

import (
	"net/http"
	"net/url"
	"strings"
)

func reactSlackInstallResultPath() string {
	return "/settings/slack/install/result"
}

func reactSlackInstallSelectPath(requestID string, pendingState string) string {
	target := url.URL{Path: "/settings/slack/install/select"}
	query := target.Query()
	if requestID := strings.TrimSpace(requestID); requestID != "" {
		query.Set("requestId", requestID)
	}
	if pendingState := strings.TrimSpace(pendingState); pendingState != "" {
		query.Set("state", pendingState)
	}
	if len(query) > 0 {
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

func (g *slackGateway) reactRouteURL(target string) string {
	route, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return target
	}
	if route.IsAbs() {
		return route.String()
	}

	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(g.reactBaseURL()), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return route.String()
	}
	basePath := strings.TrimRight(base.Path, "/")
	routePath := "/" + strings.TrimLeft(route.Path, "/")
	base.RawPath = ""
	base.Path = basePath + routePath
	base.RawQuery = route.RawQuery
	base.Fragment = route.Fragment
	return base.String()
}

func (g *slackGateway) reactBaseURL() string {
	if baseURL := strings.TrimSpace(g.cfg.ReactBaseURL); baseURL != "" {
		return baseURL
	}
	return g.cfg.SpritzBaseURL
}

func (g *slackGateway) reactRoutesShareGatewayOrigin() bool {
	gatewayURL, err := url.Parse(strings.TrimSpace(g.cfg.PublicURL))
	if err != nil || gatewayURL.Scheme == "" || gatewayURL.Host == "" {
		return false
	}
	reactURL, err := url.Parse(strings.TrimSpace(g.reactBaseURL()))
	if err != nil || reactURL.Scheme == "" || reactURL.Host == "" {
		return false
	}
	return strings.EqualFold(gatewayURL.Scheme, reactURL.Scheme) &&
		strings.EqualFold(gatewayURL.Host, reactURL.Host)
}

func (g *slackGateway) redirectToReactRoute(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, g.reactRouteURL(target), http.StatusSeeOther)
}
