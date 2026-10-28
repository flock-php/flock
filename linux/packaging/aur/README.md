# Flock in the AUR

Flock for Arch Linux is two AUR packages, one per channel:

- `flockphp-bin`: stable releases (and betas until the first stable release),
- `flockphp-beta-bin`: betas, and stable releases once they are newer than the latest beta.

Both come from `flockphp/PKGBUILD`, which takes the files of a release's `.deb` (the CLI and daemon,
the app, the unit template and the polkit rule) and installs them the Arch way. `flockphp.install`
restarts the daemons after an upgrade, as the `.deb`'s scripts do, and `flockphp.hook` undoes every
user's setup when Flock is removed. The packages conflict, so installing one replaces the other:
that's how Arch users switch channels (`flock channel` tells them), and pacman counts the files they
share as upgraded, so the setup stays. Arch has no Flock package repository: AUR helpers update Flock
(`flock update` says so). CI builds the package from each `.deb` and installs it on `archlinux:latest`
(build.yml).

## Publishing

Releases publish themselves: after beta.yml and release.yml publish a release, their `aur` job
(`.github/actions/aur`) runs `publish.sh` for the packages of the channels the release now leads, as
the update feed (`releases.json`) has them. `publish.sh` fills in the package's name, the version and
the `.debs`' checksums, writes `.SRCINFO` and pushes to `ssh://aur@aur.archlinux.org/<package>.git`.
A failed push can be run again from the workflow run's `aur` job.

It pushes with `AUR_SSH_KEY`, a secret of the `release` environment: the private key of the AUR
account that maintains both packages. Without it, the job only warns. To set it up once:

1. Register at https://aur.archlinux.org/register.
2. Make a key just for this: `ssh-keygen -t ed25519 -N '' -C flock-aur -f flock-aur`.
3. Paste `flock-aur.pub` into the AUR account (My Account → SSH Public Key).
4. Add the private key `flock-aur` as `AUR_SSH_KEY` to the `release` environment (Settings →
   Environments → release), then delete both files.

The first release after that creates the packages in the AUR. A PKGBUILD change that comes without a
release: bump `pkgrel` in the AUR's copy by hand, or wait for the next release.

To try a change before a release, on Arch: point `publish.sh` at local repositories instead of the
AUR (`git init --bare remote/flockphp-bin.git`, then
`VERSION=<release> DIST=<folder with its .debs> AUR_URL=file://$PWD/remote linux/packaging/aur/publish.sh flockphp-bin`),
clone the result and `makepkg -si` in it.
