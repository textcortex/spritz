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

  cat > "${test_dir}/bin/bridge" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "${SPRITZ_OPENCLAW_ACP_GATEWAY_URL}" > "${TEST_GATEWAY_URL_FILE}"
exit 0
EOF
  chmod +x "${test_dir}/bin/bridge"

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
    export SPRITZ_OPENCLAW_BRIDGE_BIN="${test_dir}/bin/bridge"
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

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

run_case "${tmpdir}/pod-ip-default" "10.244.3.160 2001:db8:1:3::e3ab" "ws://10.244.3.160:8080"
run_case "${tmpdir}/explicit-override" "10.244.3.160 2001:db8:1:3::e3ab" "ws://bridge.example.internal:9000" "ws://bridge.example.internal:9000"

printf 'entrypoint ACP gateway URL tests passed\n'
