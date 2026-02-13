#!/usr/bin/env bash
set -euo pipefail

config_dir="${OPENCLAW_CONFIG_DIR:-${HOME}/.openclaw}"
config_path="${OPENCLAW_CONFIG_PATH:-${config_dir}/openclaw.json}"

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

exec /usr/local/bin/spritz-entrypoint "$@"
