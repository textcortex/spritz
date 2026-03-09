#!/usr/bin/env bash
set -euo pipefail

config_dir="${OPENCLAW_CONFIG_DIR:-${HOME}/.openclaw}"
config_path="${OPENCLAW_CONFIG_PATH:-${config_dir}/openclaw.json}"
gateway_port="${OPENCLAW_GATEWAY_PORT:-8080}"
gateway_mode="${OPENCLAW_GATEWAY_MODE:-local}"
gateway_bind="${OPENCLAW_GATEWAY_BIND:-lan}"
auto_start="${OPENCLAW_AUTO_START:-true}"
acp_enabled="${OPENCLAW_ACP_ENABLED:-true}"
acp_bind="${OPENCLAW_ACP_BIND:-0.0.0.0}"
acp_port="${OPENCLAW_ACP_PORT:-2529}"
acp_path="${OPENCLAW_ACP_PATH:-/}"
bridge_bin="${SPRITZ_OPENCLAW_BRIDGE_BIN:-/usr/local/bin/spritz-openclaw-acp-bridge}"
spritz_entrypoint_bin="${SPRITZ_OPENCLAW_MAIN_ENTRYPOINT:-/usr/local/bin/spritz-entrypoint}"

detect_bridge_gateway_host() {
  if [[ -n "${SPRITZ_OPENCLAW_ACP_GATEWAY_HOST:-}" ]]; then
    printf '%s\n' "${SPRITZ_OPENCLAW_ACP_GATEWAY_HOST}"
    return
  fi

  local host_ips="" candidate=""
  if host_ips="$(hostname -i 2>/dev/null)"; then
    candidate="$(printf '%s\n' "${host_ips}" | tr ' ' '\n' | awk '/^[0-9]+\./ { print; exit }')"
    if [[ -n "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
    candidate="$(printf '%s\n' "${host_ips}" | tr ' ' '\n' | sed -n '/./{p;q;}')"
    if [[ -n "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  fi

  printf '127.0.0.1\n'
}

mkdir -p "${config_dir}"

if [[ -n "${OPENCLAW_CONFIG_JSON:-}" ]]; then
  printf '%s\n' "${OPENCLAW_CONFIG_JSON}" > "${config_path}"
elif [[ -n "${OPENCLAW_CONFIG_B64:-}" ]]; then
  printf '%s' "${OPENCLAW_CONFIG_B64}" | base64 --decode > "${config_path}"
elif [[ -n "${OPENCLAW_CONFIG_FILE:-}" && -f "${OPENCLAW_CONFIG_FILE}" ]]; then
  cp "${OPENCLAW_CONFIG_FILE}" "${config_path}"
elif [[ ! -f "${config_path}" ]]; then
  cat > "${config_path}" <<'JSON'
{
  "browser": {
    "enabled": true,
    "headless": true,
    "executablePath": "/usr/bin/chromium"
  }
}
JSON
fi

chmod 600 "${config_path}" || true

# Force OpenClaw to use the same file path we prepared above.
export OPENCLAW_CONFIG_PATH="${config_path}"

# Keep gateway defaults deterministic for Spritz web routing.
openclaw config set gateway.mode "${gateway_mode}" >/dev/null
openclaw config set gateway.port "${gateway_port}" >/dev/null
openclaw config set gateway.bind "${gateway_bind}" >/dev/null

token="${OPENCLAW_GATEWAY_TOKEN:-}"
if [[ -z "${token}" ]]; then
  token="$(openclaw config get gateway.auth.token 2>/dev/null || true)"
fi
if [[ -z "${token}" ]]; then
  token="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
fi
export OPENCLAW_GATEWAY_TOKEN="${token}"
openclaw config set gateway.auth.token "${OPENCLAW_GATEWAY_TOKEN}" >/dev/null

gateway_token_file="${OPENCLAW_GATEWAY_TOKEN_FILE:-${config_dir}/gateway.token}"
printf '%s\n' "${OPENCLAW_GATEWAY_TOKEN}" > "${gateway_token_file}"
chmod 600 "${gateway_token_file}" || true

should_auto_start=false
if [[ "${auto_start}" == "true" ]]; then
  if [[ "$#" -eq 0 ]]; then
    should_auto_start=true
  elif [[ "$#" -eq 2 && "$1" == "sleep" && "$2" == "infinity" ]]; then
    should_auto_start=true
  fi
fi

if [[ "${should_auto_start}" == "true" ]]; then
  set -- openclaw gateway run --bind "${gateway_bind}" --port "${gateway_port}"
fi

lower_acp_enabled="$(printf '%s' "${acp_enabled}" | tr '[:upper:]' '[:lower:]')"
if [[ "${lower_acp_enabled}" == "false" || "${lower_acp_enabled}" == "0" || "${lower_acp_enabled}" == "no" || "${lower_acp_enabled}" == "off" ]]; then
  exec "${spritz_entrypoint_bin}" "$@"
fi

bridge_gateway_host="$(detect_bridge_gateway_host)"
export SPRITZ_OPENCLAW_ACP_GATEWAY_URL="${SPRITZ_OPENCLAW_ACP_GATEWAY_URL:-ws://${bridge_gateway_host}:${gateway_port}}"
export SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE="${SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE:-${gateway_token_file}}"
export SPRITZ_OPENCLAW_ACP_LISTEN_ADDR="${SPRITZ_OPENCLAW_ACP_LISTEN_ADDR:-${acp_bind}:${acp_port}}"
export SPRITZ_OPENCLAW_ACP_PATH="${SPRITZ_OPENCLAW_ACP_PATH:-${acp_path}}"

"${bridge_bin}" &
bridge_pid=$!

"${spritz_entrypoint_bin}" "$@" &
main_pid=$!

cleanup() {
  kill "${main_pid}" "${bridge_pid}" 2>/dev/null || true
}

trap cleanup INT TERM

wait -n "${main_pid}" "${bridge_pid}"
status=$?

cleanup
wait "${main_pid}" 2>/dev/null || true
wait "${bridge_pid}" 2>/dev/null || true

exit "${status}"
