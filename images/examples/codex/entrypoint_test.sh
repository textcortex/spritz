#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
entrypoint="${repo_root}/images/examples/codex/entrypoint.sh"

make_codex_stub() {
  local path="$1"
  cat >"${path}" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "login" && "${2:-}" == "--with-api-key" ]]; then
  cat >"${CODEX_LOGIN_CAPTURE:?}"
  exit 0
fi
printf 'unexpected codex invocation: %s\n' "$*" >&2
exit 1
SH
  chmod +x "${path}"
}

make_main_stub() {
  local path="$1"
  cat >"${path}" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >"${MAIN_CAPTURE:?}"
SH
  chmod +x "${path}"
}

test_entrypoint_seeds_codex_login_from_openai_api_key() {
  local test_dir
  test_dir="$(mktemp -d)"
  trap 'rm -rf "${test_dir}"' RETURN

  mkdir -p "${test_dir}/bin" "${test_dir}/home"
  make_codex_stub "${test_dir}/bin/codex"
  make_main_stub "${test_dir}/bin/main-entrypoint"

  HOME="${test_dir}/home" \
  PATH="${test_dir}/bin:${PATH}" \
  OPENAI_API_KEY="example-openai-key" \
  CODEX_LOGIN_CAPTURE="${test_dir}/codex-login.txt" \
  MAIN_CAPTURE="${test_dir}/main.txt" \
  SPRITZ_CODEX_ACP_ENABLED="false" \
  SPRITZ_CODEX_MAIN_ENTRYPOINT="${test_dir}/bin/main-entrypoint" \
  SPRITZ_CODEX_BIN="${test_dir}/bin/codex" \
  bash "${entrypoint}" "sleep" "infinity"

  [[ "$(cat "${test_dir}/codex-login.txt")" == "example-openai-key" ]]
  [[ "$(cat "${test_dir}/main.txt")" == "sleep infinity" ]]
}

test_entrypoint_seeds_codex_login_from_openai_api_key
