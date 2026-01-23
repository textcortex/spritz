#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
spritz_root="$(cd "${script_dir}/.." && pwd)"

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmp_dir}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

tar -C "$(dirname "${spritz_root}")" -cf - "$(basename "${spritz_root}")" | tar -C "${tmp_dir}" -xf -
isolated_root="${tmp_dir}/$(basename "${spritz_root}")"
export GOCACHE="${tmp_dir}/gocache"
export GOMODCACHE="${tmp_dir}/gomodcache"
mkdir -p "${GOCACHE}" "${GOMODCACHE}"

mapfile -t monorepo_patterns <<'EOF'
(^|[[:space:]"'])spritz/
(^|[[:space:]"'])/spritz/
\./spritz/
\.\./spritz/
\$\{[A-Za-z_][A-Za-z0-9_]*\}/spritz/
\$[A-Za-z_][A-Za-z0-9_]*/spritz/
\}/spritz/
EOF

if command -v rg >/dev/null 2>&1; then
  for pattern in "${monorepo_patterns[@]}"; do
    if rg -n "${pattern}" "${isolated_root}" --glob '!**/*.md' --glob '!**/verify-agnostic.sh'; then
      echo "ERROR: Found monorepo-specific path reference to spritz/ inside spritz/." >&2
      exit 1
    fi
  done
else
  for pattern in "${monorepo_patterns[@]}"; do
    if grep -R -n -E --exclude='*.md' --exclude='verify-agnostic.sh' -- "${pattern}" "${isolated_root}"; then
      echo "ERROR: Found monorepo-specific path reference to spritz/ inside spritz/." >&2
      exit 1
    fi
  done
fi

if command -v rg >/dev/null 2>&1; then
  if rg -n "\\.\\./\\.\\." "${isolated_root}" --glob 'Dockerfile' --glob 'go.mod'; then
    echo "ERROR: Found path that escapes spritz/ root (../..)." >&2
    exit 1
  fi
else
  if grep -R -n --include='Dockerfile' --include='go.mod' -- "\\.\\./\\.\\." "${isolated_root}"; then
    echo "ERROR: Found path that escapes spritz/ root (../..)." >&2
    exit 1
  fi
fi

(
  cd "${isolated_root}/operator"
  go test ./...
)

(
  cd "${isolated_root}/api"
  go test ./...
)

should_build_docker="false"
if [[ "${SPRITZ_AGNOSTIC_DOCKER:-}" == "1" || "${SPRITZ_AGNOSTIC_DOCKER:-}" == "true" || "${CI:-}" == "true" ]]; then
  should_build_docker="true"
fi

if [[ "${should_build_docker}" == "true" ]]; then
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    docker build -f "${isolated_root}/operator/Dockerfile" "${isolated_root}/operator" -t spritz-operator-agnostic:local
    docker build -f "${isolated_root}/api/Dockerfile" "${isolated_root}" -t spritz-api-agnostic:local
    docker build -f "${isolated_root}/ui/Dockerfile" "${isolated_root}/ui" -t spritz-ui-agnostic:local
  else
    echo "docker not available; skipping image builds" >&2
  fi
else
  echo "docker builds disabled (set SPRITZ_AGNOSTIC_DOCKER=true to enable)" >&2
fi
