# Uninstalling

:::app
To remove Flock, open **Settings…** from the Flock panel, go to **About** and click **Uninstall Flock…**, then confirm with **Uninstall**. On macOS, click **Quit Flock** when it's done to move the app to the Trash.

On Windows, you can also uninstall Flock from **Settings → Apps**.
:::

:::cli
To remove Flock, run:

```bash
flock uninstall
```

It asks whether to delete your sites' settings and databases too. The answer defaults to no.
:::

This removes:

- the background daemon and its startup entry,
- Flock's entry on your PATH,
- the `.test` DNS configuration and the trust in Flock's certificate authority,
- the menu bar / tray app,
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
