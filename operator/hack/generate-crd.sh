#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export XDG_CACHE_HOME="${XDG_CACHE_HOME:-/tmp}"

go run sigs.k8s.io/controller-tools/cmd/controller-gen \
  crd \
  paths="./api/v1" \
  output:crd:dir="../crd/generated"
