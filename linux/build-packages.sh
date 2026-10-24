#!/usr/bin/env bash
# Builds the flockphp .deb and .rpm for one architecture into build/linux/: the CLI and the app.
# Usage: linux/build-packages.sh [amd64|arm64]   (default: amd64)
#   VERSION  the version to stamp (default: git describe)
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

export NFPM_ARCH="$ARCH" VERSION
for packager in deb rpm; do
  go run "$NFPM" package --config linux/packaging/nfpm.yaml --packager "$packager" --target "$OUT/"
done
ls -l "$OUT"/flockphp*
