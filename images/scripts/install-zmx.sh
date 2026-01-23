#!/usr/bin/env bash
set -euo pipefail

ZMX_VERSION="${ZMX_VERSION:-0.1.1}"
ZMX_SHA256="${ZMX_SHA256:-}"
ZMX_BASE_URL="${ZMX_BASE_URL:-https://zmx.sh/a}"
ARCH="${TARGETARCH:-}"

case "${ARCH}" in
  amd64)
    ARCH="x86_64"
    ;;
  arm64)
    ARCH="aarch64"
    ;;
esac

if [[ -z "${ARCH}" ]]; then
  arch_raw="$(uname -m)"
  case "${arch_raw}" in
    x86_64|aarch64)
      ARCH="${arch_raw}"
      ;;
    arm64)
      ARCH="aarch64"
      ;;
    *)
      echo "Unsupported architecture: ${arch_raw}" >&2
      exit 1
      ;;
  esac
fi

archive="zmx-${ZMX_VERSION}-linux-${ARCH}.tar.gz"
url="${ZMX_BASE_URL}/${archive}"

curl -fsSL "${url}" -o /tmp/zmx.tgz
if [[ -n "${ZMX_SHA256}" ]]; then
  echo "${ZMX_SHA256}  /tmp/zmx.tgz" | sha256sum -c -
fi

tar -xzf /tmp/zmx.tgz -C /usr/local/bin zmx
chmod +x /usr/local/bin/zmx
rm -f /tmp/zmx.tgz
