# Flock's package repository

`packages.flockphp.com` is the apt and dnf repository of the `flockphp` package. The package adds
it on install (`/etc/apt/sources.list.d/flockphp.sources` or `/etc/yum.repos.d/flockphp.repo`),
so Flock updates with the system's other updates. `flock update` runs apt or dnf right away.

## Suites

- `stable`: the newest stable release.
- `beta`: the newest beta, or the newest stable release once it is newer.

Each suite lists one version: apt and dnf update to it and never down. `flock channel beta|stable`
(and the app's Beta versions switch) points the repository file at the other suite.

The package's install adds the file on the suite of its own version (`--repo auto`): a beta's is
`beta`, so a beta installed before the first stable release finds its suite. `flk install` then
moves a user still on the default channel to beta, to match.

## Layout

Everything is a GitHub release asset; `packages.flockphp.com` only redirects
(`packages.flockphp.com.nginx`).

| URL | Asset |
| --- | --- |
| `/apt/<suite>/{Packages,Packages.gz,Release,InRelease,Release.gpg}` | `updates/apt-<suite>-<file>` |
| `/rpm/<suite>/repodata/<file>` | `updates/rpm-<suite>-repodata-<file>` |
| `/{apt,rpm}/<suite>/v<version>/<package>` | `v<version>/<package>` |
| `/flockphp.asc` | `updates/flockphp.asc` |

The apt repository is flat (`Suites: ./`). The release workflows build the index with
`linux/build-repo.sh` and sign it; the `.rpm` files are signed too (`linux/sign-rpms.sh`), the
`.deb` files are checked against the signed index.

## The key

One OpenPGP key signs the index and the `.rpm` files. Its public half is
`linux/packaging/flockphp.asc`, which the packages ship; the private half is the
`REPO_SIGNING_KEY` secret of the `release` environment. Made once, with no expiry:

```sh
gpg --quick-gen-key "Flock Packages <packages@flockphp.com>" ed25519 sign never
gpg --armor --export packages@flockphp.com > linux/packaging/flockphp.asc
gpg --armor --export-secret-keys packages@flockphp.com   # → the REPO_SIGNING_KEY secret
```

Keep an offline copy of the private key: packages signed with a new key need users to get the new
public key.

## Setting it up by hand

```sh
# Ubuntu, Debian
curl -fsSL https://packages.flockphp.com/flockphp.asc | sudo tee /usr/share/keyrings/flockphp.asc > /dev/null
printf 'Types: deb\nURIs: https://packages.flockphp.com/apt/stable/\nSuites: ./\nSigned-By: /usr/share/keyrings/flockphp.asc\n' \
  | sudo tee /etc/apt/sources.list.d/flockphp.sources
sudo apt update && sudo apt install flockphp

# Fedora
sudo rpm --import https://packages.flockphp.com/flockphp.asc
printf '[flockphp]\nname=Flock\nbaseurl=https://packages.flockphp.com/rpm/stable\ngpgcheck=1\nrepo_gpgcheck=1\ngpgkey=https://packages.flockphp.com/flockphp.asc\n' \
  | sudo tee /etc/yum.repos.d/flockphp.repo
sudo dnf install flockphp
```
