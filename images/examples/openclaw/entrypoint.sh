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
server_bin="${SPRITZ_OPENCLAW_SERVER_BIN:-/usr/local/bin/spritz-openclaw-acp-server}"
spritz_entrypoint_bin="${SPRITZ_OPENCLAW_MAIN_ENTRYPOINT:-/usr/local/bin/spritz-entrypoint}"
auth_store_path="${OPENCLAW_AUTH_PROFILES_PATH:-${config_dir}/agents/main/agent/auth-profiles.json}"

detect_bridge_gateway_host() {
  if [[ -n "${SPRITZ_OPENCLAW_ACP_GATEWAY_HOST:-}" ]]; then
    printf '%s\n' "${SPRITZ_OPENCLAW_ACP_GATEWAY_HOST}"
    return
  fi

  printf '127.0.0.1\n'
}

prepare_acp_trusted_proxy_bridge() {
  node - "${config_path}" <<'NODE'
const fs = require("node:fs");

const configPath = process.argv[2];
const raw = fs.readFileSync(configPath, "utf8");
const cfg = JSON.parse(raw);
const gateway = cfg.gateway ?? {};
const auth = gateway.auth ?? {};
const trustedProxy = auth.trustedProxy ?? {};
const trustedProxies = Array.isArray(gateway.trustedProxies) ? gateway.trustedProxies : [];
const authMode = typeof auth.mode === "string" ? auth.mode.trim() : "";

if (authMode !== "trusted-proxy") {
  process.stdout.write("{}");
  process.exit(0);
}

const mergedTrustedProxies = [];
for (const value of [...trustedProxies, "127.0.0.1", "::1"]) {
  if (typeof value !== "string") {
    continue;
  }
  const trimmed = value.trim();
  if (!trimmed || mergedTrustedProxies.includes(trimmed)) {
    continue;
  }
  mergedTrustedProxies.push(trimmed);
}

gateway.trustedProxies = mergedTrustedProxies;
cfg.gateway = gateway;
fs.writeFileSync(configPath, JSON.stringify(cfg, null, 2) + "\n");

const bridgeUser = process.env.SPRITZ_OPENCLAW_ACP_TRUSTED_PROXY_USER?.trim() || "spritz-acp-bridge";
const bridgeEmail =
  process.env.SPRITZ_OPENCLAW_ACP_TRUSTED_PROXY_EMAIL?.trim() ||
  "spritz-acp-bridge@example.invalid";
const userHeader =
  typeof trustedProxy.userHeader === "string" && trustedProxy.userHeader.trim()
    ? trustedProxy.userHeader.trim()
    : "x-forwarded-user";
const requiredHeaders = Array.isArray(trustedProxy.requiredHeaders)
  ? trustedProxy.requiredHeaders
  : [];

const headers = {
  "x-forwarded-user": bridgeUser,
  "x-forwarded-email": bridgeEmail,
  "x-forwarded-proto": "https",
  "x-forwarded-host": "localhost",
  "x-forwarded-for": "127.0.0.1",
  "x-real-ip": "127.0.0.1",
};

headers[userHeader.toLowerCase()] = userHeader.toLowerCase().includes("email")
  ? bridgeEmail
  : bridgeUser;

for (const value of requiredHeaders) {
  if (typeof value !== "string") {
    continue;
  }
  const header = value.trim().toLowerCase();
  if (!header || headers[header]) {
    continue;
  }
  if (header.includes("email")) {
    headers[header] = bridgeEmail;
    continue;
  }
  if (header === "x-forwarded-proto") {
    headers[header] = "https";
    continue;
  }
  if (header === "x-forwarded-host") {
    headers[header] = "localhost";
    continue;
  }
  if (header === "x-forwarded-for" || header === "x-real-ip") {
    headers[header] = "127.0.0.1";
    continue;
  }
  headers[header] = "1";
}

process.stdout.write(JSON.stringify(headers));
NODE
}

seed_env_auth_profiles() {
  local auth_store_dir
  auth_store_dir="$(dirname "${auth_store_path}")"
  mkdir -p "${auth_store_dir}"

  node - "${auth_store_path}" <<'NODE'
const fs = require("node:fs");

const authStorePath = process.argv[2];
const env = process.env;

const candidates = [];
if (typeof env.ANTHROPIC_API_KEY === "string" && env.ANTHROPIC_API_KEY.trim()) {
  candidates.push({
    profileId: "anthropic:default",
    provider: "anthropic",
    envKey: "ANTHROPIC_API_KEY",
  });
}

if (candidates.length === 0) {
  process.exit(0);
}

let store = {};
if (fs.existsSync(authStorePath)) {
  try {
    store = JSON.parse(fs.readFileSync(authStorePath, "utf8"));
  } catch {
    store = {};
  }
}
if (!store || typeof store !== "object" || Array.isArray(store)) {
  store = {};
}
const profiles = store.profiles && typeof store.profiles === "object" && !Array.isArray(store.profiles)
  ? { ...store.profiles }
  : {};
const lastGood = store.lastGood && typeof store.lastGood === "object" && !Array.isArray(store.lastGood)
  ? { ...store.lastGood }
  : {};

for (const candidate of candidates) {
  profiles[candidate.profileId] = {
    ...(profiles[candidate.profileId] && typeof profiles[candidate.profileId] === "object"
      ? profiles[candidate.profileId]
      : {}),
    type: "api_key",
    provider: candidate.provider,
    keyRef: {
      source: "env",
      provider: "default",
      id: candidate.envKey,
    },
  };
  lastGood[candidate.provider] = candidate.profileId;
}

const nextStore = {
  ...store,
  version: 1,
  profiles,
  lastGood,
};

fs.writeFileSync(authStorePath, `${JSON.stringify(nextStore, null, 2)}\n`, {
  mode: 0o600,
});
NODE
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
seed_env_auth_profiles

# Force OpenClaw to use the same file path we prepared above.
export OPENCLAW_CONFIG_PATH="${config_path}"

if [[ -z "${SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON:-}" ]]; then
  export SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON="$(prepare_acp_trusted_proxy_bridge)"
fi
if [[ "${SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON:-}" != "" && "${SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON:-}" != "{}" ]]; then
  export SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE="${SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE:-1}"
fi

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
export SPRITZ_OPENCLAW_ACP_ALLOW_INSECURE_PRIVATE_WS="${SPRITZ_OPENCLAW_ACP_ALLOW_INSECURE_PRIVATE_WS:-0}"
export SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE="${SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE:-${gateway_token_file}}"
export SPRITZ_OPENCLAW_ACP_LISTEN_ADDR="${SPRITZ_OPENCLAW_ACP_LISTEN_ADDR:-${acp_bind}:${acp_port}}"
export SPRITZ_OPENCLAW_ACP_PATH="${SPRITZ_OPENCLAW_ACP_PATH:-${acp_path}}"
export SPRITZ_OPENCLAW_ACP_HEALTH_PATH="${SPRITZ_OPENCLAW_ACP_HEALTH_PATH:-/healthz}"
export SPRITZ_OPENCLAW_ACP_METADATA_PATH="${SPRITZ_OPENCLAW_ACP_METADATA_PATH:-/.well-known/spritz-acp}"

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
