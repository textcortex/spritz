#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${SPRITZ_E2E_CLUSTER:-spritz-e2e}"
KUBECONFIG_PATH="${SPRITZ_E2E_KUBECONFIG:-${TMPDIR:-/tmp}/spritz-e2e.kubeconfig}"
API_PORT="${SPRITZ_E2E_API_PORT:-8090}"
API_WAIT_SECONDS="${SPRITZ_E2E_API_WAIT_SECONDS:-90}"
KEEP_CLUSTER="${SPRITZ_E2E_KEEP_CLUSTER:-false}"
SPRITZ_NAME="${SPRITZ_E2E_NAME:-spritz-e2e}"
SSH_MODE="${SPRITZ_E2E_SSH_MODE:-}"
SSH_GATEWAY_SERVICE="${SPRITZ_E2E_SSH_GATEWAY_SERVICE:-spritz-ssh-gateway}"
SSH_GATEWAY_NAMESPACE="${SPRITZ_E2E_SSH_GATEWAY_NAMESPACE:-spritz-system}"

GOCACHE="${TMPDIR:-/tmp}/spritz-gocache"
GOMODCACHE="${TMPDIR:-/tmp}/spritz-gomodcache"

LOG_DIR="${TMPDIR:-/tmp}/spritz-e2e-logs"
mkdir -p "${LOG_DIR}"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require kubectl
require go
require jq

if command -v kind >/dev/null 2>&1; then
  KIND_BIN="$(command -v kind)"
else
  OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
  esac
  KIND_BIN="${TMPDIR:-/tmp}/kind"
  echo "kind not found; downloading to ${KIND_BIN}"
  curl -sSL -o "${KIND_BIN}" "https://kind.sigs.k8s.io/dl/v0.22.0/kind-${OS}-${ARCH}"
  chmod +x "${KIND_BIN}"
fi

find_free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("", 0))
print(s.getsockname()[1])
s.close()
PY
}

if command -v lsof >/dev/null 2>&1; then
  if lsof -nP -iTCP:"${API_PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
    API_PORT="$(find_free_port)"
    echo "port ${SPRITZ_E2E_API_PORT:-8090} in use; switched to ${API_PORT}"
  fi
fi

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then
    kill "${API_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${OP_PID:-}" ]]; then
    kill "${OP_PID}" >/dev/null 2>&1 || true
  fi

  if [[ "${KEEP_CLUSTER}" == "true" ]]; then
    echo "keeping cluster ${CLUSTER_NAME}"
    return
  fi

  KUBECONFIG="${KUBECONFIG_PATH}" "${KIND_BIN}" delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
}

trap cleanup EXIT

export KUBECONFIG="${KUBECONFIG_PATH}"

if "${KIND_BIN}" get clusters | grep -q "^${CLUSTER_NAME}$"; then
  echo "cluster ${CLUSTER_NAME} already exists"
  "${KIND_BIN}" export kubeconfig --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG_PATH}"
else
  "${KIND_BIN}" create cluster --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG_PATH}"
fi

kubectl wait --for=condition=Ready node --all --timeout=120s

kubectl get namespace spritz-system >/dev/null 2>&1 || kubectl create namespace spritz-system
kubectl get namespace spritz >/dev/null 2>&1 || kubectl create namespace spritz

kubectl apply -f "${ROOT_DIR}/helm/spritz/crds/spritz.sh_spritzes.yaml"

echo "warming go module cache..."
(
  cd "${ROOT_DIR}/operator"
  GOCACHE="${GOCACHE}" GOMODCACHE="${GOMODCACHE}" go mod download
) >/dev/null 2>&1 || true

(
  cd "${ROOT_DIR}/api"
  GOCACHE="${GOCACHE}" GOMODCACHE="${GOMODCACHE}" go mod download
) >/dev/null 2>&1 || true

(
  cd "${ROOT_DIR}/operator"
  SPRITZ_OPERATOR_METRICS_ADDR=0 SPRITZ_OPERATOR_HEALTH_ADDR=0 \
    GOCACHE="${GOCACHE}" GOMODCACHE="${GOMODCACHE}" go run ./main.go
) > "${LOG_DIR}/operator.log" 2>&1 &
OP_PID=$!

(
  cd "${ROOT_DIR}/api"
  SPRITZ_NAMESPACE=spritz SPRITZ_AUTH_MODE=none PORT="${API_PORT}" \
    GOCACHE="${GOCACHE}" GOMODCACHE="${GOMODCACHE}" go run .
) > "${LOG_DIR}/api.log" 2>&1 &
API_PID=$!

echo "waiting for API on port ${API_PORT}..."
for _ in $(seq 1 "${API_WAIT_SECONDS}"); do
  status="$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost:${API_PORT}/healthz" || true)"
  if [[ "${status}" == "200" ]]; then
    break
  fi
  sleep 1
done

status="$(curl -sS -o /dev/null -w '%{http_code}' "http://localhost:${API_PORT}/healthz" || true)"
if [[ "${status}" != "200" ]]; then
  echo "API did not become ready in ${API_WAIT_SECONDS}s"
  echo "operator log:"
  tail -n 200 "${LOG_DIR}/operator.log" || true
  echo "api log:"
  tail -n 200 "${LOG_DIR}/api.log" || true
  exit 1
fi

cat <<EOF > "${LOG_DIR}/create.json"
{
  "name": "${SPRITZ_NAME}",
  "spec": {
    "image": "nginx:alpine",
    "owner": { "id": "e2e" },
    "ports": [
      { "name": "http", "containerPort": 80, "servicePort": 80 }
    ]
  }
}
EOF

if [[ -n "${SSH_MODE}" ]]; then
  cat <<EOF > "${LOG_DIR}/ssh.json"
{
  "ssh": {
    "enabled": true,
    "mode": "${SSH_MODE}",
    "gatewayService": "${SSH_GATEWAY_SERVICE}",
    "gatewayNamespace": "${SSH_GATEWAY_NAMESPACE}"
  }
}
EOF
  jq -s '.[0] * {"spec": (.[0].spec * .[1])}' "${LOG_DIR}/create.json" "${LOG_DIR}/ssh.json" > "${LOG_DIR}/create.merged.json"
  mv "${LOG_DIR}/create.merged.json" "${LOG_DIR}/create.json"
fi

curl -sS --fail -X POST "http://localhost:${API_PORT}/spritzes" \
  -H 'Content-Type: application/json' \
  --data "@${LOG_DIR}/create.json" >/dev/null

echo "waiting for spritz to become Ready..."
for _ in {1..30}; do
  if curl -sS --fail "http://localhost:${API_PORT}/spritzes/${SPRITZ_NAME}" | grep -q '"phase":"Ready"'; then
    echo "spritz is Ready"
    break
  fi
  sleep 2
done

kubectl get deployment,service -n spritz -l spritz.sh/name="${SPRITZ_NAME}"

if [[ -n "${SSH_MODE}" ]]; then
  echo "ssh info:"
  curl -sS --fail "http://localhost:${API_PORT}/spritzes/${SPRITZ_NAME}" | jq '.status.ssh'
fi

curl -sS -X DELETE "http://localhost:${API_PORT}/spritzes/${SPRITZ_NAME}" -o /dev/null -w "deleted (%{http_code})\n"

echo "done (logs in ${LOG_DIR})"
