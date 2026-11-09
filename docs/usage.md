# CLI usage

[Back to GarminDaily](../README.md) · [Deployment](deployment.md) · [Data model](data-model.md)

## Configuration

Secrets are read from the environment, never command-line arguments. The binary
does **not** load `.env` itself; use Compose, `docker run --env-file`, or your
process manager to supply it.

| Variable | Required by | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | Optional | Complete or partial database connection settings. Omit it when using only `PG*`. |
| `GARMIN_API_KEY` | `sync`, `sync-activities` | Garmin Connect Personal API key. |
| `GARMIN_RETRY_MAX_WAIT` | Optional | Maximum delay per Garmin retry, as a duration such as `2m` or `150s`. Defaults to `2m`; must be positive. |
| `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD` | Optional | Supply connection fields omitted from `DATABASE_URL`; useful for separate host and credential secrets. |
| `PGDATABASE`, `PGSSLMODE` | Optional | Supply the database or TLS mode when not explicitly set in `DATABASE_URL`. |

Database commands (`migrate`, or either sync command without `--dry-run`) accept
three configuration options:

- **Option A — URL:** all connection settings in `DATABASE_URL`.
- **Option B — PG variables:** all settings in `PG*`, with `DATABASE_URL` omitted.
- **Option C — mixed:** some settings in `DATABASE_URL`, with omitted fields supplied by `PG*`.

The [quick start](../README.md#1-set-your-connection-details) shows complete `.env`
examples for all three. In Option C, **fields explicitly set in `DATABASE_URL`
override the corresponding `PG*` variables**. For example, put host, port, database
and TLS mode in the URL and credentials in `PGUSER` / `PGPASSWORD`.
`GARMIN_API_KEY` remains separate from the database connection settings.

| Command | Behavior |
| --- | --- |
| `migrate` | Create or upgrade all six data tables transactionally; track applied versions in `schema_migrations`. Repeating is safe. |
| `sync --from YYYY-MM-DD --to YYYY-MM-DD` | Import daily data, activities and weights for an inclusive range of Garmin calendar dates. |
| `sync --last-days N --timezone IANA_ZONE` | Import daily data, activities and weights for today and the previous `N−1` dates in that timezone. |
| `sync --last-days N --timezone IANA_ZONE --exclude-today` | Import daily data, activities and weights for yesterday and the previous `N−1` completed dates in that timezone. |
| `sync-activities --from YYYY-MM-DD --to YYYY-MM-DD` | Import activities only for an inclusive range. |
| `sync-activities --last-days N --timezone IANA_ZONE [--exclude-today]` | Import activities only for the selected rolling range; useful for backfills. |
| Either sync command with `--dry-run` | Fetch and validate the same data, then print incoming PostgreSQL rows as JSON Lines without database access. |

There is no default date range. The two range modes cannot be combined.
`--last-days` must be positive and requires a named IANA timezone or `UTC`;
`Local` is rejected. `--timezone` and `--exclude-today` are only valid with an
explicit `--last-days` range. `--exclude-today` uses local calendar dates, so it
ends yesterday regardless of the execution time or daylight-saving changes.
Timezone data is embedded in the binary.

A successful `sync` reports each daily date, then activity and weight aggregates:

```text
day=2026-10-25 changed_rows=3
activities=4 changed_rows=4
weights=2 changed_rows=2
```

An identical repeat reports `changed_rows=0`. The weight count is the number of
measurements after combining compatible entries with the same timestamp.
Activity output contains counts only, not activity names, IDs or body-weight
values. Daily records commit one day at a time. The
activity API pages are all fetched and validated before one transaction writes
the requested activity range and its exercise snapshots. If activity fetching
or writing fails, `sync` exits nonzero; already committed daily dates remain,
and the activity range has no partial writes. Weights are fetched and committed
after activities, in their own range transaction; a failed weight step leaves
earlier daily/activity commits intact. Repeat the range to resume. Run
only one writer against the same tables at a time.

## Inspect data before database import

`sync --dry-run` uses the same requests, validation, activity filtering and
column/value mapping as normal `sync`. It prints one JSON object per incoming
row, with the table name and PostgreSQL column values. Weight is already in kg;
missing values are `null`. It also prints exercise snapshot cleanup predicates.

```sh
# Supply GARMIN_API_KEY through the environment; DATABASE_URL is not needed.
garmin-daily sync --dry-run --from 2026-10-25 --to 2026-10-31

# Redirect stdout if you want to keep the preview locally.
garmin-daily sync --dry-run --from 2026-10-25 --to 2026-10-31 > /tmp/garmin-preview.jsonl
```

The rolling-date flags and `sync-activities --dry-run` work the same way.
Dry-run does not read the database, so it cannot identify unchanged rows or
verify permissions and SQL writes. Its output contains personal health data;
keep saved previews outside the repository. See the [dry-run reference](dry-run.md).

## Try it locally with Compose

Compose provides a **disposable development database**, not a production setup.
Use `.env.example` as a template for your local `.env`; preserve any existing file.
Compose reads `.env` automatically.

```sh
docker compose up -d --wait db
docker compose build app
docker compose run --rm app migrate
docker compose run --rm app sync --from 2026-10-25 --to 2026-10-31
```

The database listens on `127.0.0.1:15432`. Its example credentials are for local
use only. Run `migrate` before importing; repeating it applies no changes.

## Container images

Images are published to
[`ghcr.io/zewelor/garmin-daily`](https://github.com/users/zewelor/packages/container/package/garmin-daily)
for **Linux amd64 and arm64**.

| Reference | Meaning |
| --- | --- |
| `latest` | Most recently verified and published commit from the default branch. |
| `<full-commit-SHA>` | Image built from a specific commit. |
| `@sha256:…` | Exact published image digest; use this to pin deployments. |

CI verifies formatting, `go vet`, unit tests, PostgreSQL integration tests, the
container build and a container migration before publishing. Pull requests do
not receive package-write permissions and never publish images. A manual
`workflow_dispatch` run on the default branch can repeat verification and publication.

`docker/metadata-action` generates tags and OCI source/revision labels. Publishing
uses `GITHUB_TOKEN`; no separate registry secret is needed. The published digest
appears in the job summary. Layer caching is enabled, and cleanup retains 10
recent tagged and 10 untagged versions while protecting `latest`. Older versions
may be removed, so a commit tag is not a promise of indefinite retention.

The image contains no API keys, database settings, account data or `.env` files.
The production build context allows only application source, module files, the three
embedded SQL migrations and the license; tests are excluded.
