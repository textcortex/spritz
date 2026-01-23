#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_CRD="${ROOT_DIR}/crd/spritz.sh_spritzes.yaml"
HELM_CRD="${ROOT_DIR}/helm/spritz/crds/spritz.sh_spritzes.yaml"

if [[ ! -f "${SOURCE_CRD}" ]]; then
  echo "ERROR: Source CRD not found at ${SOURCE_CRD}" >&2
  exit 1
fi

if [[ "${1:-}" == "--check" ]]; then
  if ! diff -u "${SOURCE_CRD}" "${HELM_CRD}" >/dev/null; then
    echo "ERROR: Spritz CRD copy is out of sync." >&2
    echo "Run: ./scripts/sync-crd.sh" >&2
    diff -u "${SOURCE_CRD}" "${HELM_CRD}" || true
    exit 1
  fi
  echo "Spritz CRD copy is in sync."
  exit 0
fi

cp "${SOURCE_CRD}" "${HELM_CRD}"
echo "Synced Spritz CRD to ${HELM_CRD}"
