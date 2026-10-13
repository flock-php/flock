#!/usr/bin/env bash
# Development wrapper: builds PHP into build/php/out/<version> with Flock's embedded build script.
# Usage: build/php/build.sh 8.5
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
FLOCK_BUILD_DIR="$here" exec bash "$here/../../internal/phpbuild/build.sh" "$@"
