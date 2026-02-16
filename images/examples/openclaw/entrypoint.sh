#!/usr/bin/env bash
set -euo pipefail

config_dir="${OPENCLAW_CONFIG_DIR:-${HOME}/.openclaw}"
config_path="${OPENCLAW_CONFIG_PATH:-${config_dir}/openclaw.json}"
gateway_port="${OPENCLAW_GATEWAY_PORT:-8080}"
gateway_mode="${OPENCLAW_GATEWAY_MODE:-local}"
auto_start="${OPENCLAW_AUTO_START:-true}"

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

token="${OPENCLAW_GATEWAY_TOKEN:-}"
if [[ -z "${token}" ]]; then
  token="$(openclaw config get gateway.auth.token 2>/dev/null || true)"
fi
if [[ -z "${token}" ]]; then
  token="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
fi
export OPENCLAW_GATEWAY_TOKEN="${token}"
openclaw config set gateway.auth.token "${OPENCLAW_GATEWAY_TOKEN}" >/dev/null

should_auto_start=false
if [[ "${auto_start}" == "true" ]]; then
  if [[ "$#" -eq 0 ]]; then
    should_auto_start=true
  elif [[ "$#" -eq 2 && "$1" == "sleep" && "$2" == "infinity" ]]; then
    should_auto_start=true
  fi
fi

if [[ "${should_auto_start}" == "true" ]]; then
  set -- openclaw gateway run --bind custom --port "${gateway_port}"
fi

exec /usr/local/bin/spritz-entrypoint "$@"
