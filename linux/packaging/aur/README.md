# flockphp-bin (AUR)

Flock for Arch Linux is the AUR package `flockphp-bin`: `flockphp-bin/PKGBUILD` takes the files of a
release's `.deb` (the CLI and daemon, the app, the unit template and the polkit rule) and installs
them the Arch way. `flockphp-bin.install` does what the `.deb`'s scripts do, without Flock's package
repository: Arch updates Flock from the AUR (`flock update` says so). CI builds the package from
each `.deb` and installs it on `archlinux:latest` (build.yml).

## Publishing a release

On Arch (or in an `archlinux` container), with the AUR account's SSH key:

```sh
git clone ssh://aur@aur.archlinux.org/flockphp-bin.git aur-flockphp-bin   # once
cp linux/packaging/aur/flockphp-bin/{PKGBUILD,flockphp-bin.install} aur-flockphp-bin/
cd aur-flockphp-bin
sed -i 's/^_tag=.*/_tag=1.2.0/' PKGBUILD        # the release, without the v
updpkgsums                                       # the .debs' sha256 sums, in place of SKIP
makepkg --printsrcinfo > .SRCINFO
makepkg -si                                      # builds and installs it: check before pushing
git add PKGBUILD flockphp-bin.install .SRCINFO
git commit -m "1.2.0" && git push
```

`pkgver` is the tag without its first `-` (`1.2.0-beta.1` → `1.2.0beta.1`), which pacman orders
before `1.2.0`. Bump `pkgrel` instead when only the PKGBUILD changes.
