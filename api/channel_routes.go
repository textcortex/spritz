package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"
)

var channelRouteScopeTypeTokenPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type channelRouteResolveRequest struct {
	RequestID         string `json:"requestId,omitempty"`
	Provider          string `json:"provider"`
	ExternalScopeType string `json:"externalScopeType"`
	ExternalTenantID  string `json:"externalTenantId"`
}

type channelRouteResolveOutput struct {
	Namespace  string `json:"namespace"`
	InstanceID string `json:"instanceId"`
	State      string `json:"state,omitempty"`
}

func normalizeChannelRouteResolveRequest(body channelRouteResolveRequest) (channelRouteResolveRequest, error) {
	body.RequestID = strings.TrimSpace(body.RequestID)
	body.Provider = strings.ToLower(strings.TrimSpace(body.Provider))
	body.ExternalScopeType = strings.ToLower(strings.TrimSpace(body.ExternalScopeType))
	body.ExternalTenantID = strings.TrimSpace(body.ExternalTenantID)

	if body.Provider == "" || !externalOwnerProviderTokenPattern.MatchString(body.Provider) {
		return channelRouteResolveRequest{}, errors.New("provider is invalid")
	}
	if body.ExternalScopeType == "" || !channelRouteScopeTypeTokenPattern.MatchString(body.ExternalScopeType) {
		return channelRouteResolveRequest{}, errors.New("externalScopeType is invalid")
	}
	if body.ExternalTenantID == "" {
		return channelRouteResolveRequest{}, errors.New("externalTenantId is required")
	}
	return body, nil
}

func parseChannelRouteResolveOutput(raw json.RawMessage) (channelRouteResolveOutput, error) {
	if len(raw) == 0 {
		return channelRouteResolveOutput{}, errors.New("resolver output is required")
	}
	var output channelRouteResolveOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		return channelRouteResolveOutput{}, fmt.Errorf("resolver output is invalid: %w", err)
	}
	output.Namespace = strings.TrimSpace(output.Namespace)
	output.InstanceID = strings.TrimSpace(output.InstanceID)
	output.State = strings.TrimSpace(output.State)
	if output.Namespace == "" {
		return channelRouteResolveOutput{}, errors.New("resolver output namespace is required")
	}
	if output.InstanceID == "" {
		return channelRouteResolveOutput{}, errors.New("resolver output instanceId is required")
	}
	return output, nil
}

func (s *server) resolveChannelRoute(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if !principal.isService() && !principal.isAdminPrincipal() {
		return writeForbidden(c)
	}
	if principal.isService() && !principal.hasScope(scopeChannelRouteResolve) && !principal.isAdminPrincipal() {
		return writeForbidden(c)
	}

	var body channelRouteResolveRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	normalized, err := normalizeChannelRouteResolveRequest(body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	resolver, response, err := s.extensions.resolve(
		c.Request().Context(),
		extensionOperationChannelRouteResolve,
		principal,
		normalized.RequestID,
		extensionRequestContext{
			Namespace: s.namespace,
		},
		map[string]string{
			"provider":          normalized.Provider,
			"externalScopeType": normalized.ExternalScopeType,
			"externalTenantId":  normalized.ExternalTenantID,
		},
	)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "channel route resolver failed")
	}
	if resolver == nil {
		return writeError(c, http.StatusServiceUnavailable, "channel route resolver is not configured")
	}

	switch response.Status {
	case "", extensionStatusResolved:
		output, err := parseChannelRouteResolveOutput(response.Output)
		if err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		return writeJSON(c, http.StatusOK, output)
	case extensionStatusUnresolved:
		return writeError(c, http.StatusNotFound, "channel route is unresolved")
	case extensionStatusForbidden:
		return writeForbidden(c)
	case extensionStatusAmbiguous:
		return writeError(c, http.StatusConflict, "channel route is ambiguous")
	case extensionStatusInvalid:
		return writeError(c, http.StatusBadRequest, "channel route input is invalid")
	case extensionStatusUnavailable:
		return writeError(c, http.StatusServiceUnavailable, "channel route resolver is unavailable")
	default:
		return writeError(c, http.StatusInternalServerError, "channel route resolver returned an unsupported status")
	}
}
