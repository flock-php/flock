#!/usr/bin/env bash
# Builds the PostgreSQL client tools (psql, pg_dump, pg_restore, ...) that zonky's server builds
# do not include, from a verified source tarball. libpq is linked in via @rpath so the tools are
# relocatable.
# Usage: postgres-client.sh <tarball> <version> <out-dir>  → <out-dir>/{bin,lib}
# Runs in its own work directory, which must not contain spaces (configure can't handle them).
set -euo pipefail

TARBALL="$1"
VERSION="$2"
OUT="$3"
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"
PREFIX="$PWD/stage"
JOBS="$(sysctl -n hw.ncpu)"

echo "==> Unpacking PostgreSQL $VERSION"
rm -rf src stage && mkdir src
tar xjf "$TARBALL" -C src --strip-components 1

echo "==> Configuring PostgreSQL $VERSION"
cd src
# libedit ships with macOS and gives psql line editing; ICU/OpenSSL are not needed for local clients.
./configure --prefix="$PREFIX" --without-icu --without-openssl --with-libedit-preferred \
  LDFLAGS="-Wl,-rpath,@loader_path/../lib"

echo "==> Compiling libpq"
make -j"$JOBS" -C src/interfaces/libpq install
echo "==> Compiling psql"
make -j"$JOBS" -C src/bin/psql install
echo "==> Compiling pg_dump, pg_restore and pg_dumpall"
make -j"$JOBS" -C src/bin/pg_dump install
echo "==> Compiling createdb and dropdb"
make -j"$JOBS" -C src/bin/scripts install
cd ..

rm -rf "$OUT" && mkdir -p "$OUT/bin" "$OUT/lib"
for tool in psql pg_dump pg_dumpall pg_restore createdb dropdb; do
  cp "$PREFIX/bin/$tool" "$OUT/bin/"
done
cp -a "$PREFIX"/lib/libpq*.dylib "$OUT/lib/"

# The tools reference libpq by its absolute build path; point them at @rpath (= ../lib) instead.
# Editing load commands invalidates the signature, so re-sign ad hoc (required on Apple silicon).
install_name_tool -id @rpath/libpq.5.dylib "$OUT/lib/libpq.5.dylib"
for tool in "$OUT"/bin/*; do
  install_name_tool -change "$PREFIX/lib/libpq.5.dylib" @rpath/libpq.5.dylib "$tool"
done
codesign --force --sign - "$OUT/lib/libpq.5.dylib" "$OUT"/bin/*

"$OUT/bin/psql" --version
echo "==> Built the PostgreSQL $VERSION client tools"
