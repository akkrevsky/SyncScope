#!/usr/bin/env bash
set -euo pipefail

VERSION="v2.1.7"
SHA256="93c3b8244422ecbdb05fda5b5cc644bf1b35c6462b739e2abad678e191cd5049"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ASSET="strelaysrv-linux-amd64-${VERSION}.tar.gz" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

ROOT="$(cd "$(dirname "$0")" && pwd)"
DEST="${ROOT}/relay-bin"
mkdir -p "$DEST"
if [[ -x "${DEST}/strelaysrv" ]]; then
  echo "strelaysrv already present: ${DEST}/strelaysrv"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
URL="https://github.com/syncthing/relaysrv/releases/download/${VERSION}/${ASSET}"
echo "downloading ${URL}"
curl -fsSL -o "${TMP}/relay.tgz" "$URL"
echo "${SHA256}  ${TMP}/relay.tgz" | sha256sum -c -
tar -xzf "${TMP}/relay.tgz" -C "$TMP"
cp "${TMP}/strelaysrv-linux-amd64-${VERSION}/strelaysrv" "${DEST}/strelaysrv"
chmod +x "${DEST}/strelaysrv"
"${DEST}/strelaysrv" --version
