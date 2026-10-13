#!/usr/bin/env bash
# Builds a static PHP (cli + fpm, plus xdebug.so) for Flock with static-php-cli.
#
# Usage: build.sh <major.minor>
#   FLOCK_BUILD_DIR  workspace with spc, caches and out/ (default: this script's directory)
#   FLOCK_XDEBUG=0   skip Xdebug
#
# The last line of a successful build is "FLOCK_BUILT <version> <output dir>".
# Embedded in the flock binary (internal/phpbuild); build/php/build.sh runs it for development.
set -euo pipefail

PHP_MINOR="${1:?usage: build.sh <major.minor>}"
cd "${FLOCK_BUILD_DIR:-$(dirname "$0")}"

# Extensions Laravel apps commonly need. The build fails if any of them is missing.
EXTENSIONS="bcmath,bz2,calendar,ctype,curl,dom,exif,fileinfo,filter,ftp,gd,gmp,iconv,igbinary,intl,mbregex,mbstring,mysqli,mysqlnd,opcache,openssl,pcntl,pdo,pdo_mysql,pdo_pgsql,pdo_sqlite,pgsql,phar,posix,readline,redis,session,simplexml,sockets,sodium,sqlite3,tokenizer,xml,xmlreader,xmlwriter,xsl,zip,zlib"

# Xdebug is a Zend extension and cannot be compiled in statically; it is built as xdebug.so and
# only loaded when debugging is switched on. It is optional: new PHP versions often build before
# Xdebug supports them, and PHP is still useful without it.
SHARED="xdebug"
[[ "${FLOCK_XDEBUG:-1}" == "0" ]] && SHARED=""


echo "==> Preparing build tools"
arch="$(uname -m)"; [[ "$arch" == "arm64" ]] && arch="aarch64"
# Use the newest static-php-cli (refreshed daily): support for new PHP versions lands there first.
if [[ ! -x ./spc || -n "$(find ./spc -mtime +1 2>/dev/null)" ]]; then
  if curl -fsSL -o spc.new "https://dl.static-php.dev/static-php-cli/spc-bin/nightly/spc-macos-${arch}"; then
    chmod +x spc.new && mv spc.new spc
  elif [[ ! -x ./spc ]]; then
    echo "could not download static-php-cli" >&2; exit 1
  fi
fi
./spc doctor --auto-fix

build() {
  local shared="$1" with=""
  [[ -n "$shared" ]] && with=",$shared"

  echo "==> Downloading sources for PHP $PHP_MINOR"
  ./spc download --with-php="$PHP_MINOR" --for-extensions="$EXTENSIONS$with" --prefer-pre-built \
    --ignore-cache-sources="php-src${with}"

  echo "==> Compiling PHP $PHP_MINOR${shared:+ with $shared}"
  rm -rf buildroot/modules buildroot/bin/php buildroot/bin/php-fpm
  # spc's post-build sanity checks are stricter than Flock needs (e.g. the pre-built sqlite lacks
  # SQLITE_ENABLE_COLUMN_METADATA), so don't fail on them; the binaries are verified below.
  ./spc build "$EXTENSIONS" ${shared:+--build-shared="$shared"} --build-cli --build-fpm \
    || echo "spc build reported a problem, verifying binaries…"
  [[ -x buildroot/bin/php && -x buildroot/bin/php-fpm ]]
}

if ! build "$SHARED"; then
  [[ -z "$SHARED" ]] && { echo "build failed: php or php-fpm missing" >&2; exit 1; }
  echo "==> Build with $SHARED failed, retrying without it"
  SHARED=""
  build "" || { echo "build failed: php or php-fpm missing" >&2; exit 1; }
fi

echo "==> Verifying extensions"
modules="$(./buildroot/bin/php -n -m | tr '[:upper:]' '[:lower:]')"
for ext in ${EXTENSIONS//,/ }; do
  case "$ext" in mbregex) continue ;; opcache) ext="zend opcache" ;; esac
  grep -qx "$ext" <<<"$modules" || { echo "extension $ext missing from build" >&2; exit 1; }
done

version="$(./buildroot/bin/php -n -r 'echo PHP_VERSION;')"
[[ "$version" == "$PHP_MINOR".* ]] || { echo "built PHP $version, expected $PHP_MINOR" >&2; exit 1; }

out="out/$version"
rm -rf "$out" && mkdir -p "$out/ext"
cp buildroot/bin/php buildroot/bin/php-fpm "$out/"
for ext in ${SHARED//,/ }; do
  so="$(find buildroot/modules buildroot/lib/php -name "$ext.so" 2>/dev/null | head -1)"
  if [[ -n "$so" ]]; then
    cp "$so" "$out/ext/"
  else
    echo "warning: $ext.so was not built, PHP $version is installed without it"
  fi
done

echo "FLOCK_BUILT $version $PWD/$out"
