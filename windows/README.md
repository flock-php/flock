# Flock for Windows

The Windows version is the same Go daemon and CLI as on macOS (`cmd/flock`), plus a native
WinUI 3 tray app (`windows/FlockTray`) that mirrors the macOS menu bar app: a panel from the
notification area icon, and the Mail, Dumps, Logs and Settings windows.

```
windows/
  FlockKit/         models + API client (net10.0, no UI — the macOS app's FlockKit)
  FlockKit.Tests/   xUnit tests, run anywhere: dotnet test windows/FlockKit.Tests
  FlockTray/        the WinUI 3 app (unpackaged, self-contained Windows App SDK)
  build.ps1         tests + publishes FlockTray to windows\dist\FlockTray-<arch>\
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
| `/etc/resolver/test` → Flock's DNS server | The **hosts helper**, a small Windows service (LocalSystem), copies the site domains from `%AppData%\Flock\hosts.txt` into a marked block of the hosts file. It only accepts `*.<tld>` names → 127.0.0.1. No wildcard subdomains: link `api.app` as its own site if needed. |
| PHP-FPM pool per version | 6 `php-cgi.exe` workers per version on 127.0.0.1:19000+ (e.g. 19840–19845 for 8.4); Caddy balances requests to the least busy one. |
| PHP built from source by static-php-cli | Official builds from windows.php.net (NTS x64) + the matching Xdebug DLL from xdebug.org, verified by SHA-256. "Building" a version = downloading it. |
| Shell script shims in `bin/` | `php.exe`, `composer.exe`, `node.exe`, … in `%AppData%\Flock\bin` are hard links to `flock.exe`, which acts as `flock exec <tool>` under those names. The folder is prepended to the user PATH. |
| CA trusted in the System keychain | CA added to the machine's Trusted Root store (`certutil -addstore Root`). PHP gets a bundle of the Windows root store + Flock's CA. |

`flock install` asks for administrator rights once (UAC), for the hosts helper and the CA. It asks
again only when the helper is outdated (after updating Flock) or set up for another TLD.

Redis (Valkey) and the PostgreSQL client tools still install from local builds with `--from`, as on
macOS. The PostgreSQL server (zonky builds) and Node.js (nodejs.org) download on Windows too.

## Trying it on a Windows machine

1. **Build the CLI** (on any machine with Go): `make windows`, which writes
   `dist/windows-amd64/flock.exe` and `dist/windows-arm64/flock.exe`. Copy the right one to the
   Windows machine.
2. **Install**: in PowerShell, `.\flock.exe install`. Expect a UAC prompt. Then open a **new**
   terminal so the PATH change applies.
3. **PHP**: open the tray app (step 4) → Settings → PHP → Install, or
   `flock php install --from php-8.5.x-nts-Win32-vs17-x64.zip` with a zip from windows.php.net.
   PHP needs the [Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe).
4. **Tray app**: install the .NET 10 SDK (`winget install Microsoft.DotNet.SDK.10`), then
   `.\windows\build.ps1` and start `windows\dist\FlockTray-x64\FlockTray.exe`.
   `FlockTray.exe --selftest` checks every API call against the running daemon.

### First-run checklist

- [ ] `flock install` finishes; Task Scheduler shows "Flock Daemon"; `flock status` answers
- [ ] No console windows appear (daemon, php-cgi, postgres all run hidden)
- [ ] Services shows "Hosts file (*.test)" running; the hosts file has a `# BEGIN Flock` block after linking a site
- [ ] `flock link` in a Laravel project → `http://<name>.test` serves it; `flock secure` → HTTPS without warnings in Edge/Chrome
- [ ] `php -v`, `composer -V`, `php artisan` in a new terminal use Flock's PHP (`where.exe php` lists `%AppData%\Flock\bin\php.exe` first)
- [ ] `composer install` reaches packagist (CA bundle with the Windows roots)
- [ ] Settings → PHP: install 8.4, switch a site to it, uninstall it again (files must not be locked)
- [ ] Xdebug toggle restarts the workers; a breakpoint hits with `?XDEBUG_TRIGGER=1`
- [ ] Mail: send from tinker → appears in Mail, HTML view, attachments save
- [ ] Dumps: `dump()` in a route → Dumps window, the file link opens the editor
- [ ] `flock restart` and the tray's Restart bring the daemon back with a new pid
- [ ] Ports 80/443 taken (IIS, Skype…) → the warning names the program
- [ ] Sign out and in → daemon starts; with "Open Flock at sign-in" on, the tray icon too
- [ ] `flock uninstall` removes the task, PATH entry, hosts block, helper service and CA

Logs: `%AppData%\Flock\log\` (daemon.log, web.log, php8.x-cgi.log, php-build-8.x.log) and
`%LocalAppData%\Flock\tray.log` for unexpected errors in the tray app.
