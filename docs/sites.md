# Linking & parking

Flock serves projects in two ways: **parked** folders, where every subfolder becomes a site, and **linked** sites, which are single projects.

## Parking a folder

Every subdirectory of a parked folder is served as `<subdirectory>.test`. New projects in that folder are picked up automatically, so `~/Code/blog` is `http://blog.test` as soon as you create it. You can park as many folders as you like.

:::app
Open **Settings…** from the Flock panel and go to **General**. Under **Project folders**, click **+** and choose one or more folders.

![Project folders in Settings → General](screenshot:settings-general-project-folders)

To stop serving a folder, select it, click **−** and confirm with **Remove**.
:::

:::cli
```bash
cd ~/Code
flock park
```

You can pass a path instead of `cd`-ing into it:

```bash
flock park ~/Work
```

To stop serving a parked folder:

```bash
flock unpark ~/Work
```
:::

## Linking a project

A linked site is a single project folder, wherever it lives. The site name defaults to the folder name. The app shows the site under that name, and the shown name is editable: click the name in the site's header and type a display name — Return or a click away commits it, Esc drops it —, or on Windows open the site's **⋯ → Rename…** and type one, or run `flock site title ACME Shop`, and the app shows the site under it, while its domain, path and settings stay exactly as they are. Committing the field empty, or `flock site title --default`, shows the site name (its folder name, unless `flock link` was given another) again.

:::app
In **Settings → Sites**, click **Add Site…** and choose the project folder. Then select the site to set its HTTPS in the **Site** section and its PHP version under **Versions**.

![Settings → Sites](screenshot:settings-sites)

To remove a linked site, click its **Remove site** icon and confirm with **Remove**. Sites that come from a project folder have no remove button: they disappear when you remove the folder.
:::

:::cli
Link the current directory as a site:

```bash
cd ~/Code/acme
flock link
# linked ~/Code/acme → http://acme.test
```

Pass a name to use a different one, and use the flags to set it up in one go:

```bash
flock link shop --php 8.4 --secure
```

| Flag | Description |
| --- | --- |
| `--php <version>` | Use this PHP version for the site |
| `--secure` | Serve it over HTTPS right away |

Remove a link with `flock unlink [name]`.
:::

## Domain

A site is served as `<name>.<suffix>`: by default the name is the site name and the suffix is Flock's global TLD, and the site moves with every change to the suffix. One site can take a suffix of its own:

:::cli
```bash
cd ~/Code/acme
flock site tld loc      # acme.loc; the global suffix and the other sites stay as they are
flock site tld --inherit
```
:::

:::app
On macOS and Windows, select the site in **Settings → Sites**: the **Domain** row in its **Site** section picks the suffix — **Inherit** (the site follows the global one), **.alt**, **.example**, **.internal**, **.loc**, **.test**, or your own under **Custom…**, checked as you type. **Apply** runs the whole switch, the permission prompt included: the site's `.env` files are retargeted and the new suffix's DNS and its own certificate root are provisioned. Decline the prompt and the config is on the new suffix already; the app tells you to run `flock install` to finish the setup. **Add Alias…** picks an alias's suffix the same way: **Inherit** rides the site's suffix, a picked suffix pins the alias. Subdomains have no suffix to pick: they hang under the site's domain and follow it ([Domains & subdomains](domains.md#extra-domains-and-aliases)). The global **Domain** picker is in **Settings → General → Network** (see [Choosing the local domain](domains.md#choosing-the-local-domain)).
:::

A site with a suffix of its own is never dragged by a global change, and neither is an alias pinned to a suffix. What a suffix switch provisions, and the rules a suffix must obey: [Per-site domains](domains.md#per-site-domains).

The name part of the domain is editable too — one or more words joined with dots, the suffix staying where it is:

:::cli
```bash
cd ~/Code/acme
flock site host liga        # acme.test becomes liga.test
flock site host cms.liga    # … or cms.liga.test
flock site host             # show what the site is served as now
flock site host --default   # the site name rules again (the default)
```
:::

Everything that carries the site's name into a host moves with the rename: the site's subdomains and its worktrees' hosts move, and the `.env` files of the site and its worktrees are retargeted from the old host to the new in the same run. Aliases are names of their own and never move. Nothing else does either: the folder, the site name and the suffix are untouched, and no provisioning is needed — the suffix's wildcard DNS already covers the new name and its certificate is issued on demand. A name already served — by another site, an alias or a proxy — is refused. A worktree is served under its parent's domain, so it is renamed through the parent ([Git worktrees](worktrees.md)).

:::app
On macOS, the pencil next to the site's address opens the same switch; on Windows, it's **⋯ → Change the domain name…**. One field with the current name, the hint that the suffix is chosen on the site's **Domain** row, and **Use the folder name** to hand the domain back to the site name. **Rename** runs the full switch, `.env` retargeting included.
:::

## Proxying a local service

Apps that aren't PHP, such as a Next.js, Nuxt or Vite dev server, an object store or a dashboard, run a server of their own on a port. A proxy passes a `.test` domain to that server, so the app gets a name, and HTTPS, like a site:

:::app
On macOS, click **Add Proxy…** in **Settings → Sites**, and give a name and the port (or host:port or URL). Proxies are listed under the sites, here and in the Flock panel, with what serves them (for example Node.js 22, when it's one of Flock's versions) or **Not running**; select one to turn on HTTPS, change its upstream or remove it.
:::

:::cli
```bash
flock proxy softpack 3000
# proxied http://softpack.test → http://127.0.0.1:3000
```

A port means `127.0.0.1`; give a host and port (`localhost:5173`) or a URL (`https://127.0.0.1:8443`) for anything else. Proxying the name again changes its upstream.

| Flag | Description |
| --- | --- |
| `--secure` | Serve it over HTTPS right away |

`flock secure softpack` and `flock unsecure softpack` work on proxies too, `flock proxies` lists them with what serves them (or `not running`), and `flock unproxy softpack` removes one.
:::

Requests pass to the server with their `Host` header, so the app sees `softpack.test`, and WebSocket upgrades pass through too, so hot reload works. Some dev servers only answer the hosts they know: allow the domain in Vite's `server.allowedHosts`, or Next.js's `allowedDevOrigins`. Flock doesn't start the server, so start it as usual. Until it answers, the domain shows a page saying so, which switches to the app once it runs.

A proxy's name can't be a linked site's: Flock refuses it. A folder of the same name in a project folder is served by the proxy instead, so `flock proxy softpack 3000` works for `~/Code/softpack`; `flock unproxy softpack` serves the folder again.

## Custom Caddy directives

A PHP site can add its own [Caddy directives](https://caddyserver.com/docs/caddyfile/directives), for example CORS headers. Put them in a file and name it with `caddyfile:` in the project's `flock.yml`:

```yaml
caddyfile: flock.caddy
```

A relative path starts at the folder of the `flock.yml`. To use a file only on your machine, set `caddyfile:` in `flock.local.yml` instead: it replaces the one in `flock.yml`, and `caddyfile: ""` turns it off. Without `caddyfile:`, Flock serves the site as usual and ignores any `Caddyfile` the project has.

The file holds directives only, without a site address or the braces around them: Flock adds them to the site it serves, before PHP, and reloads them when the file changes. This file allows a separate frontend to call the site and answers CORS preflight requests:

```caddyfile
@cors_preflight method OPTIONS

header {
    Access-Control-Allow-Origin "https://admin.example.test"
    Access-Control-Allow-Methods "GET, POST, PUT, PATCH, DELETE, OPTIONS"
    Access-Control-Allow-Headers "Content-Type, Authorization"
    Vary "Origin"
}

respond @cors_preflight 204
```

Site options such as `tls` and `handle_errors` aren't supported, and neither is `import`. If Flock can't use the file, it serves the site without it and says why in its warnings. The directives only apply to the site on your machine, so keep the production CORS setup in the app or the production web server.

## Listing sites

:::app
**Settings → Sites** lists every linked and parked site with its domain and a lock when it's secured. Select one to see its PHP and Node versions, domains and server. Sites from a project folder carry a **Project folder** badge. The **Sites** section of the Flock panel lists them too: click one to open it in your browser.
:::

:::cli
```bash
flock sites
```

This lists every linked and parked site with its PHP version, URL and path, and every proxy with its URL and upstream.
:::

## Document root

For each site Flock serves `public/` when it contains an `index.php`, which covers Laravel and most other frameworks. Otherwise it serves the project folder itself, so plain PHP projects and static sites work too.
