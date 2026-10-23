# PostgreSQL

Flock runs PostgreSQL natively, from builds made for Flock. You can install several major versions, each with its own databases, and switch which one is active.

## Installing

:::app
Open **Settings…** from the Flock panel, go to **Services**, switch the filter to **All** and select **PostgreSQL**. Pick a **Version** (the newest is preselected) and a **Port**, then click **Install PostgreSQL 18.x** (the button shows the exact release).

![PostgreSQL in Settings → Services](screenshot:settings-services-postgres)
:::

:::cli
```bash
flock services install postgres        # PostgreSQL 18
flock services install postgres 16     # a specific major
flock services list postgres
```
:::

PostgreSQL listens on `127.0.0.1:5432` as user `postgres`, without a password.

```bash
DB_CONNECTION=pgsql
DB_HOST=127.0.0.1
DB_PORT=5432
DB_DATABASE=laravel
DB_USERNAME=postgres
DB_PASSWORD=
```

## Creating databases

:::app
Creating a database isn't in the app yet. Use the CLI command below, your database client, or let `php artisan migrate` create the database when it asks.
:::

```bash
flock services create-db postgres acme acme_testing
```

PostgreSQL has to be installed and running.

## Switching versions

Only one major version runs at a time. Each version keeps its own data directory, so switching back brings back your databases. Installing another major doesn't switch to it, and when you uninstall the active one, the newest other installed major takes over.

:::app
In the PostgreSQL detail, click **Switch…** next to the version you want and confirm with **Switch**. The card at the top shows the **Active** version and the size of its databases.
:::

:::cli
```bash
flock services use postgres 17
```
:::

## Updating and removing

:::app
Click **Update to …** next to a version to get its newest minor release (or a new build of the one you have), and **Check for Updates** to look for new ones. On macOS, the active version's update is the **Update** button in the card at the top. A build that turns out to be broken is replaced by a fixed one automatically, and your databases stay. To remove a version, open its **⋯** menu and choose **Uninstall PostgreSQL 16…** (macOS) or **Uninstall…** (Windows). Tick **Also delete its databases** to remove its data as well.
:::

:::cli
```bash
flock services update postgres 18      # newest 18.x
flock services uninstall postgres 16   # keeps the data
flock services uninstall postgres 16 --delete-data
```
:::

## Client tools

`psql`, `pg_dump`, `pg_restore`, `pg_dumpall`, `createdb` and `dropdb` come with PostgreSQL. They're on your PATH and connect to Flock's server by default (`PGHOST`, `PGPORT` and `PGUSER` still win when you set them).

With several versions installed, the tools of the newest one are used: `pg_dump` has to be at least as new as the server it dumps, so those work with every version and with remote servers.

When no PostgreSQL is installed yet, running `psql` in a terminal offers to install PostgreSQL 18 and start it.
