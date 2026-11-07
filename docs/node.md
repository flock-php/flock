# Node.js

Flock manages Node.js the same way as PHP. Versions are installed side by side, and `node`, `npm` and `npx` pick the right one for each project.

## Installing versions

Flock installs the official Node.js builds from its signed package list, the newest release of each major. Node 16 and newer are supported.

:::app
Open **Settings…** from the Flock panel and go to **Node**. Each major shows its newest release and the npm version that comes with it. Click **Install** next to a line, or **Update to …** when a newer release is out. **Update npm** appears when a newer npm is available, and **Check now** looks for new releases.

![Settings → Node](screenshot:settings-node)

To remove a version, open its **⋯** menu and choose **Uninstall…**.
:::

:::cli
```bash
flock node install 22        # the newest 22.x
flock node install 22.11     # also the newest 22.x
flock node list
```
:::

If a project needs a Node version that isn't installed yet, `node`, `npm` and `npx` offer to install it when you run them in a terminal.

## The default version

:::app
In **Settings → Node**, click **Make Default** next to the line you want.
:::

:::cli
```bash
flock node default 22
```
:::

## Per-project versions

Flock picks the Node version in this order:

1. a version isolated for the site on your machine,
2. `node=` in the project's `.flockrc`,
3. `.nvmrc` or `.node-version`,
4. the default.

Most projects already have an `.nvmrc`, so they work without any changes.
A project that pins an exact release (`22.11.0` in `.nvmrc`) gets the newest release of that major, since Flock keeps one release per major; `flock node install` says so when it installs it.

:::app
Choose a version for a site in **Settings → Sites** to isolate it, or pin it to the project with **Pin Node 22 to Project…**.
:::

:::cli
Isolate a version with `flock isolate node 20`. `flock pin node 22` writes to whichever of the project files the project already uses, or creates an `.nvmrc`.
:::

See [Per-project versions](project-versions.md).

## Global packages

Packages installed with `npm install -g` belong to their Node line (for example 22) and survive updates within it. The global packages of your default version are on your PATH.

## Node.js apps

A Next.js, Nuxt or Vite app in a project folder can run on Node.js instead of PHP: Flock runs its `npm run dev -- --port <port>` (with `PORT` set too) using the project's Node.js version, restarts it when it exits, and serves the site's domains and HTTPS from it, hot reload included. The site keeps everything else a site has: its domains, its folder and its Node.js version, picked or pinned as above; a new version restarts the dev server.

:::app
In **Settings → Sites**, select the site and choose **Node.js (npm run dev)** under **Server → Runs on**. The site shows whether its dev server runs and on which Node.js, here and in the Flock panel; **Show Log** shows its output and **Restart** restarts it.
:::

:::cli
```bash
cd ~/Code/softpack
flock node serve
# softpack runs `npm run dev` on Node.js 22.23.4 — http://softpack.test, log: `flock logs node-softpack`
```

`flock node restart` restarts the dev server, and `flock node unserve` serves the site with PHP again.
:::

### Another command

A project that starts with something else, such as `pnpm dev`, `bun run dev` or a script of another name, gets a command of its own. Flock runs it through the shell in the project, with the project's Node.js and its `node_modules/.bin` on the PATH, as `npm run` does. `{port}` in it becomes the dev server's port, and `PORT` is set too.

:::app
Type it into **Command** under **Server**, and press Return. Empty, the site runs `npm run dev` again.
:::

:::cli
```bash
flock node serve --command "pnpm dev --port {port}"
flock node serve --command ""   # npm run dev again
```
:::

Unless it has a command of its own, the project needs a `dev` script in its `package.json`. Vite only answers the hosts it knows: Flock tells it the site's domains. Each git worktree of the app runs a dev server of its own, at its own address (see [Git worktrees](worktrees.md)). When the dev server can't run, for example because another app holds its port, the site's domains say why (with a 503) and never reach that app. Like a project's build scripts, the dev server never runs as root.

[Sharing](sharing.md) a Node.js app shares its dev server: visitors see what you see, its error overlay and source maps included (env files such as `.env.local` or `.envrc`, and the git repository, are never served, at any depth). The dev server gets the site's own name as the host, so Vite and Next.js take the visitors' requests, hot reload included.
