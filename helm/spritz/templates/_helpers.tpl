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
{{- if and (eq (include "spritz.routing.mode" .) "ingress") (not .Values.global.ingress.className) }}
{{- fail "global.ingress.className is required when ui.ingress.enabled=true" }}
{{- end }}
{{- end }}

{{/*
Canonical shared-host route values.
*/}}
{{- define "spritz.routing.mode" -}}
{{- default "ingress" .Values.global.routing.mode | lower -}}
{{- end }}

{{- define "spritz.routeModel.type" -}}
{{- default "shared-host" .Values.global.routeModel.type | lower -}}
{{- end }}

{{- define "spritz.routeModel.apiPathPrefix" -}}
{{- default "/api" .Values.global.routeModel.apiPathPrefix -}}
{{- end }}

{{- define "spritz.routeModel.authPathPrefix" -}}
{{- default "/oauth2" .Values.global.routeModel.authPathPrefix -}}
{{- end }}

{{- define "spritz.routeModel.instancePathPrefix" -}}
{{- default "/i" .Values.global.routeModel.instancePathPrefix -}}
{{- end }}

{{- define "spritz.routeModel.chatPathPrefix" -}}
{{- default "/c" .Values.global.routeModel.chatPathPrefix -}}
{{- end }}

{{/*
Public host to use for shared-host route generation.
*/}}
{{- define "spritz.routeHost" -}}
{{- if and (eq (include "spritz.routeModel.type" .) "shared-host") .Values.global.host -}}
{{- .Values.global.host -}}
{{- end -}}
{{- end }}

{{/*
Shared-host route model validation.
*/}}
{{- define "spritz.validate.routeModel" -}}
{{- if ne (include "spritz.routeModel.type" .) "shared-host" }}
{{- fail "global.routeModel.type must be shared-host" }}
{{- end }}
{{- $routingMode := include "spritz.routing.mode" . }}
{{- if not (or (eq $routingMode "ingress") (eq $routingMode "gateway-api")) }}
{{- fail "global.routing.mode must be ingress or gateway-api" }}
{{- end }}
{{- if not (include "spritz.routeModel.apiPathPrefix" .) }}
{{- fail "global.routeModel.apiPathPrefix must not be empty" }}
{{- end }}
{{- if not (include "spritz.routeModel.authPathPrefix" .) }}
{{- fail "global.routeModel.authPathPrefix must not be empty" }}
{{- end }}
{{- if not (include "spritz.routeModel.instancePathPrefix" .) }}
{{- fail "global.routeModel.instancePathPrefix must not be empty" }}
{{- end }}
{{- if not (include "spritz.routeModel.chatPathPrefix" .) }}
{{- fail "global.routeModel.chatPathPrefix must not be empty" }}
{{- end }}
{{- if not (hasPrefix "/" (include "spritz.routeModel.apiPathPrefix" .)) }}
{{- fail "global.routeModel.apiPathPrefix must start with /" }}
{{- end }}
{{- if not (hasPrefix "/" (include "spritz.routeModel.authPathPrefix" .)) }}
{{- fail "global.routeModel.authPathPrefix must start with /" }}
{{- end }}
{{- if not (hasPrefix "/" (include "spritz.routeModel.instancePathPrefix" .)) }}
{{- fail "global.routeModel.instancePathPrefix must start with /" }}
{{- end }}
{{- if not (hasPrefix "/" (include "spritz.routeModel.chatPathPrefix" .)) }}
{{- fail "global.routeModel.chatPathPrefix must start with /" }}
{{- end }}
{{- if and .Values.authGateway.enabled (ne (include "spritz.routeModel.authPathPrefix" .) (default "/oauth2" .Values.authGateway.ingress.pathPrefix)) }}
{{- fail "authGateway.ingress.pathPrefix must match global.routeModel.authPathPrefix" }}
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
{{- if and .Values.authGateway.enabled (eq (include "spritz.routing.mode" .) "ingress") }}
{{- if ne (lower .Values.global.ingress.className) "nginx" }}
{{- fail "global.ingress.className must be nginx when authGateway.enabled=true" }}
{{- end }}
{{- $mode := lower (default "" .Values.api.auth.mode) }}
{{- if not (or (eq $mode "header") (eq $mode "auto")) }}
{{- fail "api.auth.mode must be header or auto when authGateway.enabled=true" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Validation for generic Gateway API route rendering.
*/}}
{{- define "spritz.validate.gatewayAPI" -}}
{{- if eq (include "spritz.routing.mode" .) "gateway-api" }}
{{- if not .Values.global.routing.gateway.className }}
{{- fail "global.routing.gateway.className is required when global.routing.mode=gateway-api" }}
{{- end }}
{{- if .Values.authGateway.enabled }}
{{- fail "authGateway.enabled is not supported when global.routing.mode=gateway-api because the generic chart only implements oauth2-proxy external auth for ingress mode" }}
{{- end }}
{{- end }}
{{- end }}
