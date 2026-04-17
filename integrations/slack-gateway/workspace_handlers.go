package main

import (
	"net/http"
	"strings"
)

func (g *slackGateway) handleWorkspaceManagement(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(w, r)
	if !ok {
		return
	}
	installations, err := g.listManagedInstallations(r.Context(), principal.ID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace management list failed",
			"err",
			err,
			"caller_auth_id",
			principal.ID,
		)
		http.Error(w, "workspace list unavailable", http.StatusBadGateway)
		return
	}
	g.renderWorkspaceManagementPage(w, r, installations)
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
	principal, ok := requireBrowserPrincipal(w, r)
	if !ok {
		return
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	if teamID == "" {
		http.Error(w, "teamId is required", http.StatusBadRequest)
		return
	}
	requestID := newInstallRequestID()
	targets, err := g.listInstallTargetsForOwnerAuthID(
		r.Context(),
		teamID,
		principal.ID,
		requestID,
		nil,
	)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace target picker lookup failed",
			"err",
			err,
			"caller_auth_id",
			principal.ID,
			"team_id",
			teamID,
			"request_id",
			requestID,
		)
		http.Redirect(
			w,
			r,
			g.buildWorkspaceNoticeURL("target-update-failed", teamID),
			http.StatusSeeOther,
		)
		return
	}
	if len(targets) == 0 {
		http.Redirect(
			w,
			r,
			g.buildWorkspaceNoticeURL("target-update-failed", teamID),
			http.StatusSeeOther,
		)
		return
	}
	g.renderWorkspaceTargetPicker(w, teamID, requestID, targets)
}

func (g *slackGateway) handleWorkspaceTargetUpdate(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(w, r)
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
	presetInputs, err := decodeInstallTargetSelection(r.FormValue("target"))
	if err != nil {
		http.Error(w, "install target is invalid", http.StatusBadRequest)
		return
	}
	requestID := strings.TrimSpace(r.FormValue("requestId"))
	if requestID == "" {
		requestID = newInstallRequestID()
	}
	if err := g.updateManagedInstallationTarget(
		r.Context(),
		principal.ID,
		teamID,
		requestID,
		presetInputs,
	); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace target update failed",
			"err",
			err,
			"caller_auth_id",
			principal.ID,
			"team_id",
			teamID,
			"request_id",
			requestID,
		)
		http.Redirect(
			w,
			r,
			g.buildWorkspaceNoticeURL("target-update-failed", teamID),
			http.StatusSeeOther,
		)
		return
	}
	http.Redirect(
		w,
		r,
		g.buildWorkspaceNoticeURL("target-updated", teamID),
		http.StatusSeeOther,
	)
}

func (g *slackGateway) handleWorkspaceDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	principal, ok := requireBrowserPrincipal(w, r)
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
	if err := g.disconnectManagedInstallation(r.Context(), principal.ID, teamID); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace disconnect failed",
			"err",
			err,
			"caller_auth_id",
			principal.ID,
			"team_id",
			teamID,
		)
		http.Redirect(
			w,
			r,
			g.buildWorkspaceNoticeURL("workspace-disconnect-failed", teamID),
			http.StatusSeeOther,
		)
		return
	}
	http.Redirect(
		w,
		r,
		g.buildWorkspaceNoticeURL("workspace-disconnected", teamID),
		http.StatusSeeOther,
	)
}
