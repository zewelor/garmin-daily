# GarminDaily

Sync daily Garmin Connect health, sleep and HRV metrics to PostgreSQL.

[![CI](https://github.com/zewelor/garmin-daily/actions/workflows/ci.yml/badge.svg)](https://github.com/zewelor/garmin-daily/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/zewelor/garmin-daily?logo=go)](go.mod)
[![GHCR](https://img.shields.io/badge/GHCR-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)](https://github.com/users/zewelor/packages/container/package/garmin-daily)
[![License](https://img.shields.io/github/license/zewelor/garmin-daily)](LICENSE)

[Quick start](#quick-start) · [Configuration](#configuration) · [Data model](docs/data-model.md) · [Deployment](docs/deployment.md) · [Development](#development)

GarminDaily is a small, run-once CLI built around the official **Garmin Connect
Personal API**. It authenticates, imports an explicit date range and exits.
Run it manually or schedule it with cron, systemd or a Kubernetes CronJob.

- **Three daily tables** for health, sleep and HRV, with one row per date.
- **Repeatable imports** that apply corrections and skip unchanged rows.
- **Atomic daily writes**: a failed day leaves no partial records.
- **Inspection before storage**: `sync --dry-run` downloads and validates data
  without connecting to PostgreSQL; optional files retain the original responses.
- **Small container**: a static Go binary, CA certificates and license in
  `scratch`, running as UID/GID `65532:65532`; database sync requires no writable filesystem.

Each database or schema belongs to **one Garmin account**. The application does
not manage users, run a web server or store intraday samples in PostgreSQL.

## Quick start

You need a Garmin **Personal API key** and a reachable PostgreSQL database.
A Garmin Connect password is not a Personal API key.

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

# Run once against an empty database/schema.
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --env-file .env ghcr.io/zewelor/garmin-daily:latest migrate

# Import today and the previous six calendar dates in your Garmin timezone.
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --env-file .env ghcr.io/zewelor/garmin-daily:latest \
  sync --last-days 7 --timezone Europe/London
```

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
use only. Run `migrate` once; running it again reports that the tables exist.

## Configuration

Secrets are read from the environment, never command-line arguments. The binary
does **not** load `.env` itself; use Compose, `docker run --env-file`, or your
process manager to supply it.

| Variable | Required by | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | `migrate`, `sync` without `--dry-run` | PostgreSQL connection string and TLS settings. |
| `GARMIN_API_KEY` | `sync` | Garmin Connect Personal API key. |
| `PGHOST`, `PGUSER`, `PGPASSWORD` | Optional | Standard pgx connection parameters; useful for keeping credentials out of the URL. |

| Command | Behavior |
| --- | --- |
| `migrate` | Create the initial schema in one transaction. Run once on an empty database/schema. |
| `sync --from YYYY-MM-DD --to YYYY-MM-DD` | Import an inclusive range of Garmin calendar dates. |
| `sync --last-days N --timezone IANA_ZONE` | Import today and the previous `N−1` dates in the chosen timezone. |
| `sync --last-days N --timezone IANA_ZONE --exclude-today` | Import yesterday and the previous `N−1` completed dates in the chosen timezone. |
| `sync --dry-run` with either date-range mode | Download and validate monitoring data without database access. Add `--output NEW_DIRECTORY` to retain private inspection files. |

There is no default date range. The two range modes cannot be combined.
`--last-days` must be positive and requires a named IANA timezone or `UTC`;
`Local` is rejected. `--timezone` and `--exclude-today` are only valid with an
explicit `--last-days` range. `--exclude-today` uses local calendar dates, so it
ends yesterday regardless of the execution time or daylight-saving changes.
Timezone data is embedded in the binary.

A successful sync exits with code `0` and reports each day:

```text
day=2026-10-25 changed_rows=3
```

An identical repeat reports `changed_rows=0`. An error exits with code `1` and
stops the range; earlier committed days remain. Repeat the range to resume.
Run only one writer against the same tables at a time.

## Inspect data before database import

`sync --dry-run` reads health, sleep, HRV, every weight measurement, current heart-rate
zone settings and all pages of recorded activities. Activity metrics include
time in heart-rate zones, intensity minutes, calories, Training Effect,
training load and VO₂max when Garmin supplies them. The health summary also
includes daily respiration, SpO₂ and the distribution of stress durations.

```sh
# Supply GARMIN_API_KEY through the environment; DATABASE_URL is not needed.
garmin-daily sync --dry-run --from 2026-10-25 --to 2026-10-31

# Optionally keep original responses and decoded data for inspection.
garmin-daily sync --dry-run --from 2026-10-25 --to 2026-10-31 \
  --output /tmp/garmin-inspection
```

With `--output`, the directory must not exist. It is created with mode `0700`, with
`0600` files: original GET responses, selected decoded data in `data.json`,
and a final `manifest.json` containing request paths, statuses and SHA-256 hashes.
API keys, bearer tokens and request headers are not saved. A failed dry run keeps
its diagnostic files without a success manifest. These files contain personal
health data; keep them outside the repository.

The existing rolling-date flags also work with `--dry-run`. This mode never
connects to PostgreSQL. Without `--output`, it prints counts and creates no files.
**The additional metrics are not yet written by normal `sync`.**
See [dry-run reference](docs/dry-run.md) for units, selected fields and verification
limits, including discrepancies between Swagger and live weight responses.

## Data model

| Table | Contents |
| --- | --- |
| `daily_health` | Steps, distance, calories, heart rate, intensity, stress and Body Battery. |
| `daily_sleep` | Sleep timestamps, duration, stages, naps, score and overnight averages. |
| `daily_hrv` | Nightly and weekly HRV, status and baseline values reported by Garmin. |

All tables use `day DATE` as the primary key. Missing values remain SQL `NULL`;
zero remains zero. Decimal measurements retain their source precision without
passing through `float64`. Each day's three responses are fetched and validated
before a single database transaction writes them.

The [data model reference](docs/data-model.md) documents every stored field, unit,
validation rule and HTTP retry policy. No raw responses, account identifiers or
device metadata are persisted in PostgreSQL. Trends can be queried directly in SQL.

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
The production build context allows only application source, module files,
the embedded schema and the license; tests are excluded.

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
