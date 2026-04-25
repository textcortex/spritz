package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

type slackGatewayErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type installSelectionResponse struct {
	Status    string                 `json:"status"`
	State     string                 `json:"state"`
	RequestID string                 `json:"requestId"`
	TeamID    string                 `json:"teamId"`
	Targets   []backendInstallTarget `json:"targets"`
}

type installSelectionRequest struct {
	State        string         `json:"state"`
	RequestID    string         `json:"requestId"`
	PresetInputs map[string]any `json:"presetInputs"`
}

type workspaceTargetRequest struct {
	TeamID       string         `json:"teamId"`
	RequestID    string         `json:"requestId"`
	PresetInputs map[string]any `json:"presetInputs"`
}

type workspaceDisconnectRequest struct {
	TeamID string `json:"teamId"`
}

type workspaceTestRequest struct {
	TeamID    string `json:"teamId"`
	ChannelID string `json:"channelId"`
	ThreadTS  string `json:"threadTs"`
	Prompt    string `json:"prompt"`
	Mode      string `json:"mode"`
}

type workspaceTestResponse struct {
	Status          string `json:"status"`
	Outcome         string `json:"outcome,omitempty"`
	Reply           string `json:"reply,omitempty"`
	ConversationID  string `json:"conversationId,omitempty"`
	PostedMessageTS string `json:"postedMessageTs,omitempty"`
}

type channelRoutesUpdateRequest struct {
	RequestID       string                      `json:"requestId"`
	ChannelPolicies []installationChannelPolicy `json:"channelPolicies"`
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, slackGatewayErrorResponse{
		Status:  "error",
		Message: strings.TrimSpace(message),
	})
}

func decodeJSONRequest(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func (g *slackGateway) handleInstallTargetSelectionAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.handleInstallTargetSelectionAPIGet(w, r)
	case http.MethodPost:
		g.handleInstallTargetSelectionAPIPost(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *slackGateway) handleInstallTargetSelectionAPIGet(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	pendingInstall, err := g.state.parsePendingInstall(state)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "install state is invalid or expired")
		return
	}
	targets, err := g.listInstallTargets(r.Context(), &pendingInstall.Installation, pendingInstall.RequestID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"install target selection api lookup failed",
			"err", err,
			"team_id", pendingInstall.Installation.TeamID,
			"request_id", pendingInstall.RequestID,
		)
		writeAPIError(w, http.StatusBadGateway, "install targets unavailable")
		return
	}
	writeJSON(w, http.StatusOK, installSelectionResponse{
		Status:    "resolved",
		State:     state,
		RequestID: pendingInstall.RequestID,
		TeamID:    pendingInstall.Installation.TeamID,
		Targets:   targets,
	})
}

func (g *slackGateway) handleInstallTargetSelectionAPIPost(w http.ResponseWriter, r *http.Request) {
	var body installSelectionRequest
	if err := decodeJSONRequest(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pendingInstall, err := g.state.parsePendingInstall(strings.TrimSpace(body.State))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "install state is invalid or expired")
		return
	}
	requestID := firstNonEmpty(strings.TrimSpace(body.RequestID), pendingInstall.RequestID)
	if len(body.PresetInputs) == 0 {
		writeAPIError(w, http.StatusBadRequest, "presetInputs is required")
		return
	}
	installation := pendingInstall.Installation
	if err := g.upsertInstallation(r.Context(), &installation, requestID, body.PresetInputs); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"install target selection api upsert failed",
			"err", err,
			"team_id", installation.TeamID,
			"request_id", requestID,
		)
		g.writeInstallResultAPI(w, http.StatusOK, installResult{
			Status:    installResultStatusError,
			Code:      classifyInstallUpsertError(err),
			Operation: installResultOperationChannelInstall,
			Provider:  slackProvider,
			RequestID: requestID,
			TeamID:    installation.TeamID,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "installed",
		"requestId": requestID,
		"teamId":    installation.TeamID,
	})
}

func (g *slackGateway) handleInstallResultAPI(w http.ResponseWriter, r *http.Request) {
	result := installResult{
		Status:    installResultStatus(firstNonEmpty(r.URL.Query().Get("status"), string(installResultStatusError))),
		Code:      normalizeInstallResultCode(firstNonEmpty(r.URL.Query().Get("code"), string(installResultCodeInternalError))),
		Operation: strings.TrimSpace(r.URL.Query().Get("operation")),
		Retryable: strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("retryable")), "true"),
		Provider:  firstNonEmpty(r.URL.Query().Get("provider"), slackProvider),
		RequestID: strings.TrimSpace(r.URL.Query().Get("requestId")),
		TeamID:    strings.TrimSpace(r.URL.Query().Get("teamId")),
	}
	g.writeInstallResultAPI(w, http.StatusOK, result)
}

func (g *slackGateway) writeInstallResultAPI(w http.ResponseWriter, status int, result installResult) {
	descriptor := installResultDescriptorFor(result.Code, g.installRedirectPath())
	if result.Status == installResultStatusSuccess && result.Code == installResultCodeInternalError {
		descriptor = installResultDescriptorFor(installResultCodeInstalled, g.installRedirectPath())
	}
	writeJSON(w, status, map[string]any{
		"status":      result.Status,
		"code":        result.Code,
		"operation":   result.Operation,
		"provider":    result.Provider,
		"requestId":   result.RequestID,
		"teamId":      result.TeamID,
		"title":       descriptor.Title,
		"message":     descriptor.Message,
		"retryable":   descriptor.Retryable,
		"actionLabel": descriptor.ActionLabel,
		"actionHref":  descriptor.ActionHref,
	})
}

func (g *slackGateway) handleWorkspaceManagementAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	installations, err := g.listManagedInstallations(r.Context(), principal.ID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace management api list failed",
			"err", err,
			"caller_auth_id", principal.ID,
		)
		writeAPIError(w, http.StatusBadGateway, "workspace list unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "resolved",
		"installations": installations,
	})
}

func (g *slackGateway) handleWorkspaceTargetAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.handleWorkspaceTargetAPIGet(w, r)
	case http.MethodPost:
		g.handleWorkspaceTargetAPIPost(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *slackGateway) handleWorkspaceTargetAPIGet(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	if teamID == "" {
		writeAPIError(w, http.StatusBadRequest, "teamId is required")
		return
	}
	requestID := newInstallRequestID()
	targets, err := g.listInstallTargetsForOwnerAuthID(r.Context(), teamID, principal.ID, requestID, nil)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace target api lookup failed",
			"err", err,
			"caller_auth_id", principal.ID,
			"team_id", teamID,
			"request_id", requestID,
		)
		writeAPIError(w, http.StatusBadGateway, "workspace targets unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "resolved",
		"teamId":    teamID,
		"requestId": requestID,
		"targets":   targets,
	})
}

func (g *slackGateway) handleWorkspaceTargetAPIPost(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	var body workspaceTargetRequest
	if err := decodeJSONRequest(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	teamID := strings.TrimSpace(body.TeamID)
	if teamID == "" {
		writeAPIError(w, http.StatusBadRequest, "teamId is required")
		return
	}
	if len(body.PresetInputs) == 0 {
		writeAPIError(w, http.StatusBadRequest, "presetInputs is required")
		return
	}
	requestID := firstNonEmpty(body.RequestID, newInstallRequestID())
	if err := g.updateManagedInstallationTarget(r.Context(), principal.ID, teamID, requestID, body.PresetInputs); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace target api update failed",
			"err", err,
			"caller_auth_id", principal.ID,
			"team_id", teamID,
			"request_id", requestID,
		)
		writeAPIError(w, http.StatusBadGateway, "workspace target update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "updated",
		"teamId":    teamID,
		"requestId": requestID,
	})
}

func (g *slackGateway) handleWorkspaceDisconnectAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	var body workspaceDisconnectRequest
	if err := decodeJSONRequest(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	teamID := strings.TrimSpace(body.TeamID)
	if teamID == "" {
		writeAPIError(w, http.StatusBadRequest, "teamId is required")
		return
	}
	if err := g.disconnectManagedInstallation(r.Context(), principal.ID, teamID); err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace disconnect api failed",
			"err", err,
			"caller_auth_id", principal.ID,
			"team_id", teamID,
		)
		writeAPIError(w, http.StatusBadGateway, "workspace disconnect failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "disconnected",
		"teamId": teamID,
	})
}

func (g *slackGateway) handleWorkspaceTestAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	var body workspaceTestRequest
	if err := decodeJSONRequest(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	teamID := strings.TrimSpace(body.TeamID)
	if teamID == "" {
		writeAPIError(w, http.StatusBadRequest, "teamId is required")
		return
	}
	installation, err := g.lookupManagedWorkspace(r, principal.ID, teamID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"workspace test api lookup failed",
			"err", err,
			"caller_auth_id", principal.ID,
			"team_id", teamID,
		)
		writeAPIError(w, http.StatusBadGateway, "workspace lookup unavailable")
		return
	}
	if installation == nil {
		writeAPIError(w, http.StatusForbidden, "workspace not manageable")
		return
	}
	envelope, err := inferSyntheticSlackEvent(body.ChannelID, body.Prompt, body.ThreadTS)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	envelope.TeamID = teamID
	delivery, process, err := g.beginMessageEventDelivery(envelope)
	if err != nil {
		writeAPIError(w, http.StatusConflict, err.Error())
		return
	}
	if !process {
		writeJSON(w, http.StatusOK, workspaceTestResponse{
			Status:  "resolved",
			Outcome: messageEventOutcomeIgnored,
		})
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "real"
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
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workspaceTestResponse{
		Status:          "resolved",
		Outcome:         result.Outcome,
		Reply:           result.Reply,
		ConversationID:  result.ConversationID,
		PostedMessageTS: result.PostedMessageTS,
	})
}

func (g *slackGateway) handleChannelSettingsAPI(w http.ResponseWriter, r *http.Request) {
	principal, ok := requireBrowserPrincipal(g.cfg, w, r)
	if !ok {
		return
	}
	installations, err := g.listManagedInstallations(r.Context(), principal.ID)
	if err != nil {
		g.logger.ErrorContext(
			r.Context(),
			"channel settings api list failed",
			"err", err,
			"caller_auth_id", principal.ID,
		)
		writeAPIError(w, http.StatusBadGateway, "channel settings unavailable")
		return
	}

	relativePath := strings.TrimRight(g.relativeGatewayPath(r.URL.Path), "/")
	relativePath = strings.TrimPrefix(relativePath, "/api")
	if relativePath == "/settings/channels" {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "resolved",
			"installations": installations,
		})
		return
	}

	if installationID, ok := channelSettingsInstallationPathID(relativePath); ok {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		for _, installation := range installations {
			if strings.TrimSpace(installation.ID) == installationID {
				writeJSON(w, http.StatusOK, map[string]any{
					"status":       "resolved",
					"installation": installation,
				})
				return
			}
		}
		writeAPIError(w, http.StatusNotFound, "installation not found")
		return
	}

	installation, connection, found := g.matchChannelSettingsConnectionPath(
		w,
		r,
		installations,
		relativePath,
	)
	if !found {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "resolved",
			"installation": installation,
			"connection":   connection,
		})
	case http.MethodPut:
		g.handleChannelSettingsAPIUpdate(w, r, principal.ID, installation, connection)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *slackGateway) handleChannelSettingsAPIUpdate(
	w http.ResponseWriter,
	r *http.Request,
	callerAuthID string,
	installation backendManagedInstallation,
	connection backendManagedConnection,
) {
	var body channelRoutesUpdateRequest
	if err := decodeJSONRequest(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	policiesByChannel := map[string]installationChannelPolicy{}
	for _, policy := range body.ChannelPolicies {
		channelID := strings.TrimSpace(policy.ExternalChannelID)
		if channelID == "" {
			continue
		}
		requireMention := true
		if policy.RequireMention != nil {
			requireMention = *policy.RequireMention
		}
		policiesByChannel[channelID] = installationChannelPolicy{
			ExternalChannelID:   channelID,
			ExternalChannelType: strings.TrimSpace(policy.ExternalChannelType),
			RequireMention:      &requireMention,
		}
	}
	policies := make([]installationChannelPolicy, 0, len(policiesByChannel))
	for _, policy := range policiesByChannel {
		policies = append(policies, policy)
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].ExternalChannelID < policies[j].ExternalChannelID
	})
	requestID := firstNonEmpty(body.RequestID, newInstallRequestID())
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
			"channel settings api update failed",
			"err", err,
			"caller_auth_id", callerAuthID,
			"installation_id", installation.ID,
			"connection_id", connection.ID,
			"request_id", requestID,
		)
		writeAPIError(w, http.StatusBadGateway, "channel routes update failed")
		return
	}
	g.policies.forget(installation.Route.ExternalTenantID)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "updated",
		"installation": installation.ID,
		"connection":   connection.ID,
		"requestId":    requestID,
	})
}
