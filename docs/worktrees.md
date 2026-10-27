# Git worktrees

Every git worktree of a site is served as a site of its own, at `<branch>.<site>.test`, and Flock sets it up for you: dependencies, `.env` and the project's own setup commands. Work on several branches at once, or let coding agents work in parallel, each with its own URL.

## Creating a worktree

:::app
The apps don't list worktrees yet. Create them with git, your agent app or the CLI, then open them at `<branch>.<site>.test`.
:::

:::cli
```bash
cd ~/Code/acme
flock worktree add feature/auth
# ready: ~/Code/acme-feature-auth → https://feature-auth.acme.test
```

This creates the worktree next to the site, on a new branch from the current commit (`--from main` starts it elsewhere; a branch that exists is checked out), and waits until Flock has set it up. `--path` puts it somewhere else.
:::

Worktrees made any other way work the same: `git worktree add`, or a tool that uses worktrees, such as an agent app. Within a few seconds, Flock serves the worktree and sets it up.

The branch name becomes the subdomain: `/`, `_` and `.` turn into `-`, and other characters are dropped. When two branches end up with the same name, the second gets a number (`feature-auth-2`). Once Flock has set a worktree up, it keeps its address when you switch its branch. One with a detached `HEAD` is served under its folder's name.

## What Flock sets up

When Flock notices a new worktree, it:

1. copies `vendor/` and `node_modules/` from the site, copy-on-write: instantly and without using extra disk space until files change (APFS on macOS, btrfs or XFS on Linux). On other disks they are installed instead.
2. copies the site's `.env` (or the worktree's `.env.example`) and points it at the worktree: `APP_URL`, and `SESSION_DOMAIN` and `SANCTUM_STATEFUL_DOMAINS` where they name the site. A SQLite database is shared with the site: `DB_DATABASE` gets the site's database file.
3. runs `composer install` and `npm install` (or pnpm, yarn or bun, by lock file), unless the lock file is the site's. A new Laravel `.env` gets an app key.
4. runs your `after_create` hook (below).

Nothing the worktree already has is copied over, and the `.env` is only changed where it says above. `public/build` isn't copied, so the worktree never serves the site's built assets: run `npm run dev` in it, or build in a hook.

Worktrees that existed before your Flock version could set them up are served, but left as they are. To set one up, run `flock worktree setup` in it.

:::cli
Follow a setup, or start it, from inside the worktree:

```bash
flock worktree setup
```

It shows what Flock does and waits until it's done. A worktree that is set up already is left alone; `--again` sets it up once more. In scripts, it exits with `0` when the worktree is ready, `1` when a step failed (the worktree is still served) and `3` when the folder isn't a worktree of a Flock site.
:::

The log of each setup is in `flock logs worktree-<branch>.<site>`.

## flock.yml

Settings for a project's worktrees go into `flock.yml` in the project, under `worktrees:`. Commit it, so everyone working on the project gets them. Settings only for your machine go into `flock.local.yml` next to it; keep that one out of git.

```yaml
worktrees:
  auto: true                       # set new worktrees up by themselves (false: only with `flock worktree setup`)
  path: "../{{folder}}-{{branch}}" # where `flock worktree add` creates them
  copy:                            # files git doesn't track, copied from the site
    - storage/app/private
    - .env.testing
  env:                             # values written into the worktree's .env
    REDIS_PREFIX: "{{branch}}_"
  hooks:
    after_create:
      - php artisan storage:link
      - npm run build
    before_delete:
      - php artisan horizon:terminate
```

| Placeholder | Value |
| --- | --- |
| `{{branch}}` | The name the worktree is served under, e.g. `feature-auth` |
| `{{domain}}` | `feature-auth.acme.test` |
| `{{url}}` | `https://feature-auth.acme.test` |
| `{{scheme}}` | `http` or `https` |
| `{{parent}}` | The site's name, `acme` |
| `{{parent_domain}}` | `acme.test` |
| `{{folder}}` | The site's folder name |

The worktree's own `flock.yml` counts (it comes with the branch), then the site's `flock.local.yml`, then the worktree's. `copy` and the hooks add up, `env` values are merged by name, and other settings are replaced.

Hooks run line by line in the worktree, with the worktree's PHP and Node.js versions, through `sh` (`cmd` on Windows). The first line that fails stops the hook; the worktree is still served. They get `FLOCK_WORKTREE_PATH`, `FLOCK_WORKTREE_BRANCH`, `FLOCK_WORKTREE_DOMAIN`, `FLOCK_WORKTREE_URL` and `FLOCK_WORKTREE_PARENT`.

Without a `REDIS_PREFIX`, worktrees share the site's cache and queues on Redis: a queue worker in a worktree can pick up the site's jobs.

## Settings and domains

A worktree uses its site's PHP and Node.js versions, HTTPS and document root. If the branch pins other versions in `.flockrc`, `.nvmrc` or `.node-version`, those apply, unless the site is [isolated](project-versions.md). To change a worktree's settings, change them on its site. Worktrees are always served through PHP-FPM, also when their site runs on FrankenPHP.

The worktree's own subdomains reach it too: `tenant1.feature-auth.acme.test`. Every other subdomain of `acme.test` still reaches `acme`. If you added `feature-auth` to `acme` as an [extra domain](domains.md), the worktree takes that name over.

Worktrees can be anywhere on disk. One that sits in a [project folder](sites.md#parking-a-folder) next to its site is served as the worktree, not as a site of its own. A folder you link yourself with `flock link` stays a site of its own.

## Listing and removing worktrees

:::cli
```bash
flock worktree list              # every site's worktrees
flock worktree list --site acme
flock worktree open feature/auth # by branch, name or domain; the one you're in by default
flock worktree remove feature/auth
```

`flock worktree remove` runs the `before_delete` hook, then removes the worktree with git. The branch stays. `--force` removes it even when it has changes or the hook fails.
:::

A worktree removed any other way (`git worktree remove`, your agent app, or deleting its folder) stops being served within a few seconds.
