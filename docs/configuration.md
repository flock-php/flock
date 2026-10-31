# Configuration

Most settings are changed from the Flock app or the CLI. This page describes where they are stored.

## Project files

| File | Purpose |
| --- | --- |
| `.flockrc` | PHP (`php=8.5`) and optionally Node (`node=22`) versions for the project. Commit it. |
| `.nvmrc` / `.node-version` | Node version, read the same way as by nvm and other tools. Only version numbers count; aliases like `lts/*` are ignored. |

Project files are looked up from the current directory upwards. See [Per-project versions](project-versions.md).

## Data directory

Flock keeps PHP and Node builds, service data, certificates, caught mail and logs in its data directory:

| Platform | Location |
| --- | --- |
| Windows | `%AppData%\Flock` |
| macOS | `~/Library/Application Support/Flock` |
| Linux | `~/.local/share/flock` (or `$XDG_DATA_HOME/flock`) |

:::app
**Settings → General** shows the exact path under **Data**, and **Show in Finder** (macOS), **Show in Explorer** (Windows) or **Open** (Linux) opens it.
:::

## config.json

Global settings live in `config.json` in the data directory. Edit them through the app or the CLI rather than by hand, because the daemon owns this file. It contains:

- the TLD (default `test`),
- the default PHP and Node versions,
- the release channel (stable, beta), and the Xdebug and dump capture switches,
- parked folders and linked sites, with their PHP and Node versions, HTTPS state and extra domains,
- the ports Flock uses,
- the addresses, besides 127.0.0.1, that the sites and DNS also listen on for other machines (`listen`, see [below](#working-from-another-machine)).

## Ports

| Port | Used for |
| --- | --- |
| 80 / 443 | Web server (HTTP / HTTPS) |
| 53535 | DNS for `.test` (macOS and Linux; Windows uses 53) |
| 2525 | Mail catcher (SMTP) |
| 9912 | Dump server |
| 9003 | Xdebug client (your editor) |
| 53531 | Local API used by the apps (token protected) |
| 5432, 3306, 6379, … | Services, see [Databases & services](services.md) |

Each service's port can be changed when you install it, or later (see [Databases & services](services.md#installing-and-removing)). Flock warns you, naming the program, if a port is already taken.

:::app
**Settings → General** shows the domain under **Network**.
:::

## Working from another machine

Flock serves its sites and its `.test` DNS on 127.0.0.1 only. To open the sites from another machine, for example a laptop that reaches a development box over [Tailscale](https://tailscale.com) or another trusted network, list the box's address on that network under `listen` in `config.json`:

```json
{ "listen": ["100.94.14.19"] }
```

Then run `flock restart`, or restart Flock from the app. The web server and the DNS server also listen on that address, and DNS answers lookups that arrive there with that address, so the other machine connects back to it. `flock status` shows the address under `listen`.

Requests that arrive on a `listen` address are served to whoever sends them. List only addresses on networks you trust, such as the box's tailnet address, never one on a public or shared network. Your databases and other services keep listening on 127.0.0.1 only.

Only IPv4 addresses of the machine itself are used. Flock warns about any other entry in `flock status` and keeps the rest working. If the address isn't there yet, because the VPN connects after Flock starts, Flock reports itself degraded and starts listening on the address once it appears.

On the other machine:

- **Send `.test` lookups to the box.** On macOS, create `/etc/resolver/test` with these lines:

  ```
  nameserver 100.94.14.19
  port 53535
  ```

  Elsewhere, add the sites' names to the hosts file instead (`100.94.14.19 acme.test`), one line per name. When Flock runs on Windows, its DNS port is 53 instead of 53535.
- **Trust Flock's certificate authority** so HTTPS works. Copy `certs/ca.crt` from the box's [data directory](#data-directory) and add it to the other machine's trust store. On macOS that's `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.crt`, on Windows `certutil -addstore Root ca.crt` in an administrator prompt. The certificate only covers your TLD and `localhost` (see [HTTPS](https.md#the-local-certificate-authority)).

:::app
`listen` isn't in the app yet. Edit `config.json` as above, then restart Flock.
:::

## php.ini

Each PHP version has its own `php.ini`, and Flock never overwrites your changes. See [PHP](php.md#phpini).
