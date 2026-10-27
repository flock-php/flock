#!/usr/bin/env bash
# Builds the signed index of Flock's apt and dnf repository for a release, as the files of the
# "updates" release that packages.flockphp.com points at (see linux/packaging/repository.md).
# Usage: linux/build-repo.sh <version> <packages dir> <out dir> <suite>...
#   The packages dir has the release's flockphp .deb and .rpm files (linux/build-packages.sh, the
#   .rpm files signed by linux/sign-rpms.sh). Each suite (stable, beta) lists only this release:
#   apt and dnf update to the newest version and never down.
#   REPO_KEY_ID  the key in gpg's keyring that signs the index
# Needs dpkg-scanpackages (dpkg-dev), apt-ftparchive (apt-utils), createrepo_c and gpg.
set -euo pipefail
VERSION="$1" PACKAGES="$2" OUT="$3"
shift 3
: "${REPO_KEY_ID:?set REPO_KEY_ID to the signing key}"

# The packages under v<version>/, as on GitHub (the release's tag): the index's paths are
# relative to the suite, and packages.flockphp.com sends /<suite>/v<version>/<file> there.
tree="$(mktemp -d)"
trap 'rm -rf "$tree"' EXIT
mkdir -p "$tree/apt/v$VERSION" "$tree/rpm/v$VERSION"
cp "$PACKAGES"/flockphp_*.deb "$tree/apt/v$VERSION/"
cp "$PACKAGES"/flockphp-*.rpm "$tree/rpm/v$VERSION/"

# apt: a flat repository (no dists/), signed inline (InRelease) and detached (Release.gpg).
(
  cd "$tree/apt"
  dpkg-scanpackages --multiversion "v$VERSION" /dev/null > Packages 2>/dev/null
  gzip -9kn Packages
  apt-ftparchive -o APT::FTPArchive::Release::Origin=Flock -o APT::FTPArchive::Release::Label=Flock release . > Release
  gpg --batch --yes --local-user "$REPO_KEY_ID" --clearsign -o InRelease Release
  gpg --batch --yes --local-user "$REPO_KEY_ID" -abs -o Release.gpg Release
)

# dnf: repodata/ with plain names, which the next release replaces (no stale files to prune).
createrepo_c --quiet --simple-md-filenames --no-database "$tree/rpm"
gpg --batch --yes --local-user "$REPO_KEY_ID" --detach-sign --armor "$tree/rpm/repodata/repomd.xml"

# Release assets are flat: apt-<suite>-<file>, rpm-<suite>-repodata-<file>.
mkdir -p "$OUT"
for suite in "$@"; do
  for f in Packages Packages.gz Release InRelease Release.gpg; do
    cp "$tree/apt/$f" "$OUT/apt-$suite-$f"
  done
  for f in "$tree"/rpm/repodata/*; do
    cp "$f" "$OUT/rpm-$suite-repodata-$(basename "$f")"
  done
done
ls "$OUT"
