#!/usr/bin/env bash
set -euo pipefail

# release-c2.sh — build, tag, publish GitHub release, and update Homebrew formula.
# Usage: scripts/release-c2.sh <version>
# Example: scripts/release-c2.sh 0.8.10

REPO="jrniemiec/c2"
HOMEBREW_FORMULA="$HOME/dev/homebrew-c2/Formula/c2.rb"

# --- Helpers -----------------------------------------------------------------

confirm() {
  local msg="$1"
  echo
  echo "==> $msg"
  read -r -p "    Proceed? [Y/n] " ans
  case "$ans" in
    ""|y|Y) return 0 ;;
    *) echo "Aborted." >&2; exit 1 ;;
  esac
}

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

# --- Stage 1: Build dist tarball ---------------------------------------------

confirm "Stage 1/6 — Build dist tarball: run 'make dist VERSION=${VERSION}' → ${TARBALL}"
make dist VERSION="$VERSION"

if [[ ! -f "$TARBALL" ]]; then
  echo "error: expected tarball not found: $TARBALL" >&2
  exit 1
fi
echo "    OK: $(du -sh "$TARBALL" | cut -f1) written to $TARBALL"

# --- Stage 2: Tag and push ---------------------------------------------------

confirm "Stage 2/6 — Tag and push: create git tag ${TAG} and push to origin"
git tag "$TAG"
git push origin "$TAG"
echo "    OK: tag ${TAG} pushed"

# --- Stage 3: Create GitHub release ------------------------------------------

confirm "Stage 3/6 — Create GitHub release: publish ${TAG} and upload tarball"
gh release create "$TAG" "$TARBALL" \
  --repo "$REPO" \
  --title "$TAG" \
  --generate-notes
echo "    OK: release ${TAG} published"

# --- Stage 4: Compute SHA256 -------------------------------------------------

confirm "Stage 4/6 — Compute SHA256: download tarball from GitHub and hash it"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${DIST_NAME}.tar.gz"
SHA256=$(curl -sL "$DOWNLOAD_URL" | shasum -a 256 | awk '{print $1}')
echo "    SHA256: $SHA256"

# --- Stage 5: Update Homebrew formula ----------------------------------------

confirm "Stage 5/6 — Update Homebrew formula: patch version=${VERSION} sha256=${SHA256} in ${HOMEBREW_FORMULA}"
sed -i '' "s/version \".*\"/version \"${VERSION}\"/" "$HOMEBREW_FORMULA"
sed -i '' "s/sha256 \".*\"/sha256 \"${SHA256}\"/" "$HOMEBREW_FORMULA"
echo "    OK: formula patched"

# --- Stage 6: Commit and push Homebrew tap -----------------------------------

confirm "Stage 6/6 — Commit and push Homebrew tap: git commit + push in tap repo"
cd "$(dirname "$HOMEBREW_FORMULA")/.."
git add Formula/c2.rb
git commit -m "c2 ${VERSION}"
git push
echo "    OK: tap updated"

echo
echo "==> Done. c2 ${TAG} released."
