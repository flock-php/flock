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
Click **Update to …** next to a version to get its newest minor release (or a new build of the one you have), and **Check for Updates** to look for new ones. On macOS, the active version's update is the **Update** button in the card at the top. A build that turns out to be broken is replaced by a fixed one automatically, and your databases stay. To remove a version, open its **⋯** menu and choose **Uninstall PostgreSQL 16…** (macOS) or **Uninstall…** (Windows). Tick **Also delete its databases** to remove its data as well. On Linux, **Check now** looks for new versions, and **Uninstall…** asks whether to keep the databases (**Uninstall, Keep Databases** or **Uninstall and Delete Databases**).
:::

:::cli
```bash
flock services update postgres 18      # newest 18.x
flock services uninstall postgres 16   # keeps the data
flock services uninstall postgres 16 --delete-data
```
:::

## Extensions

Flock offers PostGIS, pgRouting, pgvector and TimescaleDB, each as a download of its own. You pick them per version, since not every extension is built for every version.

| Extension | What it adds | `CREATE EXTENSION` | Versions |
|---|---|---|---|
| PostGIS | geographic types, spatial indexes and queries | `postgis` | 14–18 |
| pgRouting | shortest paths and routing on PostGIS networks | `pgrouting` | 14–18 |
| pgvector | vector similarity search for embeddings | `vector` | 14–18 |
| TimescaleDB | hypertables, compression and continuous aggregates | `timescaledb` | 16–18 |

:::app
Click **Extensions…** next to a version, then **Install** next to the extension. When you install another version, Flock offers to install the extensions of the active one with it.
:::

:::cli
```bash
flock services extensions postgres                    # the active version's extensions
flock services extensions postgres install postgis    # for the active version
flock services extensions postgres install timescaledb --major 17
flock services extensions postgres uninstall pgvector
flock services install postgres 19 --with-extensions  # a new version with the active one's extensions
```
:::

Then enable it in each database that uses it, for example in a migration:

```php
DB::statement('CREATE EXTENSION IF NOT EXISTS postgis');
```

Updates keep the extensions of a version. Uninstalling a version removes its extensions, but not your databases unless you choose so.

TimescaleDB is loaded when PostgreSQL starts, so installing or removing it restarts that version. Flock turns its telemetry off. TimescaleDB's compression, continuous aggregates and policies are under the [Timescale License](https://github.com/timescale/timescaledb/blob/main/tsl/LICENSE-TIMESCALE), which allows using them for development but not offering TimescaleDB as a database service.

PostGIS comes without raster support (`postgis_raster`) and without `ST_AsMVT` and `ST_AsGeobuf`.

## Your own extensions

Every version has a folder for extensions Flock doesn't offer. Flock makes what you put there available to that version and keeps it across updates and when you uninstall Flock. The library goes in `lib/`, the `.control` and `.sql` files in `extension/`.

:::app
Click **Extensions…** next to a version, then **Open Extensions Folder**.
:::

:::cli
```bash
flock services extensions postgres dir --open
```
:::

On macOS and Linux, build an extension with PGXS against Flock's PostgreSQL and install it into that folder:

```bash
PG_CONFIG=$(flock services extensions postgres pg-config)
FOLDER=$(flock services extensions postgres dir)
make PG_CONFIG=$PG_CONFIG
make install PG_CONFIG=$PG_CONFIG pkglibdir="$FOLDER/lib" datadir="$FOLDER"
```

On Windows, put the extension's DLL for that PostgreSQL version in `lib\`. On Windows and Linux, libraries the extension needs go in `deps`; on Linux, PostgreSQL restarts once to pick them up.

New files are picked up within a few seconds; then run `CREATE EXTENSION`. An extension that has to be preloaded (`shared_preload_libraries`) isn't supported from this folder.

## Client tools

`psql`, `pg_dump`, `pg_restore`, `pg_dumpall`, `createdb` and `dropdb` come with PostgreSQL. They're on your PATH and connect to Flock's server by default (`PGHOST`, `PGPORT` and `PGUSER` still win when you set them).

With several versions installed, the tools of the newest one are used: `pg_dump` has to be at least as new as the server it dumps, so those work with every version and with remote servers.

When no PostgreSQL is installed yet, running `psql` in a terminal offers to install PostgreSQL 18 and start it.
