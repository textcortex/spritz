#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
entrypoint="${repo_root}/images/examples/openclaw/entrypoint.sh"

assert_eq() {
  local actual="$1"
  local expected="$2"
  local message="$3"
  if [[ "${actual}" != "${expected}" ]]; then
    printf 'assertion failed: %s\nexpected: %s\nactual:   %s\n' "${message}" "${expected}" "${actual}" >&2
    exit 1
  fi
}

make_openclaw_stub() {
  local path="$1"
  cat > "${path}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "config" && "${2:-}" == "get" && "${3:-}" == "gateway.auth.token" ]]; then
  exit 0
fi
if [[ "${1:-}" == "config" && "${2:-}" == "set" ]]; then
  exit 0
fi
printf 'unexpected openclaw invocation: %s\n' "$*" >&2
exit 1
EOF
  chmod +x "${path}"
}

run_case() {
  local test_dir="$1"
  local hostname_output="$2"
  local expected_url="$3"
  local explicit_url="${4:-}"

  mkdir -p "${test_dir}/bin" "${test_dir}/home"
  make_openclaw_stub "${test_dir}/bin/openclaw"

  cat > "${test_dir}/bin/hostname" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ "\${1:-}" == "-i" ]]; then
  printf '%s\n' '${hostname_output}'
  exit 0
fi
exec /usr/bin/hostname "\$@"
EOF
  chmod +x "${test_dir}/bin/hostname"

  cat > "${test_dir}/bin/acp-server" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "${SPRITZ_OPENCLAW_ACP_GATEWAY_URL}" > "${TEST_GATEWAY_URL_FILE}"
exit 0
EOF
  chmod +x "${test_dir}/bin/acp-server"

  cat > "${test_dir}/bin/main-entrypoint" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sleep 5 &
wait $!
EOF
  chmod +x "${test_dir}/bin/main-entrypoint"

  local output_file="${test_dir}/gateway-url.txt"
  (
    export PATH="${test_dir}/bin:${PATH}"
    export HOME="${test_dir}/home"
    export OPENCLAW_AUTO_START=false
    export TEST_GATEWAY_URL_FILE="${output_file}"
    export SPRITZ_OPENCLAW_SERVER_BIN="${test_dir}/bin/acp-server"
    export SPRITZ_OPENCLAW_MAIN_ENTRYPOINT="${test_dir}/bin/main-entrypoint"
    if [[ -n "${explicit_url}" ]]; then
      export SPRITZ_OPENCLAW_ACP_GATEWAY_URL="${explicit_url}"
    else
      unset SPRITZ_OPENCLAW_ACP_GATEWAY_URL || true
    fi
    bash "${entrypoint}" true
  )

  local actual
  actual="$(cat "${output_file}")"
  assert_eq "${actual}" "${expected_url}" "gateway URL should match"
}

run_auth_profile_case() {
  local test_dir="$1"

  mkdir -p "${test_dir}/bin" "${test_dir}/home"
  make_openclaw_stub "${test_dir}/bin/openclaw"

  cat > "${test_dir}/bin/acp-server" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sleep 1
EOF
  chmod +x "${test_dir}/bin/acp-server"

  cat > "${test_dir}/bin/main-entrypoint" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sleep 1
EOF
  chmod +x "${test_dir}/bin/main-entrypoint"

  (
    export PATH="${test_dir}/bin:${PATH}"
    export HOME="${test_dir}/home"
    export OPENCLAW_AUTO_START=false
    export ANTHROPIC_API_KEY="sk-test-anthropic"
    export SPRITZ_OPENCLAW_SERVER_BIN="${test_dir}/bin/acp-server"
    export SPRITZ_OPENCLAW_MAIN_ENTRYPOINT="${test_dir}/bin/main-entrypoint"
    bash "${entrypoint}" true
  )

  local auth_path="${test_dir}/home/.openclaw/agents/main/agent/auth-profiles.json"
  [[ -f "${auth_path}" ]] || {
    printf 'expected auth profile store at %s\n' "${auth_path}" >&2
    exit 1
  }

  grep -q '"anthropic:default"' "${auth_path}" || {
    printf 'expected anthropic default profile in %s\n' "${auth_path}" >&2
    exit 1
  }
  grep -q '"provider": "anthropic"' "${auth_path}" || {
    printf 'expected anthropic provider in %s\n' "${auth_path}" >&2
    exit 1
  }
  grep -q '"id": "ANTHROPIC_API_KEY"' "${auth_path}" || {
    printf 'expected ANTHROPIC_API_KEY ref in %s\n' "${auth_path}" >&2
    exit 1
  }
}

run_runtime_bridge_case() {
  local test_dir="$1"

  mkdir -p "${test_dir}/bin" "${test_dir}/home"
  make_openclaw_stub "${test_dir}/bin/openclaw"

  cat > "${test_dir}/bin/acp-server" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sleep 1
EOF
  chmod +x "${test_dir}/bin/acp-server"

  cat > "${test_dir}/bin/main-entrypoint" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sleep 1
EOF
  chmod +x "${test_dir}/bin/main-entrypoint"

  (
    export PATH="${test_dir}/bin:${PATH}"
    export HOME="${test_dir}/home"
    export OPENCLAW_AUTO_START=false
    export ANTHROPIC_API_KEY="tc-runtime-bridge"
    export ANTHROPIC_BASE_URL="http://127.0.0.1:8091"
    export SPRITZ_OPENCLAW_SERVER_BIN="${test_dir}/bin/acp-server"
    export SPRITZ_OPENCLAW_MAIN_ENTRYPOINT="${test_dir}/bin/main-entrypoint"
    bash "${entrypoint}" true
  )

  local auth_path="${test_dir}/home/.openclaw/agents/main/agent/auth-profiles.json"
  [[ ! -f "${auth_path}" ]] || {
    printf 'did not expect runtime bridge mode to seed auth profiles at %s\n' "${auth_path}" >&2
    exit 1
  }
}

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

run_case "${tmpdir}/pod-ip-default" "10.244.3.160 2001:db8:1:3::e3ab" "ws://127.0.0.1:8080"
run_case "${tmpdir}/explicit-override" "10.244.3.160 2001:db8:1:3::e3ab" "ws://bridge.example.internal:9000" "ws://bridge.example.internal:9000"
run_auth_profile_case "${tmpdir}/anthropic-auth-profile"
run_runtime_bridge_case "${tmpdir}/runtime-bridge-auth-profile-skip"

printf 'entrypoint ACP gateway URL tests passed\n'
