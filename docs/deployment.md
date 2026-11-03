# Deployment

GarminDaily runs one command and exits. Use cron, a systemd timer or a Kubernetes
CronJob to schedule it. It does not require a specific PostgreSQL operator,
secret manager or GitOps system.

## Prepare the database

1. Create a PostgreSQL database or schema for one Garmin account, with a role
   allowed to create its tables. Additional accounts need separate databases
   or schemas.
2. Supply `DATABASE_URL` and `GARMIN_API_KEY` through your secret manager or
   process environment. Keep real values out of Git and command-line arguments.
3. Pin `ghcr.io/zewelor/garmin-daily` to the digest in the publication job summary.
4. Run `migrate` once against the empty database/schema. It creates all five
   tables in one transaction. Running it again is an error; do not include it
   in each scheduled sync.
5. Import an explicit range with `sync --from YYYY-MM-DD --to YYYY-MM-DD`, check
   the daily dates and activity summaries, then repeat it. With unchanged source
   data, daily and activity writes should report `changed_rows=0`.

Use the connection string's `search_path` parameter to select a schema. pgx also
accepts `PGHOST`, `PGUSER` and `PGPASSWORD`, so credentials need not be embedded
in the URL. Configure TLS for your server; the `sslmode=disable` setting in
Compose is for local testing only.

## Upgrade an existing database

An installation created by an earlier GarminDaily release has the three daily
tables. Before running the new `sync`, execute `migrate-activities` once with a
role allowed to create tables in the selected schema. This creates
`activities` and `activity_exercises` together in one transaction and leaves
daily tables and rows untouched. The command fails if either activity table
already exists; do not rerun it after success. Do not run the fresh-install
`migrate` against an existing daily schema because that command creates all
five tables and deliberately fails on existing tables.

If an activity table already exists from a prior attempt, inspect the schema
before syncing. The migration is transactional, so a failed execution does not
leave a newly created half of the activity schema.

## Schedule synchronization

For example:

```sh
garmin-daily sync --last-days 3 --timezone Europe/London
```

`sync` imports daily rows first, committing each date, then synchronizes all
activities whose source-local start date falls within the same inclusive range.
It fetches and validates every activity page before writing, then commits the
activity summaries and exercise snapshots in one range transaction. An API or
database failure in the activity step exits nonzero while earlier daily rows
remain committed; the activity range itself has no partial writes.

To backfill activities without repeating daily requests, run for example:

```sh
garmin-daily sync-activities --last-days 90 --timezone Europe/London
```

`sync-activities` accepts the same inclusive `--from`/`--to` and rolling
`--last-days`/`--timezone`/`--exclude-today` flags as `sync`.

Choose the IANA timezone matching your Garmin account. This imports today and
the previous two calendar dates. To import three completed dates ending
yesterday, use:

```sh
garmin-daily sync --last-days 3 --timezone Europe/London --exclude-today
```

`--exclude-today` requires an explicit `--last-days` range. It selects yesterday
and the previous `N−1` calendar dates in the chosen timezone, regardless of the
execution hour or daylight-saving transitions. Repeating a rolling window picks
up late corrections. Choose the window length and schedule for your own use;
neither is hardcoded in the application.

Allow only one writer against the same tables at a time. For Kubernetes, use:

| Setting | Value or purpose |
| --- | --- |
| `concurrencyPolicy` | `Forbid` |
| `restartPolicy` | `Never` |
| `backoffLimit` | `0` |
| `activeDeadlineSeconds` | An explicit deadline suited to the date range. |

Run manual Jobs sequentially as well; CronJob concurrency policy does not prevent
them from overlapping. Monitor exit codes and logs. A failure stops the range.
Earlier committed daily dates remain; activity changes are committed as one
range and roll back together on a write failure. Rerun the range to resume.

## Container runtime

The image runs as UID/GID `65532:65532`. Use a read-only root filesystem,
drop all capabilities and enable `no-new-privileges`.

Allow DNS resolution, outbound HTTPS to Garmin and connections to PostgreSQL.
The application needs no writable filesystem, persistent volume, Service,
Ingress or HTTP server. CA certificates and IANA timezone data are included.

See the [README](../README.md#quick-start) for `docker run` and local Compose
examples. Publishing the image does not migrate a database or deploy anything
to an existing environment.

## Backups and dashboards

Configure PostgreSQL backups and verify a restore before relying on the stored
history. Use a separate read-only role for dashboards. Field mappings and units
are documented in the [data model](data-model.md).

When replacing another importer, compare the same date range and measurements
first. Switch schedules after verification, preserving the existing history and
a way to resume the previous process.
