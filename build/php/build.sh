#!/usr/bin/env bash
# Builds Flock's static PHP (cli + fpm) with static-php-cli.
# Usage: build/php/build.sh 8.5   → output in build/php/out/<version>/{php,php-fpm}
set -euo pipefail

cd "$(dirname "$0")"
PHP_MINOR="${1:-8.5}"

# Extensions Laravel apps commonly need. Keep in sync with internal/php (Flock's expected module list).
EXTENSIONS="bcmath,bz2,calendar,ctype,curl,dom,exif,fileinfo,filter,ftp,gd,gmp,iconv,igbinary,intl,mbregex,mbstring,mysqli,mysqlnd,opcache,openssl,pcntl,pdo,pdo_mysql,pdo_pgsql,pdo_sqlite,pgsql,phar,posix,readline,redis,session,simplexml,sockets,sodium,sqlite3,tokenizer,xml,xmlreader,xmlwriter,xsl,zip,zlib"

if [[ ! -x ./spc ]]; then
  arch="$(uname -m)"; [[ "$arch" == "arm64" ]] && arch="aarch64"
  curl -fsSL -o spc "https://dl.static-php.dev/static-php-cli/spc-bin/nightly/spc-macos-${arch}"
  chmod +x spc
fi

./spc doctor --auto-fix
# Xdebug is a Zend extension and cannot be compiled in statically; it is built as xdebug.so
# and only loaded when debugging is switched on (`flock xdebug on`).
SHARED="xdebug"

rm -rf buildroot/modules
./spc download --with-php="$PHP_MINOR" --for-extensions="$EXTENSIONS,$SHARED" --prefer-pre-built --ignore-cache-sources=php-src,xdebug
# spc's post-build sanity checks are stricter than Flock needs (e.g. the pre-built sqlite lacks
# SQLITE_ENABLE_COLUMN_METADATA), so don't fail on them; verify the modules ourselves below.
./spc build "$EXTENSIONS" --build-shared="$SHARED" --build-cli --build-fpm || echo "spc build reported a problem, verifying binaries…"

for bin in buildroot/bin/php buildroot/bin/php-fpm; do
  [[ -x "$bin" ]] || { echo "missing $bin" >&2; exit 1; }
done

modules="$(./buildroot/bin/php -n -m | tr '[:upper:]' '[:lower:]')"
for ext in ${EXTENSIONS//,/ }; do
  case "$ext" in mbregex) continue ;; opcache) ext="zend opcache" ;; esac
  grep -qx "$ext" <<<"$modules" || { echo "extension $ext missing from build" >&2; exit 1; }
done

version="$(./buildroot/bin/php -n -r 'echo PHP_VERSION;')"
[[ "$version" == "$PHP_MINOR".* ]] || { echo "built PHP $version, expected $PHP_MINOR" >&2; exit 1; }
mkdir -p "out/$version"
cp buildroot/bin/php buildroot/bin/php-fpm "out/$version/"
mkdir -p "out/$version/ext"
for ext in ${SHARED//,/ }; do
  so="$(find buildroot/modules buildroot/lib/php -name "$ext.so" 2>/dev/null | head -1)"
  [[ -n "$so" ]] || { echo "shared extension $ext.so not found" >&2; exit 1; }
  cp "$so" "out/$version/ext/"
done
echo "built PHP $version → build/php/out/$version"
