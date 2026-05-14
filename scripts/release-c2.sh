#!/usr/bin/env bash
set -euo pipefail

# release-c2.sh — build, tag, publish GitHub release, and update Homebrew formula.
# Usage: scripts/release-c2.sh <version>
# Example: scripts/release-c2.sh 0.8.10

REPO="jrniemiec/c2"
HOMEBREW_FORMULA="$HOME/dev/homebrew-c2/Formula/c2.rb"

# --- Validate args -----------------------------------------------------------

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version>   (e.g. 0.8.10)" >&2
  exit 1
fi

VERSION="$1"
TAG="v${VERSION}"
DIST_NAME="c2-${TAG}-darwin-arm64"
TARBALL="dist/${DIST_NAME}.tar.gz"

# --- Preflight checks --------------------------------------------------------

if [[ -n "$(git status --porcelain)" ]]; then
  echo "error: working tree is not clean — commit or stash changes first" >&2
  exit 1
fi

if ! command -v gh &>/dev/null; then
  echo "error: gh CLI not found" >&2
  exit 1
fi

if [[ ! -f "$HOMEBREW_FORMULA" ]]; then
  echo "error: formula not found: $HOMEBREW_FORMULA" >&2
  exit 1
fi

if git tag | grep -qx "$TAG"; then
  echo "error: tag $TAG already exists" >&2
  exit 1
fi

echo "==> Releasing c2 ${TAG}"

# --- Build dist tarball ------------------------------------------------------

echo "==> Building dist..."
make dist VERSION="$VERSION"

if [[ ! -f "$TARBALL" ]]; then
  echo "error: expected tarball not found: $TARBALL" >&2
  exit 1
fi

# --- Tag and push ------------------------------------------------------------

echo "==> Tagging ${TAG}..."
git tag "$TAG"
git push origin "$TAG"

# --- Create and publish GitHub release ---------------------------------------

echo "==> Creating GitHub release ${TAG}..."
gh release create "$TAG" "$TARBALL" \
  --repo "$REPO" \
  --title "$TAG" \
  --generate-notes

# --- Compute SHA256 ----------------------------------------------------------

echo "==> Computing SHA256..."
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${DIST_NAME}.tar.gz"
SHA256=$(curl -sL "$DOWNLOAD_URL" | shasum -a 256 | awk '{print $1}')
echo "    SHA256: $SHA256"

# --- Update Homebrew formula -------------------------------------------------

echo "==> Updating Homebrew formula..."
sed -i '' "s/version \".*\"/version \"${VERSION}\"/" "$HOMEBREW_FORMULA"
sed -i '' "s/sha256 \".*\"/sha256 \"${SHA256}\"/" "$HOMEBREW_FORMULA"

cd "$(dirname "$HOMEBREW_FORMULA")/.."
git add Formula/c2.rb
git commit -m "c2 ${VERSION}"
git push

echo "==> Done. c2 ${TAG} released."
