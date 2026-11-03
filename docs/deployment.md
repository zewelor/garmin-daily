# Deployment

GarminDaily runs one command and exits. Use cron, a systemd timer or a Kubernetes
CronJob to schedule it. It does not require a specific PostgreSQL operator,
secret manager or GitOps system.

## Prepare the database

PostgreSQL **18 or newer** is required. `migrate`, `sync` and `sync-activities`
reject older servers before modifying the database. Dry-run does not connect
to PostgreSQL.

1. Create a PostgreSQL database or schema for one Garmin account, with a role
   allowed to create its tables. Additional accounts need separate databases
   or schemas.
2. Supply `DATABASE_URL` and `GARMIN_API_KEY` through your secret manager or
   process environment. Keep real values out of Git and command-line arguments.
3. Pin `ghcr.io/zewelor/garmin-daily` to the digest in the publication job summary.
4. Run `migrate` against the selected database/schema. It creates or upgrades
   all six data tables and records versions in `schema_migrations`, in one
   transaction. Repeating it is safe; run it before syncing a new release.
5. Import an explicit range with `sync --from YYYY-MM-DD --to YYYY-MM-DD`, check
   the daily dates, activity and weight summaries, then repeat it. With unchanged
   source data, all writes should report `changed_rows=0`.

Use the connection string's `search_path` parameter to select a schema. pgx also
accepts `PGHOST`, `PGUSER` and `PGPASSWORD`, so credentials need not be embedded
in the URL. Configure TLS for your server; the `sslmode=disable` setting in
Compose is for local testing only.

## Upgrade an existing database

Run `migrate` with the schema-owner role before using this release. This is the
same command used for fresh installations. It recognizes the three previously
released schema versions (daily tables; daily plus activities; all monitoring
fields) after checking their structure, records their versions and applies only
pending migrations. Existing data is retained; new measurement columns begin as
`NULL`. Re-import a date range to populate them.

Migrations are transactional and serialized per schema. A repeated run applies
no changes. An incomplete, modified or unknown schema, or an unsupported version
history, fails explicitly; inspect the discrepancy before retrying. Extra
unrelated tables do not affect migration recognition.

The migration role needs permission to alter existing tables and create tables.
Grant the runtime writer access to `weight_measurements` and grant `SELECT` to
any existing read-only role, using the same roles as the other data tables.
Migration metadata does not require runtime writer access.

## Schedule synchronization

For example:

```sh
garmin-daily sync --last-days 3 --timezone Europe/London
```

`sync` imports daily rows first, committing each date, then synchronizes all
activities whose source-local start date falls within the same inclusive range.
It fetches and validates every activity page before writing, then commits the
activity summaries and exercise snapshots in one range transaction. Finally,
it fetches all weight measurements for that range and upserts them in a separate
transaction. An API or database failure exits nonzero while earlier committed
steps remain; each activity/weight batch has no partial writes.

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
