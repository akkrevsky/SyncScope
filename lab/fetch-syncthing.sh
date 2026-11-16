#!/usr/bin/env bash
set -euo pipefail

# Pinned for the lab. Update both the version and the checksum together.
VERSION="v2.1.7"
SHA256="2edc416e7ed136b7c858ed5f3f02a3b8316589605e54c380228ceea7acbf0142"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ASSET="syncthing-linux-amd64-${VERSION}.tar.gz" ;;
  *) echo "unsupported arch: $ARCH (lab supports linux-amd64)" >&2; exit 1 ;;
esac

ROOT="$(cd "$(dirname "$0")" && pwd)"
DEST="${ROOT}/bin"
mkdir -p "$DEST"
if [[ -x "${DEST}/syncthing" ]]; then
  echo "syncthing already present: ${DEST}/syncthing"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
URL="https://github.com/syncthing/syncthing/releases/download/${VERSION}/${ASSET}"
echo "downloading ${URL}"
curl -fsSL -o "${TMP}/st.tgz" "$URL"
echo "${SHA256}  ${TMP}/st.tgz" | sha256sum -c -
tar -xzf "${TMP}/st.tgz" -C "$TMP"
cp "${TMP}/syncthing-linux-amd64-${VERSION}/syncthing" "${DEST}/syncthing"
chmod +x "${DEST}/syncthing"
"${DEST}/syncthing" --version
