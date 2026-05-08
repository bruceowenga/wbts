#!/usr/bin/env bash
# Update PKGBUILD and .SRCINFO for a new wbts release.
# Usage: ./update-version.sh 0.3.0
set -euo pipefail

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    echo "Usage: $0 <version>  (e.g. $0 0.3.0)" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Fetch the checksums file from the GitHub release
CHECKSUMS_URL="https://github.com/bruceowenga/wbts/releases/download/v${VERSION}/checksums.txt"
echo "Fetching checksums for v${VERSION}..."
CHECKSUMS="$(curl -fsSL "$CHECKSUMS_URL")"

AMD64_SHA="$(echo "$CHECKSUMS" | grep "linux_amd64.tar.gz" | awk '{print $1}')"
ARM64_SHA="$(echo "$CHECKSUMS" | grep "linux_arm64.tar.gz" | awk '{print $1}')"

if [ -z "$AMD64_SHA" ] || [ -z "$ARM64_SHA" ]; then
    echo "error: could not find tar.gz checksums in release. Is v${VERSION} fully released?" >&2
    exit 1
fi

echo "  amd64: $AMD64_SHA"
echo "  arm64: $ARM64_SHA"

# Update PKGBUILD
sed -i \
    -e "s/^pkgver=.*/pkgver=${VERSION}/" \
    -e "s/^pkgrel=.*/pkgrel=1/" \
    -e "s/sha256sums_x86_64=('[^']*')/sha256sums_x86_64=('${AMD64_SHA}')/" \
    -e "s/sha256sums_aarch64=('[^']*')/sha256sums_aarch64=('${ARM64_SHA}')/" \
    "${SCRIPT_DIR}/PKGBUILD"

# Regenerate .SRCINFO
# Requires makepkg (run on an Arch system or in a container)
if command -v makepkg &>/dev/null; then
    (cd "$SCRIPT_DIR" && makepkg --printsrcinfo > .SRCINFO)
    echo "Regenerated .SRCINFO via makepkg"
else
    # Manually update .SRCINFO if makepkg is not available
    sed -i \
        -e "s/pkgver = .*/pkgver = ${VERSION}/" \
        -e "s/pkgrel = .*/pkgrel = 1/" \
        -e "s|wbts-bin-[0-9.]*-x86_64|wbts-bin-${VERSION}-x86_64|g" \
        -e "s|wbts-bin-[0-9.]*-aarch64|wbts-bin-${VERSION}-aarch64|g" \
        -e "s|/v[0-9.]*/wbts_[0-9.]*_linux_amd64|/v${VERSION}/wbts_${VERSION}_linux_amd64|g" \
        -e "s|/v[0-9.]*/wbts_[0-9.]*_linux_arm64|/v${VERSION}/wbts_${VERSION}_linux_arm64|g" \
        -e "s/sha256sums_x86_64 = .*/sha256sums_x86_64 = ${AMD64_SHA}/" \
        -e "s/sha256sums_aarch64 = .*/sha256sums_aarch64 = ${ARM64_SHA}/" \
        "${SCRIPT_DIR}/.SRCINFO"
    echo "Updated .SRCINFO manually (makepkg not available)"
    echo "NOTE: run 'makepkg --printsrcinfo > .SRCINFO' on an Arch system to verify"
fi

echo "Done. Diff:"
git -C "$SCRIPT_DIR" diff --stat 2>/dev/null || true
