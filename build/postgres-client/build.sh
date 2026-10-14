#!/usr/bin/env bash
# Builds the PostgreSQL client tools (psql, pg_dump, pg_restore, ...) that zonky's server
# builds do not include. libpq is linked in via @rpath so the tools are relocatable.
# Flock itself builds them with internal/components/postgres-client.sh (`flock postgres install-client`).
# Usage: build/postgres-client/build.sh [version] → build/postgres-client/out/<version>/{bin,lib}
set -euo pipefail

cd "$(dirname "$0")"
VERSION="${1:-18.6}"
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-13.0}"
PREFIX="$PWD/stage/$VERSION"

mkdir -p src
if [[ ! -d "src/postgresql-$VERSION" ]]; then
  curl -fsSL "https://ftp.postgresql.org/pub/source/v$VERSION/postgresql-$VERSION.tar.bz2" | tar xj -C src
fi

cd "src/postgresql-$VERSION"
# libedit ships with macOS and gives psql line editing; ICU/OpenSSL are not needed for local clients.
./configure --prefix="$PREFIX" --without-icu --without-openssl --with-libedit-preferred \
  LDFLAGS="-Wl,-rpath,@loader_path/../lib" > ../../configure.log
make -j"$(sysctl -n hw.ncpu)" -C src/interfaces/libpq install > ../../make.log
make -j"$(sysctl -n hw.ncpu)" -C src/bin/psql install >> ../../make.log
make -j"$(sysctl -n hw.ncpu)" -C src/bin/pg_dump install >> ../../make.log
make -j"$(sysctl -n hw.ncpu)" -C src/bin/scripts install >> ../../make.log
cd ../..

OUT="out/$VERSION"
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
echo "built PostgreSQL $VERSION client tools → build/postgres-client/$OUT"
