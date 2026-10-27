#!/usr/bin/env bash
# Builds the flockphp .deb and .rpm for one architecture into build/linux/: the CLI and the app.
# Usage: linux/build-packages.sh [amd64|arm64]   (default: amd64)
#   VERSION   the version to stamp (default: git describe)
#   REPO_KEY  the public key of Flock's package repository (default: linux/packaging/flockphp.asc)
# The app links GTK, so it is built on that architecture (needs Rust and libgtk-4-dev,
# libadwaita-1-dev, libwebkitgtk-6.0-dev).
set -euo pipefail
cd "$(dirname "$0")/.."
ARCH="${1:-amd64}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)}"
VERSION="${VERSION#v}"
NFPM=github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0
OUT=build/linux

mkdir -p "$OUT"
REPO_KEY="${REPO_KEY:-linux/packaging/flockphp.asc}"
[ -f "$REPO_KEY" ] || { echo "No repository key at $REPO_KEY." >&2; exit 1; }
cp "$REPO_KEY" "$OUT/flockphp.asc"
# nfpm.yaml packages build/linux/flock, so one architecture at a time.
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X flock/internal/cli.Version=$VERSION" -o "$OUT/flock" ./cmd/flock

case "$(uname -m)" in
  x86_64) HOST=amd64 ;;
  aarch64 | arm64) HOST=arm64 ;;
  *) HOST="$(uname -m)" ;;
esac
if [ "$(uname -s)" != Linux ] || [ "$HOST" != "$ARCH" ]; then
  echo "The app is built natively: run this on Linux $ARCH." >&2
  exit 1
fi
FLOCK_VERSION="$VERSION" cargo build --release --locked --manifest-path linux/Cargo.toml
cp linux/target/release/flock-app "$OUT/flock-app"

# A package version starts with a digit: a build that isn't a release (dev-d635400, git describe's
# hash) becomes a pre-release of 0.0.0 (0.0.0~dev.d635400), lower than any release.
PKG_VERSION="$VERSION"
case "$PKG_VERSION" in [0-9]*) ;; *) PKG_VERSION="0.0.0-${PKG_VERSION//-/.}" ;; esac
export NFPM_ARCH="$ARCH" PKG_VERSION
# Named after the release (1.2.0-beta.1), not the package version (1.2.0~beta.1): GitHub renames
# release assets with characters like ~. The repository's index points at these names.
case "$ARCH" in amd64) RPM_ARCH=x86_64 ;; arm64) RPM_ARCH=aarch64 ;; *) RPM_ARCH="$ARCH" ;; esac
go run "$NFPM" package --config linux/packaging/nfpm.yaml --packager deb --target "$OUT/flockphp_${VERSION}_${ARCH}.deb"
go run "$NFPM" package --config linux/packaging/nfpm.yaml --packager rpm --target "$OUT/flockphp-${VERSION}.${RPM_ARCH}.rpm"
ls -l "$OUT"/flockphp*
