#!/bin/sh
# Installs Flock on Linux (any distribution with glibc, x86-64 or arm64): downloads the flock CLI of
# the newest release, checks it against its SHA-256 and runs `flock-php install`, which copies it to
# ~/.config/Flock/bin and sets Flock up (asks for your password once). The CLI is flock-php on Linux:
# util-linux owns flock(1).
#
#   curl -fsSL https://raw.githubusercontent.com/flock-php/flock/main/install.sh | sh
#
# FLOCK_VERSION=1.0.0 installs that release instead. Arguments go to `flock-php install`:
#
#   curl -fsSL https://raw.githubusercontent.com/flock-php/flock/main/install.sh | sh -s -- --skip-privileged
#
# Running it again updates Flock.
set -eu

repo=flock-php/flock

die() {
  echo "flock: $*" >&2
  exit 1
}

[ "$(uname -s)" = Linux ] || die "this script is for Linux; for macOS and Windows get the app from https://github.com/$repo/releases"

case $(uname -m) in
x86_64 | amd64) arch=x64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "no Flock build for $(uname -m) (there are x86-64 and arm64 ones)" ;;
esac

for tool in curl tar sha256sum; do
  command -v "$tool" >/dev/null || die "$tool is needed (Ubuntu: sudo apt install $tool)"
done

# The tag_name of the first release in a GitHub API response, without its v.
tag() {
  sed -n 's/^ *"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1
}

version=${FLOCK_VERSION:-}
if [ -z "$version" ]; then
  # The newest stable release; until there is one, the newest beta.
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" 2>/dev/null | tag)
  [ -n "$version" ] || version=$(curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=1" | tag)
  [ -n "$version" ] || die "could not find a release of $repo"
fi
version=${version#v}

name=flock-$version-linux-$arch.tar.gz
url=https://github.com/$repo/releases/download/v$version/$name

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading Flock $version ($arch)…"
curl -fsSL -o "$tmp/$name" "$url" || die "could not download $url"
curl -fsSL -o "$tmp/$name.sha256" "$url.sha256" || die "could not download $url.sha256"
(cd "$tmp" && sha256sum -c --status "$name.sha256") || die "$name doesn't match its SHA-256"
tar -xzf "$tmp/$name" -C "$tmp" flock-php

# Piped into sh, stdin is this script: the password prompt and any question need the terminal.
if [ ! -t 0 ] && (: </dev/tty) 2>/dev/null; then
  "$tmp/flock-php" install "$@" </dev/tty
else
  "$tmp/flock-php" install "$@"
fi
