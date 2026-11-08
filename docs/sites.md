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

A linked site is a single project folder, wherever it lives. The site name defaults to the folder name.

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
| `--command <cmd>` | The command that starts its server (see below) |
| `--dir <folder>` | The folder the command runs in (the current one by default) |
| `--stop-command <cmd>` | The command that stops a server the command starts in the background |

Remove a link with `flock unlink [name]`.
:::

## Proxying a local service

A Next.js, Nuxt or Vite app in a project folder is best served as a site that runs on Node.js: Flock runs its dev server with the project's Node.js (see [Node.js apps](node.md#nodejs-apps)). For any other local service on a port, such as an object store, a dashboard or a dev server outside your project folders, a proxy passes a `.test` domain to it, so it gets a name, and HTTPS, like a site:

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
| `--command <cmd>` | The command that starts its server (see below) |
| `--dir <folder>` | The folder the command runs in (the current one by default) |
| `--stop-command <cmd>` | The command that stops a server the command starts in the background |

`flock secure softpack` and `flock unsecure softpack` work on proxies too, `flock proxies` lists them with what serves them (or `not running`), and `flock unproxy softpack` removes one.
:::

Requests pass to the server with their `Host` header, so the app sees `softpack.test`, and WebSocket upgrades pass through too, so hot reload works. Some dev servers only answer the hosts they know: allow the domain in Vite's `server.allowedHosts`, or Next.js's `allowedDevOrigins`. Start the server as usual, or give the proxy a command so Flock starts it (below). Until it answers, the domain shows a page saying so, which switches to the app once it runs.

### Starting the server with Flock

A proxy can have a command that starts its server, such as `npm run dev`, run in a folder of your choice. Flock runs it only when you start the proxy, through the shell, with Flock's PHP and Node.js first on `PATH` (so the project's versions are used) and `PORT` set to the upstream's port. It keeps the command running and restarts it if it exits, until you stop the proxy, and the command's output goes to the proxy's log. When Flock quits, the command stops too.

For a command that starts a server in the background and exits, such as `docker compose up -d`, also give a stop command (`docker compose stop`). Starting runs the command once, and stopping runs the stop command. Running or not, the proxy's state is then whatever answers at the upstream.

:::app
On macOS, select the proxy in **Settings → Sites** and set its **Command** (and, under it, the folder and an optional stop command). **Start** and **Stop** run it, and **Output** shows what it prints, with **Clear** and **Open Log**.
:::

:::cli
```bash
cd ~/Code/softpack
flock proxy softpack 3000 --command "npm run dev"   # runs in the current folder, or --dir
flock proxies start softpack
# started `npm run dev` in /Users/me/Code/softpack — its output: flock logs proxy-softpack
flock proxies stop softpack

flock proxy s3 9000 --command "docker compose up -d" --stop-command "docker compose stop"
```

`--command ""` removes the command and the stop command.
:::

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
