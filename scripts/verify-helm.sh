#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
spritz_root="$(cd "${script_dir}/.." && pwd)"
chart_dir="${spritz_root}/helm/spritz"
example_values="${chart_dir}/examples/portable-oidc-auth.values.yaml"

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

helm lint "${chart_dir}"
helm template spritz "${chart_dir}" >/dev/null
helm template spritz "${chart_dir}" -f "${example_values}" >/dev/null

expect_failure \
  "api.auth.mode must be header or auto when authGateway.enabled=true" \
  helm template spritz "${chart_dir}" -f "${example_values}" --set api.auth.mode=none

expect_failure \
  "global.ingress.className must be nginx when authGateway.enabled=true" \
  helm template spritz "${chart_dir}" -f "${example_values}" --set global.ingress.className=traefik

echo "helm checks passed"
