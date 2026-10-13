#!/usr/bin/env bash
# Builds Valkey (Redis-compatible) for Flock.
# Usage: build/valkey/build.sh [version]  → build/valkey/out/<version>/{valkey-server,valkey-cli}
# Only links against macOS system libraries (TLS off: Flock binds to localhost only), so the
# binaries run on any Mac with the same architecture and macOS ≥ the deployment target.
set -euo pipefail

cd "$(dirname "$0")"
VERSION="${1:-9.1.2}"
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"

mkdir -p src
if [[ ! -d "src/valkey-$VERSION" ]]; then
  curl -fsSL "https://github.com/valkey-io/valkey/archive/refs/tags/$VERSION.tar.gz" | tar xz -C src
fi

make -C "src/valkey-$VERSION" -j"$(sysctl -n hw.ncpu)" BUILD_TLS=no USE_SYSTEMD=no MALLOC=libc

mkdir -p "out/$VERSION"
cp "src/valkey-$VERSION/src/valkey-server" "src/valkey-$VERSION/src/valkey-cli" "out/$VERSION/"
"out/$VERSION/valkey-server" --version
echo "built Valkey $VERSION → build/valkey/out/$VERSION"
