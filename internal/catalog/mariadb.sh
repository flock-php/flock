#!/usr/bin/env bash
# Builds MariaDB for Flock from a verified source tarball, with a static OpenSSL built first (the
# macOS SDK has none) and cmake from Kitware (not part of the Command Line Tools).
# Usage: CMAKE=<cmake> OPENSSL_TARBALL=<tarball> OPENSSL_PREFIX=<dir> mariadb.sh <tarball> <version> <out-dir>
# OPENSSL_PREFIX is reused when it already holds a build. Homebrew and /usr/local are ignored, so
# the result links only against macOS system libraries and runs on any Mac of this architecture.
set -euo pipefail

TARBALL="$1"
VERSION="$2"
OUT="$3"
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"
export PATH="$(dirname "$CMAKE"):/usr/bin:/bin:/usr/sbin:/sbin"
JOBS="$(sysctl -n hw.ncpu)"

if [[ ! -f "$OPENSSL_PREFIX/lib/libssl.a" ]]; then
  echo "==> Building OpenSSL"
  rm -rf openssl && mkdir openssl
  tar xzf "$OPENSSL_TARBALL" -C openssl --strip-components 1
  target=darwin64-arm64-cc
  [[ "$(uname -m)" == "x86_64" ]] && target=darwin64-x86_64-cc
  rm -rf "$OPENSSL_PREFIX.tmp"
  (cd openssl && ./Configure "$target" no-shared no-tests no-docs no-apps --prefix="$OPENSSL_PREFIX.tmp" --libdir=lib \
    && make -j"$JOBS" && make install_sw)
  rm -rf "$OPENSSL_PREFIX" && mv "$OPENSSL_PREFIX.tmp" "$OPENSSL_PREFIX"
  rm -rf openssl
fi

echo "==> Unpacking MariaDB $VERSION"
rm -rf src build && mkdir src build
tar xzf "$TARBALL" -C src --strip-components 1

echo "==> Configuring MariaDB $VERSION"
"$CMAKE" -S src -B build \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$OUT" \
  -DCMAKE_OSX_DEPLOYMENT_TARGET="$MACOSX_DEPLOYMENT_TARGET" \
  -DCMAKE_IGNORE_PREFIX_PATH="/opt/homebrew;/usr/local" \
  -DWITH_SSL="$OPENSSL_PREFIX" \
  -DWITH_PCRE=bundled \
  -DWITH_ZLIB=system \
  -DPLUGIN_TOKUDB=NO -DPLUGIN_ROCKSDB=NO -DPLUGIN_MROONGA=NO -DPLUGIN_SPIDER=NO -DPLUGIN_CONNECT=NO \
  -DPLUGIN_OQGRAPH=NO -DPLUGIN_SPHINX=NO -DPLUGIN_COLUMNSTORE=NO \
  -DWITH_UNIT_TESTS=OFF -DWITH_EMBEDDED_SERVER=OFF -DWITH_WSREP=OFF

echo "==> Compiling MariaDB $VERSION"
"$CMAKE" --build build -j "$JOBS"

echo "==> Installing MariaDB $VERSION"
rm -rf "$OUT"
"$CMAKE" --install build
rm -rf "$OUT/mysql-test" "$OUT/sql-bench" "$OUT/man" "$OUT/include" "$OUT/share/doc"
# Test, benchmark, backup and storage-engine repair tools (statically linked, ~9 MB each).
rm -f "$OUT"/bin/mariadb-test* "$OUT"/bin/mysqltest* "$OUT"/bin/mariadb-client-test* "$OUT"/bin/mysql_client_test* \
  "$OUT"/bin/mariadb-backup "$OUT"/bin/mariabackup "$OUT"/bin/mbstream "$OUT"/bin/mariadb-slap "$OUT"/bin/mysqlslap \
  "$OUT"/bin/aria_* "$OUT"/bin/myisam* "$OUT"/bin/mariadb-ldb "$OUT"/bin/sst_dump
find "$OUT/lib" -name '*.a' -delete
"$OUT/bin/mariadbd" --version
echo "==> Built MariaDB $VERSION"
