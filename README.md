# GarminDaily

Sync Garmin Connect health, sleep, HRV, activities and weight measurements to PostgreSQL.

[![CI](https://github.com/zewelor/garmin-daily/actions/workflows/ci.yml/badge.svg)](https://github.com/zewelor/garmin-daily/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/zewelor/garmin-daily?logo=go)](go.mod)
[![GHCR](https://img.shields.io/badge/GHCR-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)](https://github.com/users/zewelor/packages/container/package/garmin-daily)
[![License](https://img.shields.io/github/license/zewelor/garmin-daily)](LICENSE)

[Quick start](#quick-start) · [Configuration](#configuration) · [Data model](docs/data-model.md) · [Deployment](docs/deployment.md) · [Development](#development)

GarminDaily is a small, run-once CLI built around the official **Garmin Connect
Personal API**. It authenticates, imports an explicit date range and exits.
Run it manually or schedule it with cron, systemd or a Kubernetes CronJob.

- **Six compact tables** for daily health, sleep and HRV, top-level activities,
  workout summaries and weight measurements.
- **Repeatable imports** that apply corrections and skip unchanged rows.
- **Atomic writes**: each daily date, activity range and weight range commit transactionally.
- **Inspection before storage**: `sync --dry-run` downloads and validates monitoring data
  and prints incoming PostgreSQL rows without connecting to the database.
- **Small container**: a static Go binary, CA certificates and license in
  `scratch`, running as UID/GID `65532:65532`; database sync requires no writable filesystem.

Each database or schema belongs to **one Garmin account**. The application does
not manage users or run a web server. PostgreSQL stores typed summaries, without
intraday sensor series, GPS data, raw API responses, account identifiers or device metadata.

## Quick start

You need a Garmin **Personal API key** and a reachable **PostgreSQL 18 or newer** database.
A Garmin Connect password is not a Personal API key.
Database commands reject older PostgreSQL versions before changing the schema or importing data.

### Use the published image

Create a local `.env` file with your own connection details:

```dotenv
GARMIN_API_KEY=your-personal-api-key
DATABASE_URL=postgres://USER:PASSWORD@DB_HOST:5432/garmin_daily?sslmode=require
```

Match TLS settings to your database server. `DB_HOST` must be reachable from the
container: `localhost` refers to the container itself. Keep `.env` out of Git.

```sh
docker pull ghcr.io/zewelor/garmin-daily:latest

# Create or upgrade the schema; repeating this command is safe.
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --env-file .env ghcr.io/zewelor/garmin-daily:latest migrate

# Import today and the previous six calendar dates in your Garmin timezone.
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --env-file .env ghcr.io/zewelor/garmin-daily:latest \
  sync --last-days 7 --timezone Europe/London
```

Use the same `migrate` command for existing installations. See
[deployment](docs/deployment.md#upgrade-an-existing-database).

Choose the IANA timezone that matches your Garmin account. See
[deployment](docs/deployment.md) for scheduling, database roles and backups.

### Try it locally with Compose

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

## Configuration

Secrets are read from the environment, never command-line arguments. The binary
does **not** load `.env` itself; use Compose, `docker run --env-file`, or your
process manager to supply it.

| Variable | Required by | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | `migrate`, either sync command without `--dry-run` | PostgreSQL connection string and TLS settings. |
| `GARMIN_API_KEY` | `sync`, `sync-activities` | Garmin Connect Personal API key. |
| `PGHOST`, `PGUSER`, `PGPASSWORD` | Optional | Standard pgx connection parameters; useful for keeping credentials out of the URL. |

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

An identical repeat reports `changed_rows=0`. Activity output contains counts
only, not activity names, IDs or body-weight values. Daily records commit one day at a time. The
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
keep saved previews outside the repository. See the [dry-run reference](docs/dry-run.md).

## Data model

| Table | Contents |
| --- | --- |
| `daily_health` | Steps, distance, calories, heart rate, intensity, stress levels/durations, daily respiration and Body Battery. |
| `daily_sleep` | Sleep timestamps, duration, stages, naps, score and overnight averages. |
| `daily_hrv` | Nightly and weekly HRV, status and baseline values reported by Garmin. |
| `activities` | One row per Garmin activity ID, including source-local date, UTC start instant and seconds in HR zones 1–5. |
| `activity_exercises` | Indexed workout summary elements attached to an activity. |
| `weight_measurements` | Each measurement's UTC timestamp, Garmin date and body mass in kg. |

Daily tables use `day DATE` as the primary key. Activity rows use Garmin's
`activity_id`; exercise rows use `(activity_id, exercise_index)` and weights use
their UTC measurement timestamp. Missing values
remain SQL `NULL`; zero remains zero. Decimal measurements retain their source
precision without passing through `float64`. Each day's three responses are
fetched and validated before a single database transaction writes them.

The [data model reference](docs/data-model.md) documents every field, unit,
validation rule and HTTP retry policy. Activities are upserted by ID; corrections
replace prior values, exercise arrays replace their prior indexed snapshot, and
activities absent from a response are retained. Trends can be queried directly
in SQL.

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

## Development

Use the Go version declared in [go.mod](go.mod). Docker, Compose and `just` are
needed for the container and integration workflows.

```sh
go build -trimpath -o garmin-daily ./cmd/garmin-daily
just unit
go vet ./...
just integration
just docker-build
```

`just integration` creates and removes its own Compose project and test database.
Port `15432` must be free; stop a manually started development database before
running it. Alternatively, supply a dedicated test database:

```sh
TEST_DATABASE_URL='postgres://…' go test -race -tags integration -v ./...
```

Tests cover HTTP failures, validation, repeat imports, corrections, missing and
late values, exact decimals, transaction rollback and recovery after a lost
connection. HTTP fixtures are synthetic; this suite does not contact a real
Garmin account.

CLI and PostgreSQL code live in `cmd/garmin-daily/`. The `garmin/` package is an
independent, standard-library-only Personal API client. Renovate maintains
version and digest updates; merging remains manual.

## API references

- [Garmin Personal API and authorization](https://developer.garmin.com/gc-developer-program/personal/personal-api/)
- [API key and token FAQ](https://developer.garmin.com/gc-developer-program/personal/personal-faq/)
- [Official Swagger](https://developer.garmin.com/gc-developer-program/personal/swagger-docs/)
- [Official Python client](https://github.com/garmin/connect-personal-api-python)

## License

[MIT](LICENSE). A copy is included in the container at `/LICENSE`.
