#!/usr/bin/env bash
# Installs, checks, and removes a Flock package in its target container.
#
# Usage: linux/validate-package.sh <deb|rpm|aur> <container-image> <package-directory>
#
# VERSION is required for an AUR validation. The AUR package repackages the AMD64 .deb.
set -euo pipefail

package_type="${1:?package type is required}"
image="${2:?container image is required}"
package_dir="$(cd "${3:?package directory is required}" && pwd)"
repo="$(cd "$(dirname "$0")/.." && pwd)"

case "$package_type" in
    deb)
        install='apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq /pkgs/flockphp*.deb'
        remove='DEBIAN_FRONTEND=noninteractive apt-get remove -y -qq flockphp'
        check_polkit=false
        ;;
    rpm)
        install='dnf install -y -q /pkgs/flockphp*.rpm'
        remove='dnf remove -y -q flockphp'
        check_polkit=false
        ;;
    aur)
        : "${VERSION:?VERSION is required for AUR validation}"
        docker run --rm -e VERSION -v "$package_dir:/pkgs:ro" -v "$repo/linux/packaging/aur/flockphp:/aur:ro" "$image" sh -ec '
            pacman -Syu --noconfirm --needed base-devel >/dev/null
            useradd -m builder
            cp -r /aur /home/builder/pkg && cp /pkgs/flockphp_*_amd64.deb /home/builder/pkg/
            cd /home/builder/pkg
            sed -i -e "s|^_tag=.*|_tag=$VERSION|" -e "s|\"https://[^\"]*/|\"|" PKGBUILD
            chown -R builder . && su builder -c "makepkg --nodeps --noconfirm"
            pacman -U --noconfirm flockphp-bin-*.pkg.tar.zst
            test -x /usr/libexec/flockphp/flock && test -f /usr/lib/systemd/system/flock@.service
            test -f /usr/share/applications/dev.flock.Flock.desktop && test -f /usr/share/polkit-1/rules.d/50-flock.rules
            ! ldd /usr/libexec/flockphp/flock-app | grep "not found"
            flk version
            flk --help >/dev/null
            pacman -R --noconfirm flockphp-bin
            ! test -e /usr/bin/flk
        '
        exit 0
        ;;
    *)
        echo "Unsupported package type: $package_type" >&2
        exit 2
        ;;
esac

docker run --rm -v "$package_dir:/pkgs:ro" "$image" sh -ec "
    $install
    test -x /usr/libexec/flockphp/flock && test -f /usr/lib/systemd/system/flock@.service
    test -f /usr/share/applications/dev.flock.Flock.desktop
    ! ldd /usr/libexec/flockphp/flock-app | grep 'not found'
    flk version
    flk --help >/dev/null
    $remove
    ! test -e /usr/bin/flk
"
