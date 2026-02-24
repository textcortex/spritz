{{/*
Shared validation for the default UI/API ingress surface.
*/}}
{{- define "spritz.validate.uiApiIngress" -}}
{{- if ne .Values.ui.namespace .Values.api.namespace }}
{{- fail "ui.namespace and api.namespace must match when ui.ingress.enabled=true" }}
{{- end }}
{{- if not .Values.global.host }}
{{- fail "global.host is required when ui.ingress.enabled=true" }}
{{- end }}
{{- if not .Values.global.ingress.className }}
{{- fail "global.ingress.className is required when ui.ingress.enabled=true" }}
{{- end }}
{{- end }}

{{/*
Shared validation for auth gateway wiring.
*/}}
{{- define "spritz.validate.authGatewayCore" -}}
{{- if .Values.authGateway.enabled }}
{{- $provider := lower (default "" .Values.authGateway.provider) }}
{{- if ne $provider "oauth2-proxy" }}
{{- fail "authGateway.provider must be oauth2-proxy when authGateway.enabled=true" }}
{{- end }}
{{- if not .Values.authGateway.oauth2Proxy.existingSecret }}
{{- fail "authGateway.oauth2Proxy.existingSecret is required when authGateway.enabled=true" }}
{{- end }}
{{- if not .Values.authGateway.oauth2Proxy.oidcIssuerURL }}
{{- fail "authGateway.oauth2Proxy.oidcIssuerURL is required when authGateway.enabled=true" }}
{{- end }}
{{- if not .Values.global.host }}
{{- fail "global.host is required when authGateway.enabled=true" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Validation for nginx ingress-based auth gateway integration.
*/}}
{{- define "spritz.validate.authGatewayIngress" -}}
{{- if .Values.authGateway.enabled }}
{{- if ne (lower .Values.global.ingress.className) "nginx" }}
{{- fail "global.ingress.className must be nginx when authGateway.enabled=true" }}
{{- end }}
{{- $mode := lower (default "" .Values.api.auth.mode) }}
{{- if not (or (eq $mode "header") (eq $mode "auto")) }}
{{- fail "api.auth.mode must be header or auto when authGateway.enabled=true" }}
{{- end }}
{{- end }}
{{- end }}
