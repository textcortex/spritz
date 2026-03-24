#!/usr/bin/env bash
set -euo pipefail

auto_login="${SPRITZ_CODEX_AUTO_LOGIN:-true}"
acp_enabled="${SPRITZ_CODEX_ACP_ENABLED:-true}"
acp_bind="${SPRITZ_CODEX_ACP_BIND:-0.0.0.0}"
acp_port="${SPRITZ_CODEX_ACP_PORT:-2529}"
acp_path="${SPRITZ_CODEX_ACP_PATH:-/}"
codex_bin="${SPRITZ_CODEX_BIN:-codex}"
server_bin="${SPRITZ_CODEX_SERVER_BIN:-/usr/local/bin/spritz-codex-acp-server}"
spritz_entrypoint_bin="${SPRITZ_CODEX_MAIN_ENTRYPOINT:-/usr/local/bin/spritz-entrypoint}"

lower_auto_login="$(printf '%s' "${auto_login}" | tr '[:upper:]' '[:lower:]')"
if [[ -n "${OPENAI_API_KEY:-}" && "${lower_auto_login}" != "false" && "${lower_auto_login}" != "0" && "${lower_auto_login}" != "no" && "${lower_auto_login}" != "off" ]]; then
  printf '%s' "${OPENAI_API_KEY}" | "${codex_bin}" login --with-api-key >/dev/null
fi

lower_acp_enabled="$(printf '%s' "${acp_enabled}" | tr '[:upper:]' '[:lower:]')"
if [[ "${lower_acp_enabled}" == "false" || "${lower_acp_enabled}" == "0" || "${lower_acp_enabled}" == "no" || "${lower_acp_enabled}" == "off" ]]; then
  exec "${spritz_entrypoint_bin}" "$@"
fi

export SPRITZ_CODEX_ACP_LISTEN_ADDR="${SPRITZ_CODEX_ACP_LISTEN_ADDR:-${acp_bind}:${acp_port}}"
export SPRITZ_CODEX_ACP_PATH="${SPRITZ_CODEX_ACP_PATH:-${acp_path}}"
export SPRITZ_CODEX_ACP_HEALTH_PATH="${SPRITZ_CODEX_ACP_HEALTH_PATH:-/healthz}"
export SPRITZ_CODEX_ACP_METADATA_PATH="${SPRITZ_CODEX_ACP_METADATA_PATH:-/.well-known/spritz-acp}"
export SPRITZ_CODEX_BIN="${SPRITZ_CODEX_BIN:-${codex_bin}}"
export SPRITZ_CODEX_ARGS_JSON="${SPRITZ_CODEX_ARGS_JSON:-[\"--dangerously-bypass-approvals-and-sandbox\"]}"
export SPRITZ_CODEX_WORKDIR="${SPRITZ_CODEX_WORKDIR:-/workspace}"
export SPRITZ_CODEX_REQUIRED_ENV="${SPRITZ_CODEX_REQUIRED_ENV:-OPENAI_API_KEY}"
export SPRITZ_CODEX_AGENT_NAME="${SPRITZ_CODEX_AGENT_NAME:-codex-cli}"
export SPRITZ_CODEX_AGENT_TITLE="${SPRITZ_CODEX_AGENT_TITLE:-Codex ACP Gateway}"
export SPRITZ_CODEX_PACKAGE_ROOT="${SPRITZ_CODEX_PACKAGE_ROOT:-/usr/local/lib/node_modules/@openai/codex}"

"${server_bin}" &
server_pid=$!

"${spritz_entrypoint_bin}" "$@" &
main_pid=$!

cleanup() {
  kill "${main_pid}" "${server_pid}" 2>/dev/null || true
}

trap cleanup INT TERM

wait -n "${main_pid}" "${server_pid}"
status=$?

cleanup
wait "${main_pid}" 2>/dev/null || true
wait "${server_pid}" 2>/dev/null || true

exit "${status}"
