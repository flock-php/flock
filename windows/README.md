# Flock for Windows

The Windows version is the same Go daemon and CLI as on macOS (`cmd/flock`), plus a native
WinUI 3 tray app (`windows/FlockTray`) that mirrors the macOS menu bar app: a panel from the
notification area icon, and the Mail, Dumps, Logs and Settings windows.

```
windows/
  FlockKit/         models + API client (net10.0, no UI — the macOS app's FlockKit)
  FlockKit.Tests/   xUnit tests, run anywhere: dotnet test windows/FlockKit.Tests
  FlockTray/        the WinUI 3 app (unpackaged, self-contained Windows App SDK)
  build.ps1         tests + publishes FlockTray to windows\dist\FlockTray-x64\
```

The UI is written in C# instead of XAML markup, the way the macOS app is written in SwiftUI. It
uses the same WinUI controls, and it compiles (with full type checking) on macOS and Linux too:

```sh
dotnet msbuild windows/FlockTray -restore -t:Compile -p:RuntimeIdentifier=win-x64 -p:Platform=x64 -p:WindowsAppSDKSelfContained=false
```

Running and publishing it needs Windows (the Windows App SDK's packaging tools are Windows programs).

## How Flock works on Windows

| macOS | Windows |
| --- | --- |
| launchd agent keeps `flock daemon` running | Scheduled task "Flock Daemon" starts `flockd.exe daemon --watchdog` at sign-in; the watchdog restarts a crashed daemon. `flockd.exe` is `flock.exe` marked as a GUI program, so no console window appears. |
| `/etc/resolver/test` → Flock's DNS server | An **NRPT rule** (`Get-DnsClientNrptRule`, comment "Flock") sends every `*.test` name to Flock's DNS server on 127.0.0.1:53 — port 53 because NRPT rules can't name a port. It answers A records only (sites listen on IPv4 only; an AAAA `::1` answer cost PHP ~2 s per connection). So every subdomain works, up to 4 levels below a site or alias (`a.b.c.d.app.test`). The daemon checks every minute that lookups really arrive (Group Policy, VPN clients or a program on port 53 can get in the way) and reports it in `flock status` / Settings. |
| — | Fallback: the **hosts helper**, a small Windows service (LocalSystem), copies site domains and their extra domains (`flock domain add tenant1`, `flock domain add app-2.test`) from `%AppData%\Flock\hosts.txt` into a marked block of the hosts file. It only accepts `*.<tld>` names → 127.0.0.1. These names resolve even where the NRPT rule is overridden, and in browsers whose own secure DNS (DoH in "secure" mode) skips NRPT. It is its own program, `flock-helper.exe` (Caddy, linked into `flock.exe`, claims the service dispatcher of any process that runs as a service); it logs failures to `C:\Program Files\Flock\hosts-helper.log`. |
| PHP-FPM pool per version | 6 `php-cgi.exe` workers per version on 127.0.0.1:19000+ (e.g. 19840–19845 for 8.4); Caddy balances requests to the least busy one. |
| PHP from the binaries manifest: Flock's own build (`bin/php`, `php-fpm`, shared extensions) | PHP from the binaries manifest: the official windows.php.net build (thread-safe x64) plus Flock's extension pack (apcu, igbinary, imagick, mongodb, pcov, redis, xdebug), verified by SHA-256 |
| Shell script shims in `bin/` | `php.exe`, `composer.exe`, `node.exe`, … in `%AppData%\Flock\bin` are hard links to `flock.exe`, which acts as `flock exec <tool>` under those names. The folder is prepended to the user PATH. |
| CA trusted in the System keychain | CA added to the machine's Trusted Root store (`certutil -addstore Root`). PHP gets a bundle of the Windows root store + Flock's CA. |

`flock install` asks for administrator rights once (UAC), for the hosts helper, the NRPT rule and the
CA. It asks again only when the helper is outdated (after updating Flock), the rule is missing, or
either is set up for another TLD.

Everything downloads on Windows, verified by the vendors' published checksums — nothing is built from source:

| Service | macOS | Windows |
| --- | --- | --- |
| PostgreSQL (`flock postgres install`), with `psql`, `pg_dump` and the other client tools | the binaries manifest (built by Flock) | the binaries manifest (built by Flock) |
| Valkey (`flock valkey install`) | built from source | not available (no established Windows build) — use Redis |
| Redis (`flock services install redis [8.10\|8.4\|7.4\|7.2]`) | built from source | built from the upstream tags with MSYS2 by the public CI of [redis-windows/redis-windows](https://github.com/redis-windows/redis-windows) (~10 MB). A Redis that an older Flock installed moves over (line 7.2, with its data). |
| MySQL, MariaDB, MongoDB (`flock services install <id> [version]`) | MySQL, MongoDB: official builds; MariaDB: built from source | the official zips (MySQL ~330 MB, MariaDB ~100 MB, MongoDB ~800 MB — Flock drops the debug symbols) |
| Meilisearch, RustFS | official binaries | official binaries |
| Typesense | official builds | not available (no Windows build) |
| Node.js | nodejs.org | nodejs.org |

`--from <dir>` still works on Windows for your own builds. Running `psql` in a terminal before
the tools are installed offers to download them; `redis-cli` talks to the installed Redis. The PostgreSQL tools, like PHP, need
the [Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe).

Windows PHP builds don't include the phpredis extension, so Flock suggests `REDIS_CLIENT=predis`
there (macOS: `phpredis`); add the client to the project with `composer require predis/predis`.

Services are stopped gracefully, as on macOS, although Windows has no signals: PostgreSQL through
`pg_ctl stop -m fast`, Redis with a `SHUTDOWN` command (which saves its snapshot). Only when that
fails, or a service hasn't exited after 10 seconds, it is killed. php-cgi workers are just killed.

## Trying it on a Windows machine

1. **Build the CLI** (on any machine with Go): `make windows`, which writes `flock.exe` and
   `flock-helper.exe` to `dist/windows-amd64/`. Copy the folder to the Windows machine (both files,
   side by side). Only x64 is built for now; Windows on ARM runs it under emulation.
2. **Install**: in PowerShell, `.\flock.exe install`. Expect a UAC prompt. Then open a **new**
   terminal so the PATH change applies.
3. **PHP**: `flock php install 8.5`, or the tray app (step 4) → Settings → PHP → Install. In a
   terminal, just running `php` offers to install a missing version (Node.js too). A zip from
   windows.php.net also works: `flock php install --from php-8.5.x-nts-Win32-vs17-x64.zip`.
   PHP needs the [Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe).
4. **Tray app**: install the .NET 10 SDK (`winget install Microsoft.DotNet.SDK.10`), then
   `.\windows\build.ps1` and start `windows\dist\FlockTray-x64\FlockTray.exe`.
   `FlockTray.exe --selftest` checks every API call against the running daemon.

### First-run checklist

- [ ] `flock install` finishes; Task Scheduler shows "Flock Daemon"; `flock status` answers
- [ ] No console windows appear (daemon, php-cgi, postgres all run hidden)
- [ ] Services shows "Hosts file" running; the hosts file has a `# BEGIN Flock` block after linking a site
- [ ] `flock status` says `*.test → 127.0.0.1, any subdomain`; `https://a.b.<name>.test` serves the site without certificate warnings
- [ ] `flock domain add tenant1` → `tenant1.<name>.test` appears in the hosts file within a few seconds
- [ ] `flock link` in a Laravel project → `http://<name>.test` serves it; `flock secure` → HTTPS without warnings in Edge/Chrome
- [ ] `php -v`, `composer -V`, `php artisan` in a new terminal use Flock's PHP (`where.exe php` lists `%AppData%\Flock\bin\php.exe` first)
- [ ] `composer install` reaches packagist (CA bundle with the Windows roots)
- [ ] Settings → PHP: install 8.4, switch a site to it, uninstall it again (files must not be locked)
- [ ] A PhpStorm breakpoint hits on reload with no browser extension, and the site goes back to the fast pool once it is removed
- [ ] `?XDEBUG_TRIGGER=1` and the Xdebug browser extension hit a breakpoint; requests without them never load Xdebug
- [ ] `flock debug artisan …` hits a breakpoint; plain `php` does not load Xdebug
- [ ] `flock services install redis` downloads Redis and starts it; `redis-cli ping` → PONG; `flock postgres install` → `psql -c "select 1"`
- [ ] Upgrading from a Flock that had Redis: it keeps running from `services\redis\7.2.16` with its data
- [ ] Settings → Services → All: install MySQL, MariaDB (port suggested as 3307 next to MySQL), MongoDB, Meilisearch, RustFS; each starts and its connection works; Typesense and Valkey show as not available
- [ ] Stopping Redis/MySQL/MariaDB/PostgreSQL in Services (or `flock restart`) logs a clean shutdown (redis.log: "Redis is now ready to exit"; mysql.log: "Shutdown complete"; postgres.log: "database system is shut down"), not a kill
- [ ] Mail: send from tinker → appears in Mail, HTML view, attachments save
- [ ] Dumps: `dump()` in a route → Dumps window, the file link opens the editor
- [ ] `flock restart` and the tray's Restart bring the daemon back with a new pid
- [ ] Ports 80/443 taken (IIS, Skype…) → the warning names the program
- [ ] Sign out and in → daemon starts; with "Open Flock at sign-in" on, the tray icon too
- [ ] `flock uninstall` removes the task, PATH entry, hosts block, helper service, CA and Flock's
      files (binaries, PHP/Node versions, certificates, logs, caches), keeping only `config.json`
      and the services' data (PostgreSQL, MySQL, Redis, ...); `flock uninstall --purge` removes those too

Logs: `%AppData%\Flock\log\` (daemon.log, web.log, php8.x-cgi.log, php8.x-install.log) and
`%LocalAppData%\Flock\tray.log` for unexpected errors in the tray app.
