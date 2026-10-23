# Installation

Download the installer for your platform from the [download page](https://flockphp.com/download). Both apps are code-signed, and the macOS app is notarized by Apple, so they open without security warnings.

## Windows

**Requirements:** Windows 10 (version 2004 or newer) or Windows 11, x64, and the [Microsoft Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe), which PHP needs.

1. Run `Flock-Setup-<version>-x64.exe`. The installer itself doesn't need administrator rights.
2. The tray app opens with the first-run setup. Optionally keep **Open Flock when I sign in** checked.
3. Click **Set up Flock**. Windows asks for administrator approval once.

The setup:

- adds Flock's `bin` folder (with the `php`, `composer`, `node` and `npm` shims) to your PATH,
- registers the **Flock Daemon** scheduled task, which starts at sign-in without a console window,
- routes `*.test` to Flock's DNS server with a name-resolution policy rule, and installs a small helper service that keeps a `# BEGIN Flock` block in your hosts file as a fallback,
- adds Flock's certificate authority to the Trusted Root store.

## macOS

**Requirements:** macOS 15 Sequoia or newer, on Apple silicon (Intel Macs are not supported).

1. Open `Flock-<version>-macOS.dmg` and drag **Flock** to Applications.
2. Start Flock. A bird appears in your menu bar and the first-run setup opens.
3. Optionally keep **Open Flock's menu bar app at login** checked.
4. Click **Set Up Flock**. macOS asks for your password twice: once to add the DNS resolver, and once to trust Flock's certificate authority.

The setup adds the shims to your PATH, registers a launchd agent for the daemon, writes `/etc/resolver/test` so `.test` names go to Flock's DNS server, and trusts the certificate authority in your login keychain.

## Checking the installation

:::app
Click the bird in the menu bar (macOS) or the tray icon (Windows). The top of the panel should read **Running**, followed by the Flock version, and every service listed under **Services** should have a green dot.

![The Flock panel with every service running](screenshot:panel-running)

Open a **new** terminal (so it picks up the new PATH). `php -v` and `node -v` should print Flock's versions.
:::

:::cli
Open a **new** terminal (so it picks up the new PATH) and run:

```bash
flock status
```

It shows the daemon, web server, DNS and every service, and whether `.test` names resolve. `php -v` and `node -v` should print Flock's versions.
:::

## Ports

Flock needs ports **80** and **443** for the web server. If another program uses them, for example another local environment, IIS or Apache, Flock tells you which program it is.

:::app
Stop that program, then click **Restart** at the bottom of the Flock panel.
:::

:::cli
Stop that program and run `flock restart`.
:::
