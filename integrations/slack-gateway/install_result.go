package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

type installResultStatus string

const (
	installResultStatusSuccess installResultStatus = "success"
	installResultStatusError   installResultStatus = "error"
)

type installResultCode string

const (
	installResultCodeInstalled                       installResultCode = "installed"
	installResultCodeInstallStateInvalid             installResultCode = "install_state_invalid"
	installResultCodeInstallStateExpired             installResultCode = "install_state_expired"
	installResultCodeProviderAuthorizationDenied     installResultCode = "provider_authorization_denied"
	installResultCodeProviderAuthorizationFailed     installResultCode = "provider_authorization_failed"
	installResultCodeExternalIdentityUnresolved      installResultCode = "external_identity_unresolved"
	installResultCodeExternalIdentityForbidden       installResultCode = "external_identity_forbidden"
	installResultCodeExternalIdentityAmbiguous       installResultCode = "external_identity_ambiguous"
	installResultCodeInstallationConflict            installResultCode = "installation_conflict"
	installResultCodeInstallationRegistryUnavailable installResultCode = "installation_registry_unavailable"
	installResultCodeRuntimeBindingUnavailable       installResultCode = "runtime_binding_unavailable"
	installResultCodeInternalError                   installResultCode = "internal_error"
)

type installResult struct {
	Status    installResultStatus
	Code      installResultCode
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

type installResultPageData struct {
	Title       string
	Message     string
	RequestID   string
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

var installResultPageTemplate = template.Must(template.New("install-result").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{ .Title }}</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f6f4ee;
        --surface: #fffdf8;
        --border: #d9d2c4;
        --text: #1f1b16;
        --muted: #62584b;
        --accent: #0f766e;
        --danger: #9a3412;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: linear-gradient(180deg, var(--bg), #efe9dd);
        color: var(--text);
        display: grid;
        place-items: center;
        padding: 24px;
      }
      main {
        width: min(560px, 100%);
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: 20px;
        padding: 28px;
        box-shadow: 0 24px 80px rgba(31, 27, 22, 0.08);
      }
      h1 {
        margin: 0 0 12px;
        font-size: 30px;
        line-height: 1.1;
      }
      p {
        margin: 0;
        font-size: 16px;
        line-height: 1.6;
        color: var(--muted);
      }
      .meta {
        margin-top: 18px;
        padding-top: 18px;
        border-top: 1px solid var(--border);
        font-size: 13px;
        color: var(--muted);
      }
      .action {
        display: inline-flex;
        align-items: center;
        margin-top: 22px;
        padding: 11px 16px;
        border-radius: 999px;
        background: var(--text);
        color: #fff;
        text-decoration: none;
        font-weight: 600;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>{{ .Title }}</h1>
      <p>{{ .Message }}</p>
      {{ if .ActionHref }}
      <a class="action" href="{{ .ActionHref }}">{{ .ActionLabel }}</a>
      {{ end }}
      {{ if .RequestID }}
      <div class="meta">Request ID: <code>{{ .RequestID }}</code></div>
      {{ end }}
    </main>
  </body>
</html>`))

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

func (g *slackGateway) redirectToInstallResult(w http.ResponseWriter, r *http.Request, result installResult) {
	target := url.URL{Path: g.installResultPath()}
	query := target.Query()
	query.Set("status", string(result.Status))
	query.Set("code", string(result.Code))
	query.Set("provider", firstNonEmpty(result.Provider, slackProvider))
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
	case installResultCodeInstallStateExpired:
		return installResultDescriptor{
			Title:       "Install link expired",
			Message:     "This install link expired before it completed. Start the install again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeInstallStateInvalid:
		return installResultDescriptor{
			Title:       "Install link is invalid",
			Message:     "This install callback could not be verified. Start the install again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeProviderAuthorizationDenied:
		return installResultDescriptor{
			Title:       "Slack authorization was cancelled",
			Message:     "The Slack install did not finish because authorization was denied or cancelled.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeProviderAuthorizationFailed:
		return installResultDescriptor{
			Title:       "Slack authorization failed",
			Message:     "The Slack install did not complete successfully. Please try again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeExternalIdentityUnresolved:
		return installResultDescriptor{
			Title:       "Install could not be linked",
			Message:     "This Slack install could not be linked to an owner account yet. Link the expected account, then start the install again.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeExternalIdentityForbidden:
		return installResultDescriptor{
			Title:       "Install is not allowed",
			Message:     "This Slack identity is not allowed to complete the install for this deployment.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeExternalIdentityAmbiguous:
		return installResultDescriptor{
			Title:       "Install owner is ambiguous",
			Message:     "This Slack install matched more than one possible owner. Resolve the account mapping, then start the install again.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeInstallationConflict:
		return installResultDescriptor{
			Title:       "Install conflicts with existing state",
			Message:     "This workspace already has conflicting install state. Resolve the existing binding, then start the install again.",
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeInstallationRegistryUnavailable:
		return installResultDescriptor{
			Title:       "Install could not be completed",
			Message:     "The install service is temporarily unavailable. Please try again shortly.",
			Retryable:   true,
			ActionLabel: "Start install again",
			ActionHref:  installURL,
		}
	case installResultCodeRuntimeBindingUnavailable:
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
		installResultCodeInstallStateInvalid,
		installResultCodeInstallStateExpired,
		installResultCodeProviderAuthorizationDenied,
		installResultCodeProviderAuthorizationFailed,
		installResultCodeExternalIdentityUnresolved,
		installResultCodeExternalIdentityForbidden,
		installResultCodeExternalIdentityAmbiguous,
		installResultCodeInstallationConflict,
		installResultCodeInstallationRegistryUnavailable,
		installResultCodeRuntimeBindingUnavailable,
		installResultCodeInternalError:
		return installResultCode(strings.TrimSpace(raw))
	default:
		return installResultCodeInternalError
	}
}

func classifyInstallUpsertError(err error) installResultCode {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return installResultCodeInternalError
	}
	if statusErr.statusCode >= http.StatusInternalServerError {
		return installResultCodeInstallationRegistryUnavailable
	}
	var payload backendInstallErrorPayload
	if json.Unmarshal([]byte(strings.TrimSpace(statusErr.body)), &payload) == nil {
		if code := normalizeInstallResultCode(payload.Error); code != installResultCodeInternalError {
			return code
		}
		if statusErr.statusCode == http.StatusNotFound && payload.Status == "unresolved" && payload.Field == "ownerRef" {
			return installResultCodeExternalIdentityUnresolved
		}
		if statusErr.statusCode == http.StatusForbidden && payload.Status == "forbidden" && payload.Field == "ownerRef" {
			return installResultCodeExternalIdentityForbidden
		}
		if statusErr.statusCode == http.StatusConflict && payload.Status == "ambiguous" && payload.Field == "ownerRef" {
			return installResultCodeExternalIdentityAmbiguous
		}
		if statusErr.statusCode == http.StatusConflict && payload.Status == "ambiguous" {
			return installResultCodeInstallationConflict
		}
		if statusErr.statusCode == http.StatusServiceUnavailable && payload.Status == "unavailable" {
			return installResultCodeInstallationRegistryUnavailable
		}
	}
	switch statusErr.statusCode {
	case http.StatusServiceUnavailable:
		return installResultCodeInstallationRegistryUnavailable
	case http.StatusConflict:
		return installResultCodeInstallationConflict
	default:
		return installResultCodeInternalError
	}
}

func (g *slackGateway) handleInstallResult(w http.ResponseWriter, r *http.Request) {
	result := installResult{
		Status:    installResultStatus(firstNonEmpty(r.URL.Query().Get("status"), string(installResultStatusError))),
		Code:      normalizeInstallResultCode(firstNonEmpty(r.URL.Query().Get("code"), string(installResultCodeInternalError))),
		Provider:  firstNonEmpty(r.URL.Query().Get("provider"), slackProvider),
		RequestID: strings.TrimSpace(r.URL.Query().Get("requestId")),
		TeamID:    strings.TrimSpace(r.URL.Query().Get("teamId")),
	}
	descriptor := installResultDescriptorFor(result.Code, g.installRedirectPath())
	if result.Status == installResultStatusSuccess && result.Code == installResultCodeInternalError {
		descriptor = installResultDescriptorFor(installResultCodeInstalled, g.installRedirectPath())
	}
	page := installResultPageData{
		Title:       descriptor.Title,
		Message:     descriptor.Message,
		RequestID:   result.RequestID,
		ActionLabel: descriptor.ActionLabel,
		ActionHref:  descriptor.ActionHref,
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = installResultPageTemplate.Execute(w, page)
}
