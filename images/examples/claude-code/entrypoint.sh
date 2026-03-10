#!/usr/bin/env bash
set -euo pipefail

acp_enabled="${SPRITZ_CLAUDE_CODE_ACP_ENABLED:-true}"
acp_bind="${SPRITZ_CLAUDE_CODE_ACP_BIND:-0.0.0.0}"
acp_port="${SPRITZ_CLAUDE_CODE_ACP_PORT:-2529}"
acp_path="${SPRITZ_CLAUDE_CODE_ACP_PATH:-/}"
server_bin="${SPRITZ_CLAUDE_CODE_SERVER_BIN:-/usr/local/bin/spritz-claude-code-acp-server}"
spritz_entrypoint_bin="${SPRITZ_CLAUDE_CODE_MAIN_ENTRYPOINT:-/usr/local/bin/spritz-entrypoint}"

lower_acp_enabled="$(printf '%s' "${acp_enabled}" | tr '[:upper:]' '[:lower:]')"
if [[ "${lower_acp_enabled}" == "false" || "${lower_acp_enabled}" == "0" || "${lower_acp_enabled}" == "no" || "${lower_acp_enabled}" == "off" ]]; then
  exec "${spritz_entrypoint_bin}" "$@"
fi

export SPRITZ_CLAUDE_CODE_ACP_LISTEN_ADDR="${SPRITZ_CLAUDE_CODE_ACP_LISTEN_ADDR:-${acp_bind}:${acp_port}}"
export SPRITZ_CLAUDE_CODE_ACP_PATH="${SPRITZ_CLAUDE_CODE_ACP_PATH:-${acp_path}}"
export SPRITZ_CLAUDE_CODE_ACP_HEALTH_PATH="${SPRITZ_CLAUDE_CODE_ACP_HEALTH_PATH:-/healthz}"
export SPRITZ_CLAUDE_CODE_ACP_METADATA_PATH="${SPRITZ_CLAUDE_CODE_ACP_METADATA_PATH:-/.well-known/spritz-acp}"
export SPRITZ_CLAUDE_CODE_ACP_BIN="${SPRITZ_CLAUDE_CODE_ACP_BIN:-claude-agent-acp}"
export SPRITZ_CLAUDE_CODE_ACP_ARGS_JSON="${SPRITZ_CLAUDE_CODE_ACP_ARGS_JSON:-[]}"
export SPRITZ_CLAUDE_CODE_REQUIRED_ENV="${SPRITZ_CLAUDE_CODE_REQUIRED_ENV:-ANTHROPIC_API_KEY}"
export SPRITZ_CLAUDE_CODE_WORKDIR="${SPRITZ_CLAUDE_CODE_WORKDIR:-/workspace}"
export SPRITZ_CLAUDE_CODE_AGENT_NAME="${SPRITZ_CLAUDE_CODE_AGENT_NAME:-claude-agent-acp}"
export SPRITZ_CLAUDE_CODE_AGENT_TITLE="${SPRITZ_CLAUDE_CODE_AGENT_TITLE:-Claude Code ACP Gateway}"

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
