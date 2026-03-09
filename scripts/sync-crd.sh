#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="${ROOT_DIR}/crd"
HELM_DIR="${ROOT_DIR}/helm/spritz/crds"

if [[ "${1:-}" == "--check" ]]; then
  status=0
  while IFS= read -r source; do
    target="${HELM_DIR}/$(basename "${source}")"
    if [[ ! -f "${target}" ]] || ! diff -u "${source}" "${target}" >/dev/null; then
      echo "ERROR: Spritz CRD copy is out of sync for $(basename "${source}")." >&2
      echo "Run: ./scripts/sync-crd.sh" >&2
      diff -u "${source}" "${target}" || true
      status=1
    fi
  done < <(find "${SOURCE_DIR}" -maxdepth 1 -type f -name '*.yaml' | sort)
  if [[ "${status}" -ne 0 ]]; then
    exit "${status}"
  fi
  echo "Spritz CRD copies are in sync."
  exit 0
fi

mkdir -p "${HELM_DIR}"
while IFS= read -r source; do
  target="${HELM_DIR}/$(basename "${source}")"
  cp "${source}" "${target}"
  echo "Synced Spritz CRD to ${target}"
done < <(find "${SOURCE_DIR}" -maxdepth 1 -type f -name '*.yaml' | sort)
