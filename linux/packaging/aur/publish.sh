#!/usr/bin/env bash
# Publishes a release to the AUR: for each package named, flockphp/PKGBUILD with that pkgname, the
# release's version and its .debs' checksums, and the .SRCINFO the AUR reads, pushed to the
# package's AUR repository (which the first push creates).
#
#   VERSION      the release, e.g. 1.1.0-beta.2
#   DIST         folder with the release's flockphp_<VERSION>_amd64.deb and _arm64.deb
#   AUR_SSH_KEY  private key of the AUR account that maintains the packages
#   AUR_URL      where the packages' repositories are (default: the AUR; tests point it elsewhere)
#
# Usage: linux/packaging/aur/publish.sh flockphp-bin [flockphp-beta-bin]
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
AUR_URL="${AUR_URL:-ssh://aur@aur.archlinux.org}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# aur.archlinux.org's host key: SHA256:RFzBCUItH9LZS0cKB5UE6ceAYhBD5C8GeOBip8Z11+4, as the AUR's
# home page lists it.
echo 'aur.archlinux.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEuBKrPzbawxA/k2g6NcyV5jmqwJ2s+zpgZGZ7tpLIcN' > "$work/known_hosts"
if [ -n "${AUR_SSH_KEY:-}" ]; then
  (umask 077 && printf '%s\n' "$AUR_SSH_KEY" > "$work/key")
  export GIT_SSH_COMMAND="ssh -i $work/key -o IdentitiesOnly=yes -o UserKnownHostsFile=$work/known_hosts -o StrictHostKeyChecking=yes"
fi

sum() { sha256sum "$DIST/flockphp_${VERSION}_$1.deb" | cut -d' ' -f1; }
amd64="$(sum amd64)"
arm64="$(sum arm64)"
hook="$(sha256sum "$here/flockphp/flockphp.hook" | cut -d' ' -f1)"

# makepkg's .SRCINFO for the PKGBUILD in $1: here on Arch, else in an Arch container. makepkg
# refuses to run as root.
srcinfo() {
  if command -v makepkg > /dev/null && [ "$(id -u)" != 0 ]; then
    (cd "$1" && makepkg --printsrcinfo)
  else
    docker run --rm -v "$1:/pkg:ro" archlinux:latest sh -ec \
      'useradd -m builder && cp -r /pkg /home/builder/pkg && chown -R builder /home/builder/pkg && su builder -c "cd /home/builder/pkg && makepkg --printsrcinfo"'
  fi
}

for pkg in "$@"; do
  repo="$work/$pkg"
  git clone -q "$AUR_URL/$pkg.git" "$repo"
  desc=""
  [ "$pkg" = flockphp-beta-bin ] && desc=" (beta versions)"
  sed -e "s/^pkgname=.*/pkgname=$pkg/" \
    -e "s/^_tag=.*/_tag=$VERSION/" \
    -e "s/^pkgrel=.*/pkgrel=1/" \
    -e "s/^\(pkgdesc='.*\)'/\1$desc'/" \
    -e "s/^sha256sums=.*/sha256sums=('$hook')/" \
    -e "s/^sha256sums_x86_64=.*/sha256sums_x86_64=('$amd64')/" \
    -e "s/^sha256sums_aarch64=.*/sha256sums_aarch64=('$arm64')/" \
    "$here/flockphp/PKGBUILD" > "$repo/PKGBUILD"
  cp "$here/flockphp/flockphp.install" "$here/flockphp/flockphp.hook" "$repo/"
  srcinfo "$repo" > "$repo/.SRCINFO"
  git -C "$repo" add PKGBUILD flockphp.install flockphp.hook .SRCINFO
  if git -C "$repo" diff --cached --quiet; then
    echo "$pkg is at $VERSION already"
    continue
  fi
  git -C "$repo" -c user.name="Flock releases" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
    commit -q -m "$VERSION"
  git -C "$repo" push -q origin HEAD:master
  echo "$pkg $VERSION published"
done
