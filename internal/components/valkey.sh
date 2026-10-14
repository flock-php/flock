#!/usr/bin/env bash
# Builds Valkey (Redis-compatible) for Flock from a verified source tarball.
# Usage: valkey.sh <tarball> <version> <out-dir>  → <out-dir>/{valkey-server,valkey-cli}
# Runs in its own work directory. Only links against macOS system libraries (TLS off: Flock binds
# to localhost only), so the binaries run on this Mac and any other of the same architecture.
set -euo pipefail

TARBALL="$1"
VERSION="$2"
OUT="$3"
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"

echo "==> Unpacking Valkey $VERSION"
rm -rf src && mkdir src
tar xzf "$TARBALL" -C src --strip-components 1

echo "==> Compiling Valkey $VERSION"
make -C src -j"$(sysctl -n hw.ncpu)" BUILD_TLS=no USE_SYSTEMD=no MALLOC=libc

rm -rf "$OUT" && mkdir -p "$OUT"
cp src/src/valkey-server src/src/valkey-cli "$OUT/"
"$OUT/valkey-server" --version
echo "==> Built Valkey $VERSION"
