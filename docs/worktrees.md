# Git worktrees

Every git worktree of a site is served as a site of its own, at `<branch>.<site>.test`. Work on several branches at once, or let coding agents work in parallel, each with its own URL.

## Serving a worktree

Create the worktree the way you normally do, with git or with a tool that uses worktrees, such as an agent app:

```bash
cd ~/Code/acme
git worktree add -b feature/auth ../acme-feature-auth
```

Within a few seconds, Flock serves it at `feature-auth.acme.test`. The branch name becomes the subdomain: `/`, `_` and `.` turn into `-`, and other characters are dropped. When two branches end up with the same name, the second gets a number (`feature-auth-2`). A worktree with a detached `HEAD` is served under its folder's name.

A worktree uses its site's PHP and Node.js versions, HTTPS and document root. If the branch pins other versions in `.flockrc`, `.nvmrc` or `.node-version`, those apply, unless the site is [isolated](project-versions.md). To change a worktree's settings, change them on its site. Worktrees are always served through PHP-FPM, also when their site runs on FrankenPHP.

The worktree's own subdomains reach it too: `tenant1.feature-auth.acme.test`. Every other subdomain of `acme.test` still reaches `acme`. If you added `feature-auth` to `acme` as an [extra domain](domains.md), the worktree takes that name over.

Worktrees can be anywhere on disk. One that sits in a [project folder](sites.md#parking-a-folder) next to its site is served as the worktree, not as a site of its own. A folder you link yourself with `flock link` stays a site of its own.

When you remove a worktree (`git worktree remove`, or by deleting its folder), Flock stops serving it.

:::app
The apps don't list worktrees yet. Open them in your browser at `<branch>.<site>.test`, or use the CLI.
:::

:::cli
List the worktrees of every site, or of one:

```bash
flock worktree list
flock worktree list --site acme
```

Open a worktree in your browser, the one you're in or one by name (its branch, the name it's served under, or its domain):

```bash
flock worktree open
flock worktree open feature/auth
```
:::

## Things to set in the worktree

A new worktree only has what's committed. Until Flock does it for you, do this in the worktree yourself:

- copy the site's `.env`, and set `APP_URL` to the worktree's URL,
- run `composer install` and `npm install`,
- run `php artisan storage:link` if the app uses it.

The worktree uses the same database as its site unless you change `DB_DATABASE` in its `.env`.
