#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
spritz_root="$(cd "${script_dir}/.." && pwd)"
chart_dir="${spritz_root}/helm/spritz"
example_values="${chart_dir}/examples/portable-oidc-auth.values.yaml"
tmp_dir="$(mktemp -d)"

cleanup() {
  rm -rf "${tmp_dir}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if ! command -v helm >/dev/null 2>&1; then
  echo "ERROR: helm is required but not found in PATH" >&2
  exit 1
fi

expect_failure() {
  local expected_message="$1"
  shift
  local output_file
  output_file="$(mktemp)"

  if "$@" >"${output_file}" 2>&1; then
    echo "ERROR: expected command to fail but it succeeded: $*" >&2
    cat "${output_file}" >&2
    rm -f "${output_file}"
    exit 1
  fi

  if ! grep -q "${expected_message}" "${output_file}"; then
    echo "ERROR: expected failure message not found: ${expected_message}" >&2
    cat "${output_file}" >&2
    rm -f "${output_file}"
    exit 1
  fi

  rm -f "${output_file}"
}

expect_contains() {
  local file="$1"
  local needle="$2"
  local description="$3"
  if ! grep -Fq "${needle}" "${file}"; then
    echo "ERROR: expected rendered output to contain ${description}" >&2
    exit 1
  fi
}

expect_not_contains() {
  local file="$1"
  local needle="$2"
  local description="$3"
  if grep -Fq "${needle}" "${file}"; then
    echo "ERROR: expected rendered output to not contain ${description}" >&2
    exit 1
  fi
}

default_render="${tmp_dir}/default.yaml"
auth_render="${tmp_dir}/auth.yaml"
auth_annotations_render="${tmp_dir}/auth-annotations.yaml"
acp_network_policy_render="${tmp_dir}/acp-network-policy.yaml"

helm lint "${chart_dir}"
helm template spritz "${chart_dir}" >"${default_render}"
helm template spritz "${chart_dir}" -f "${example_values}" >"${auth_render}"
helm template spritz "${chart_dir}" -f "${example_values}" --set authGateway.ingress.annotations.authonly=enabled >"${auth_annotations_render}"
helm template spritz "${chart_dir}" --set acp.networkPolicy.enabled=true >"${acp_network_policy_render}"

expect_contains "${default_render}" "name: spritz-web" "spritz-web ingress in default render"
expect_not_contains "${default_render}" "name: spritz-auth" "spritz-auth ingress when auth gateway is disabled"

expect_contains "${auth_render}" "name: spritz-auth" "spritz-auth ingress in auth render"
expect_contains "${auth_render}" "path: /oauth2" "oauth2 ingress path in auth render"
expect_contains "${auth_render}" "nginx.ingress.kubernetes.io/auth-url:" "nginx auth-url annotation in auth render"
expect_contains "${auth_render}" "nginx.ingress.kubernetes.io/auth-signin:" "nginx auth-signin annotation in auth render"
expect_contains "${auth_render}" "nginx.ingress.kubernetes.io/configuration-snippet:" "identity header injection snippet in auth render"
expect_contains "${auth_annotations_render}" "authonly: enabled" "auth ingress custom annotations in auth render"
expect_contains "${acp_network_policy_render}" "kind: NetworkPolicy" "ACP network policy when enabled"
expect_contains "${acp_network_policy_render}" "name: spritz-acp" "ACP network policy name when enabled"
expect_contains "${default_render}" 'resources: ["spritzes/status", "spritzconversations/status"]' "status RBAC for spritz conversations"
expect_contains "${default_render}" "name: SPRITZ_AUTH_HEADER_TYPE" "principal type auth header wiring"
expect_contains "${default_render}" "name: SPRITZ_AUTH_BEARER_SCOPES_PATHS" "bearer scope path wiring"
expect_not_contains "${default_render}" "name: SPRITZ_AUTH_BEARER_DEFAULT_TYPE" "forced bearer default type wiring when chart leaves it unset"
expect_contains "${default_render}" "name: SPRITZ_PROVISIONER_DEFAULT_IDLE_TTL" "default provisioner idle ttl wiring"
expect_contains "${default_render}" "name: SPRITZ_PROVISIONER_DEFAULT_TTL" "default provisioner ttl wiring"
expect_contains "${default_render}" "name: SPRITZ_TERMINAL_ACTIVITY_DEBOUNCE" "terminal activity debounce wiring"
expect_contains "${default_render}" 'resources: ["configmaps"]' "configmap RBAC for idempotency reservations"

expect_failure \
  "api.auth.mode must be header or auto when authGateway.enabled=true" \
  helm template spritz "${chart_dir}" -f "${example_values}" --set api.auth.mode=none

expect_failure \
  "global.ingress.className must be nginx when authGateway.enabled=true" \
  helm template spritz "${chart_dir}" -f "${example_values}" --set global.ingress.className=traefik

expect_failure \
  "operator.homePVC has been removed; use operator.homeSizeLimit and sharedMounts instead" \
  helm template spritz "${chart_dir}" --set operator.homePVC.enabled=true

expect_failure \
  "operator.sharedConfigPVC has been removed; use operator.sharedMounts/api.sharedMounts instead" \
  helm template spritz "${chart_dir}" --set operator.sharedConfigPVC.enabled=true

echo "helm checks passed"
