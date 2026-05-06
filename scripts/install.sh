#!/usr/bin/env bash
# wbts installer — downloads the latest release binary for your platform.
# Usage: curl -fsSL https://raw.githubusercontent.com/bruceowenga/wbts/main/scripts/install.sh | bash
set -euo pipefail

REPO="bruceowenga/wbts"
BINARY="wbts"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# --- platform detection ---

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  linux) ;;
  *) echo "error: only Linux is supported (got: $OS)" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64)          ARCH="amd64" ;;
  aarch64|arm64)   ARCH="arm64" ;;
  *) echo "error: unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# --- resolve latest version ---

VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"v\([^"]*\)".*/\1/')"

if [ -z "$VERSION" ]; then
  echo "error: could not determine latest version from GitHub API" >&2
  exit 1
fi

TARBALL="wbts_${VERSION}_linux_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${TARBALL}"

# --- download and install ---

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading wbts v${VERSION} for linux/${ARCH}..."
if ! curl -fsSL "$URL" -o "${TMP}/${TARBALL}"; then
  echo "error: download failed from $URL" >&2
  exit 1
fi

tar -xzf "${TMP}/${TARBALL}" -C "$TMP"

if [ ! -f "${TMP}/${BINARY}" ]; then
  echo "error: binary not found in archive" >&2
  exit 1
fi

if [ -w "$INSTALL_DIR" ]; then
  install -m 755 "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  echo "Installing to ${INSTALL_DIR}/${BINARY} (requires sudo)..."
  sudo install -m 755 "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

echo ""
echo "wbts v${VERSION} installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Quick start:"
echo "  wbts check-perms    # check which log sources are accessible"
echo "  wbts --since 2h     # show what happened in the last 2 hours"
echo "  wbts --since 2h --summary  # incident windows only"
