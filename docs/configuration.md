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

:::app
**Settings → General** shows the exact path under **Data**, and **Show in Finder** (macOS) or **Show in Explorer** (Windows) opens it.
:::

## config.json

Global settings live in `config.json` in the data directory. Edit them through the app or the CLI rather than by hand, because the daemon owns this file. It contains:

- the TLD (default `test`),
- the default PHP and Node versions,
- the release channel (stable, beta), and the Xdebug and dump capture switches,
- parked folders and linked sites, with their PHP and Node versions, HTTPS state and extra domains,
- the ports Flock uses.

## Ports

| Port | Used for |
| --- | --- |
| 80 / 443 | Web server (HTTP / HTTPS) |
| 53535 | DNS for `.test` (macOS; Windows uses 53) |
| 2525 | Mail catcher (SMTP) |
| 9912 | Dump server |
| 9003 | Xdebug client (your editor) |
| 53531 | Local API used by the apps (token protected) |
| 5432, 3306, 6379, … | Services, see [Databases & services](services.md) |

Each service's port can be changed when you install it, or later (see [Databases & services](services.md#installing-and-removing)). Flock warns you, naming the program, if a port is already taken.

:::app
**Settings → General** shows the domain under **Network**.
:::

## php.ini

Each PHP version has its own `php.ini`, and Flock never overwrites your changes. See [PHP](php.md#phpini).
