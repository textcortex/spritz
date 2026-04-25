package main

import "net/http"

func (g *slackGateway) handleWorkspaceManagement(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	g.redirectToReactRoute(w, r, reactSlackWorkspacesPath(r.URL.Query()))
	_ = principal
}

func (g *slackGateway) handleWorkspaceTarget(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.handleWorkspaceTargetPicker(w, r)
	case http.MethodPost:
		g.handleWorkspaceTargetUpdate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *slackGateway) handleWorkspaceTargetPicker(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	g.redirectToReactRoute(w, r, reactSlackWorkspaceTargetPath(r.URL.Query()))
	_ = principal
}

func (g *slackGateway) handleWorkspaceTargetUpdate(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "legacy workspace target form removed; use /api/slack/workspaces/target", http.StatusGone)
}

func (g *slackGateway) handleWorkspaceDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.Error(w, "legacy workspace disconnect form removed; use /api/slack/workspaces/disconnect", http.StatusGone)
}
