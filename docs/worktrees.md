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

This creates the worktree next to the site, on a new branch from the current commit (`--from main` starts it elsewhere; a branch that exists is checked out), and waits until Flock has set it up. `--path` puts it somewhere else, and `--db copy` or `--db fresh` gives it a [database](#databases) of its own.
:::

Worktrees made any other way work the same: `git worktree add`, or a tool that uses worktrees, such as an agent app. Within a few seconds, Flock serves the worktree and sets it up.

The branch name becomes the subdomain: `/`, `_` and `.` turn into `-`, and other characters are dropped. When two branches end up with the same name, the second gets a number (`feature-auth-2`). Once Flock has set a worktree up, it keeps its address when you switch its branch. One with a detached `HEAD` is served under its folder's name.

## What Flock sets up

When Flock notices a new worktree, it:

1. copies `vendor/` and `node_modules/` from the site, copy-on-write: instantly and without using extra disk space until files change (APFS on macOS, btrfs or XFS on Linux). On other disks they are installed instead.
2. copies the site's `.env` (or the worktree's `.env.example`) and points it at the worktree: `APP_URL`, and `SESSION_DOMAIN` and `SANCTUM_STATEFUL_DOMAINS` where they name the site.
3. runs `composer install`, unless the lock file is the site's. A new Laravel `.env` gets an app key.
4. sets up the [database](#databases): by default the worktree uses the site's.
5. runs `npm install` (or pnpm, yarn or bun, by lock file), unless the lock file is the site's.
6. runs your `after_create` hook (below).

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

## Databases

A worktree uses the site's database unless you give it one of its own:

| Mode | The worktree's database |
| --- | --- |
| `shared` | The site's (the default). Migrations you run in the worktree change the site's database too. |
| `copy` | A copy of the site's, with the branch's new migrations run on it |
| `fresh` | An empty one, with the migrations run |

Flock reads `DB_CONNECTION`, `DB_HOST`, `DB_PORT` and `DB_DATABASE` from the site's `.env`. A database of its own works with the PostgreSQL, MySQL and MariaDB that Flock runs, and with SQLite. Other servers, like one in Docker or on another machine, are always shared.

| | PostgreSQL | MySQL and MariaDB | SQLite |
| --- | --- | --- | --- |
| `copy` | Copied from the site's database instantly; while the site's app is connected to it, dumped and restored instead | `mysqldump`, restored into the new database | The file, copy-on-write |
| `fresh` | A new database | A new database | A new empty file |

The worktree's database is named after the site's and the branch, e.g. `acme_feature_auth`, and `DB_DATABASE` in the worktree's `.env` names it. A SQLite file goes to the same place in the worktree as in the site. When a database by that name exists already and Flock didn't make it, Flock leaves it alone and the worktree uses the site's; the setup's log says so, and so it does when Flock can't make the database at all.

Choose the mode for a project in `flock.yml` (`database: copy`), or for one worktree with `--db`.

:::cli
Switch the database of a worktree that's set up, the one you're in by default:

```bash
flock worktree db copy
flock worktree db shared feature/auth   # back to the site's
```

The database Flock made for the worktree before is dropped (after asking), unless you add `--keep-db`.
:::

When a worktree's folder is removed (by git, your agent app or deleting it), its PostgreSQL, MySQL or MariaDB database is kept, in case you need its data; a SQLite file in the worktree goes with the folder. `flock worktree list` lists these databases, and `flock worktree remove <path>` drops one. When a worktree comes back at the same path, it gets its database back.

## flock.yml

Settings for a project's worktrees go into `flock.yml` in the project, under `worktrees:`. Commit it, so everyone working on the project gets them. Settings only for your machine go into `flock.local.yml` next to it; keep that one out of git.

```yaml
worktrees:
  auto: true                       # set new worktrees up by themselves (false: only with `flock worktree setup`)
  path: "../{{folder}}-{{branch}}" # where `flock worktree add` creates them
  database: shared                 # shared, copy or fresh
  migrate: true                    # run the migrations for a copy or fresh database
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

Hooks run line by line in the worktree, with the worktree's PHP and Node.js versions, through `sh` (`cmd` on Windows). The first line that fails stops the hook; the worktree is still served. They get `FLOCK_WORKTREE_PATH`, `FLOCK_WORKTREE_BRANCH`, `FLOCK_WORKTREE_DOMAIN`, `FLOCK_WORKTREE_URL`, `FLOCK_WORKTREE_PARENT` and `FLOCK_WORKTREE_DATABASE` (the worktree's own database, empty when it uses the site's); in hooks, `{{database}}` is that too.

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

`flock worktree remove` runs the `before_delete` hook, removes the worktree with git and drops the database Flock made for it. The branch stays. `--force` removes it even when it has changes or the hook fails, and `--keep-db` keeps its database.
:::

A worktree removed any other way (`git worktree remove`, your agent app, or deleting its folder) stops being served within a few seconds.
