# Uninstalling

:::app
To remove Flock, open **Settings…** from the Flock panel, go to **About** and click **Uninstall Flock…**, then confirm with **Uninstall**. On macOS, click **Quit Flock** when it's done to move the app to the Trash.

On Windows, you can also uninstall Flock from **Settings → Apps**.

On Linux, open Flock, go to **About** and click **Uninstall Flock…**. Linux asks for your password once. Then remove the package with `sudo apt remove flockphp` (Ubuntu), `sudo dnf remove flockphp` (Fedora) or `sudo pacman -R flockphp-bin` (Arch, or `flockphp-beta-bin`).
:::

:::cli
To remove Flock, run:

```bash
flock uninstall
```

It asks whether to delete your sites' settings and databases too. The answer defaults to no.

On Linux, remove the package afterwards with `sudo apt remove flockphp` (Ubuntu), `sudo dnf remove flockphp` (Fedora) or `sudo pacman -R flockphp-bin` (Arch, or `flockphp-beta-bin`). Removing only the package stops Flock and undoes its system-wide setup, but keeps the changes in your home folder: run `flock uninstall` first.
:::

This removes:

- the background daemon and its startup entry,
- Flock's entry on your PATH,
- the `.test` DNS configuration and the trust in Flock's certificate authority,
- the menu bar / tray app (on Linux, the package removes the app),
- downloaded PHP, Node and service binaries,
- certificates, caught mail, captured dumps and logs.

**Your sites' settings and databases are kept.** After reinstalling Flock, install your PHP, Node and service versions again: your databases come back with them. Edits to `php.ini` and global npm packages are not kept.

## Removing everything

To also delete your sites' settings and your databases:

:::app
Tick **Also delete my sites' settings and databases** before you click **Uninstall**.
:::

:::cli
```bash
flock uninstall --purge
```
:::

> This can't be undone. Back up any databases you want to keep first, for example with `pg_dump` or your database client.

Your project folders are never touched, and `.flockrc` files stay in your projects.

On Linux, `sudo apt purge flockphp` also removes Flock's package repository from your system. With dnf, `dnf remove` already does.
