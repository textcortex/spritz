package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type installResultStatus string

const (
	installResultStatusSuccess installResultStatus = "success"
	installResultStatusError   installResultStatus = "error"
)

const installResultOperationChannelInstall = "channel.install"

type installResultCode string

const (
	installResultCodeInstalled           installResultCode = "installed"
	installResultCodeStateInvalid        installResultCode = "state.invalid"
	installResultCodeStateExpired        installResultCode = "state.expired"
	installResultCodeAuthDenied          installResultCode = "auth.denied"
	installResultCodeAuthFailed          installResultCode = "auth.failed"
	installResultCodeIdentityUnresolved  installResultCode = "identity.unresolved"
	installResultCodeIdentityForbidden   installResultCode = "identity.forbidden"
	installResultCodeIdentityAmbiguous   installResultCode = "identity.ambiguous"
	installResultCodeTargetsEmpty        installResultCode = "install.targets.empty"
	installResultCodeTargetsUnavailable  installResultCode = "install.targets.unavailable"
	installResultCodeTargetInvalid       installResultCode = "install.target.invalid"
	installResultCodeTargetForbidden     installResultCode = "install.target.forbidden"
	installResultCodeTargetConflict      installResultCode = "install.target.conflict"
	installResultCodeRegistryConflict    installResultCode = "registry.conflict"
	installResultCodeResolverUnavailable installResultCode = "resolver.unavailable"
	installResultCodeRuntimeUnavailable  installResultCode = "runtime.unavailable"
	installResultCodeInternalError       installResultCode = "internal.error"
)

type installResult struct {
	Status    installResultStatus
	Code      installResultCode
	Operation string
	Retryable bool
	Provider  string
	RequestID string
	TeamID    string
}

type installResultDescriptor struct {
	Title       string
	Message     string
	Retryable   bool
	ActionLabel string
	ActionHref  string
}

type backendInstallErrorPayload struct {
	Status    string `json:"status"`
	Field     string `json:"field,omitempty"`
	Error     string `json:"error,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

func classifyInstallStateError(err error) installResultCode {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "expired") {
		return installResultCodeStateExpired
	}
	return installResultCodeStateInvalid
}

func newInstallRequestID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return "install-request-id-unavailable"
}

func (g *slackGateway) installResultPath() string {
	return g.publicPathPrefix() + "/slack/install/result"
}

func (g *slackGateway) installRedirectPath() string {
	return g.publicPathPrefix() + "/slack/install"
}

func (g *slackGateway) installRedirectURL() string {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(g.cfg.PublicURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return g.installRedirectPath()
	}
	basePath := strings.TrimRight(base.Path, "/")
	routePath := "/slack/install"
	base.RawPath = ""
	base.Path = basePath + routePath
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
}

func (g *slackGateway) redirectToInstallResult(w http.ResponseWriter, r *http.Request, result installResult) {
	target := url.URL{Path: g.installResultPath()}
	query := target.Query()
	query.Set("status", string(result.Status))
	query.Set("code", string(result.Code))
	query.Set("provider", firstNonEmpty(result.Provider, slackProvider))
	if operation := strings.TrimSpace(result.Operation); operation != "" {
		query.Set("operation", operation)
	}
	if result.Retryable {
		query.Set("retryable", "true")
	}
	if requestID := strings.TrimSpace(result.RequestID); requestID != "" {
		query.Set("requestId", requestID)
	}
	if teamID := strings.TrimSpace(result.TeamID); teamID != "" {
		query.Set("teamId", teamID)
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func installResultDescriptorFor(code installResultCode, installURL string) installResultDescriptor {
	switch code {
	case installResultCodeInstalled:
		return installResultDescriptor{
			Title:   "Slack workspace connected",
			Message: "The shared Slack app is installed and ready. You can close this tab.",
		}
	case installResultCodeStateExpired:
		return installResultDescriptor{
			Title:       "Install link expired",
			Message:     "This install link expired before it completed. Start the install again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeStateInvalid:
		return installResultDescriptor{
			Title:       "Install link is invalid",
			Message:     "This install callback could not be verified. Start the install again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeAuthDenied:
		return installResultDescriptor{
			Title:       "Slack authorization was cancelled",
			Message:     "The Slack install did not finish because authorization was denied or cancelled.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeAuthFailed:
		return installResultDescriptor{
			Title:       "Slack authorization failed",
			Message:     "The Slack install did not complete successfully. Please try again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeIdentityUnresolved:
		return installResultDescriptor{
			Title:       "Install could not be linked",
			Message:     "This Slack install could not be linked to an owner account yet. Link the expected account, then start the install again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeIdentityForbidden:
		return installResultDescriptor{
			Title:       "Install is not allowed",
			Message:     "This Slack identity is not allowed to complete the install for this deployment.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeIdentityAmbiguous:
		return installResultDescriptor{
			Title:       "Install owner is ambiguous",
			Message:     "This Slack install matched more than one possible owner. Resolve the account mapping, then start the install again.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeTargetsEmpty:
		return installResultDescriptor{
			Title:       "No install targets are available",
			Message:     "This account does not have any eligible targets for this workspace install yet.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeTargetsUnavailable:
		return installResultDescriptor{
			Title:       "Install targets are unavailable",
			Message:     "The install target picker could not be loaded right now. Please try again shortly.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeTargetInvalid:
		return installResultDescriptor{
			Title:       "Selected install target is invalid",
			Message:     "The chosen install target is no longer valid. Start the install again and pick a current target.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeTargetForbidden:
		return installResultDescriptor{
			Title:       "Selected install target is not allowed",
			Message:     "This install target is not available for the current installer.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeTargetConflict:
		return installResultDescriptor{
			Title:       "Install target selection is ambiguous",
			Message:     "The requested install target could not be resolved uniquely. Start the install again and choose a specific target.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeRegistryConflict:
		return installResultDescriptor{
			Title:       "Install conflicts with existing state",
			Message:     "This workspace already has conflicting install state. Resolve the existing binding, then start the install again.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeResolverUnavailable:
		return installResultDescriptor{
			Title:       "Install could not be completed",
			Message:     "The install service is temporarily unavailable. Please try again shortly.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeRuntimeUnavailable:
		return installResultDescriptor{
			Title:       "Install is still being prepared",
			Message:     "The workspace binding is not ready yet. Please try again shortly.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	default:
		return installResultDescriptor{
			Title:       "Install failed",
			Message:     "The install did not complete successfully. Please try again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	}
}

func normalizeInstallResultCode(raw string) installResultCode {
	switch installResultCode(strings.TrimSpace(raw)) {
	case installResultCodeInstalled,
		installResultCodeStateInvalid,
		installResultCodeStateExpired,
		installResultCodeAuthDenied,
		installResultCodeAuthFailed,
		installResultCodeIdentityUnresolved,
		installResultCodeIdentityForbidden,
		installResultCodeIdentityAmbiguous,
		installResultCodeTargetsEmpty,
		installResultCodeTargetsUnavailable,
		installResultCodeTargetInvalid,
		installResultCodeTargetForbidden,
		installResultCodeTargetConflict,
		installResultCodeRegistryConflict,
		installResultCodeResolverUnavailable,
		installResultCodeRuntimeUnavailable,
		installResultCodeInternalError:
		return installResultCode(strings.TrimSpace(raw))
	case "install_state_invalid":
		return installResultCodeStateInvalid
	case "install_state_expired":
		return installResultCodeStateExpired
	case "provider_authorization_denied":
		return installResultCodeAuthDenied
	case "provider_authorization_failed":
		return installResultCodeAuthFailed
	case "external_identity_unresolved":
		return installResultCodeIdentityUnresolved
	case "external_identity_forbidden":
		return installResultCodeIdentityForbidden
	case "external_identity_ambiguous":
		return installResultCodeIdentityAmbiguous
	case "install_targets_empty":
		return installResultCodeTargetsEmpty
	case "install_targets_unavailable":
		return installResultCodeTargetsUnavailable
	case "install_target_invalid":
		return installResultCodeTargetInvalid
	case "install_target_forbidden":
		return installResultCodeTargetForbidden
	case "install_target_conflict":
		return installResultCodeTargetConflict
	case "installation_conflict":
		return installResultCodeRegistryConflict
	case "installation_registry_unavailable":
		return installResultCodeResolverUnavailable
	case "runtime_binding_unavailable":
		return installResultCodeRuntimeUnavailable
	default:
		return installResultCodeInternalError
	}
}

func classifyInstallUpsertError(err error) installResultCode {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return installResultCodeInternalError
	}
	var payload backendInstallErrorPayload
	if json.Unmarshal([]byte(strings.TrimSpace(statusErr.body)), &payload) == nil {
		if code := normalizeInstallResultCode(payload.Error); code != installResultCodeInternalError {
			return code
		}
		if payload.Status == "unresolved" && payload.Field == "ownerRef" {
			return installResultCodeIdentityUnresolved
		}
		if payload.Status == "forbidden" && payload.Field == "ownerRef" {
			return installResultCodeIdentityForbidden
		}
		if payload.Status == "ambiguous" && payload.Field == "ownerRef" {
			return installResultCodeIdentityAmbiguous
		}
		if payload.Status == "ambiguous" {
			return installResultCodeRegistryConflict
		}
		if payload.Status == "unavailable" {
			return installResultCodeResolverUnavailable
		}
	}
	switch statusErr.statusCode {
	case http.StatusServiceUnavailable:
		return installResultCodeResolverUnavailable
	case http.StatusConflict:
		return installResultCodeRegistryConflict
	default:
		if statusErr.statusCode >= http.StatusInternalServerError {
			return installResultCodeResolverUnavailable
		}
		return installResultCodeInternalError
	}
}

func (g *slackGateway) handleInstallResult(w http.ResponseWriter, r *http.Request) {
	target := url.URL{Path: reactSlackInstallResultPath(), RawQuery: r.URL.RawQuery}
	g.redirectToReactRoute(w, r, target.String())
}
