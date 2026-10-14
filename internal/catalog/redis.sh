#!/usr/bin/env bash
# Builds Redis for Flock from a verified source tarball.
# Usage: redis.sh <tarball> <version> <out-dir>  → <out-dir>/{redis-server,redis-cli}
# Builds src/ only: since 8.x the top-level make also builds modules that need Rust. Links only
# against macOS system libraries (TLS off: Flock binds to localhost only).
set -euo pipefail

TARBALL="$1"
VERSION="$2"
OUT="$3"
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"
export PATH="/usr/bin:/bin:/usr/sbin:/sbin"

echo "==> Unpacking Redis $VERSION"
rm -rf src && mkdir src
tar xzf "$TARBALL" -C src --strip-components 1

echo "==> Compiling Redis $VERSION"
make -C src/src -j"$(sysctl -n hw.ncpu)" BUILD_TLS=no USE_SYSTEMD=no MALLOC=libc

rm -rf "$OUT" && mkdir -p "$OUT"
cp src/src/redis-server src/src/redis-cli "$OUT/"
"$OUT/redis-server" --version
echo "==> Built Redis $VERSION"
