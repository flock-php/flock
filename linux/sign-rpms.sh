#!/usr/bin/env bash
# Signs the release's .rpm files with the repository's key, so dnf checks each package
# (gpgcheck=1) besides the index. The .deb files stay unsigned: apt checks them against the
# signed index, as is usual for apt repositories.
# Usage: linux/sign-rpms.sh <packages dir>
#   REPO_KEY_ID  the key in gpg's keyring
# Needs rpmsign (rpm) and gpg.
set -euo pipefail
: "${REPO_KEY_ID:?set REPO_KEY_ID to the signing key}"
for rpm in "$1"/flockphp-*.rpm; do
  rpmsign --define "_gpg_name $REPO_KEY_ID" --addsign "$rpm"
done
