# GarminDaily

**Your Garmin history, in PostgreSQL. A clear daily view, in Grafana.**

Sync health, sleep, HRV, workouts and weight through the official Garmin Connect
Personal API. Keep typed measurements in your own database, browse the included
dashboard, or query the history with your own tools.

[![CI](https://github.com/zewelor/garmin-daily/actions/workflows/ci.yml/badge.svg)](https://github.com/zewelor/garmin-daily/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/zewelor/garmin-daily?logo=go)](go.mod)
[![GHCR](https://img.shields.io/badge/GHCR-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)](https://github.com/users/zewelor/packages/container/package/garmin-daily)
[![License](https://img.shields.io/github/license/zewelor/garmin-daily)](LICENSE)

[Quick start](#quick-start) · [Dashboard](#dashboard) · [CLI guide](docs/usage.md) · [Deployment](docs/deployment.md) · [Data model](docs/data-model.md)

![Garmin Daily overview with synthetic data: latest completed night, sleep stages and score, steps, HRV and resting heart rate](docs/screenshots/overview.png)

*Screenshots use entirely synthetic demo data, including all workout names and measurements.*

- **A quick health and recovery overview.** See the latest completed night, then
  follow sleep, HRV, resting heart rate and steps over time. Expand details when needed.
- **History you can query.** Six compact PostgreSQL tables keep measurements and
  units explicit. Missing readings stay missing; zero stays zero.
- **Repeatable imports.** Backfill an inclusive date range, then schedule a rolling
  window to pick up late changes. Unchanged rows are skipped.
- **A small collector.** One Go CLI runs, imports and exits. The container contains
  a static binary, CA certificates and the license, and needs no writable filesystem.

## Dashboard

Start with **“How was my latest night, and what has changed?”** The overview puts
sleep, HRV and personal heart-rate comparisons together. Steps use a configurable
goal and a seven-day average, with a separate color for days below the goal.

One dashboard keeps the rest within reach: stress and energy, health details,
recorded training, movement and expandable daily history. Cards link to the
relevant measurement history; tables expose the underlying daily and workout details.

### Training when you need it

Compare weekly recorded time, inspect workout heart-rate zones and open activity
details. Missing measurements are marked rather than presented as zero effort.
Panel help explains unfamiliar terms such as aerobic and anaerobic training effect.

![Recorded training: weekly totals, time in heart-rate zones, activities and expandable workout details](docs/screenshots/training.png)

**[Import the dashboard →](dashboards/README.md)** · [Download JSON](https://raw.githubusercontent.com/zewelor/garmin-daily/main/dashboards/garmin-daily.json)

The dashboard uses built-in Grafana panels and the PostgreSQL datasource.
Choose your datasource, step goal, reference window and timezone. The default
chart range is four complete Monday–Sunday weeks; the top cards independently
show the latest completed main sleep and its date. The demo uses the portable
JSON defaults, including a configurable 10,000-step goal.

## Quick start

You need a **Garmin Connect Personal API key** and **PostgreSQL 18 or newer**.
A Garmin Connect password is not a Personal API key.

### 1. Set your connection details

Create a local `.env` file and keep it out of Git. Choose one of these three
options for the **database connection settings**:

**Option A — everything in `DATABASE_URL`:**

```dotenv
GARMIN_API_KEY=your-personal-api-key
DATABASE_URL=postgres://garmin_daily:your-database-password@db.example.com:5432/garmin_daily?sslmode=require
```

**Option B — separate PostgreSQL variables:**

```dotenv
GARMIN_API_KEY=your-personal-api-key
PGHOST=db.example.com
PGPORT=5432
PGDATABASE=garmin_daily
PGUSER=garmin_daily
PGPASSWORD=your-database-password
PGSSLMODE=require
```

In Option B, all connection settings come from `PG*`; omit `DATABASE_URL`.

**Option C — mix `DATABASE_URL` and `PG*`:**

Keep the host, port, database and TLS mode in the URL, and supply credentials separately:

```dotenv
GARMIN_API_KEY=your-personal-api-key
DATABASE_URL=postgres://db.example.com:5432/garmin_daily?sslmode=require
PGUSER=garmin_daily
PGPASSWORD=your-database-password
```

In Option C, `PG*` fills in fields omitted from the URL. **Values explicitly set
in `DATABASE_URL` take precedence over the corresponding `PG*` variables.**

Match TLS settings to your server. The database host, whether in the URL or
`PGHOST`, must be reachable from the container; `localhost` refers to the container
itself. Each database or schema holds one Garmin account.

### 2. Create the schema and import history

```sh
docker pull ghcr.io/zewelor/garmin-daily:latest
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges --env-file .env ghcr.io/zewelor/garmin-daily:latest migrate
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges --env-file .env ghcr.io/zewelor/garmin-daily:latest sync --last-days 90 --timezone Europe/London
```

Replace `Europe/London` with the IANA timezone matching your Garmin account.
Choose a history window available from your account; 90 days gives the dashboard
room to show trends and calculate personal reference ranges. Long backfills can
be split into inclusive `--from YYYY-MM-DD --to YYYY-MM-DD` ranges.

Run `migrate` before using a new release; repeating it is safe. Repeating an
unchanged import reports `changed_rows=0`. Allow only one writer at a time.

### 3. Open your dashboard and keep it current

Connect Grafana using a read-only database role, then
[import the dashboard JSON](dashboards/README.md). Grafana connects directly to
PostgreSQL; GarminDaily does not run a web server.

Schedule the same container with cron, a systemd timer or a Kubernetes CronJob.
For recurring imports, use `sync --last-days 3 --timezone Europe/London` to refresh
today and the previous two calendar dates. Add `--exclude-today` to import three
completed dates instead. See [deployment](docs/deployment.md) for scheduling,
database permissions, upgrades and backups.

## What is stored?

| Table | Measurements |
| --- | --- |
| `daily_health` | Steps, distance, calories, heart rate, intensity minutes, stress, respiration and Body Battery. |
| `daily_sleep` | Sleep times, duration, stages, naps, score and overnight averages. |
| `daily_hrv` | Nightly and weekly HRV, status and Garmin's baseline values. |
| `activities` | Recorded workouts, training effect, load and time in heart-rate zones. |
| `activity_exercises` | Indexed exercise summaries attached to a workout. |
| `weight_measurements` | Body mass, measurement timestamp and Garmin date. |

Storage covers typed summaries. Intraday sensor series, GPS tracks, raw API
responses, account identifiers and device metadata are not stored. Available
measurements depend on what Garmin supplies for your device and activity.
The dashboard's personal comparisons describe recorded data; they are not a
medical assessment or a reproduction of Garmin Health Status.

See the [data model](docs/data-model.md) for every field, unit and validation rule.
To inspect incoming rows without connecting to PostgreSQL, use
[`sync --dry-run`](docs/dry-run.md). Its output contains your health data.

## Documentation

| Guide | Use it for |
| --- | --- |
| [Dashboard](dashboards/README.md) | Import, variables, date semantics and file provisioning. |
| [CLI usage](docs/usage.md) | Environment variables, date ranges, local Compose and image tags. |
| [Deployment](docs/deployment.md) | Database setup, scheduling, migrations, upgrades and backups. |
| [Data model](docs/data-model.md) | Tables, units, missing values and import behavior. |
| [Dry-run](docs/dry-run.md) | Preview the rows an import would write. |

## Development

Use the Go version in [go.mod](go.mod). Docker, Compose and `just` support the
container and integration workflows.

```sh
go build -trimpath -o garmin-daily ./cmd/garmin-daily
just unit
go vet ./...
just integration
just docker-build
```

`just integration` creates and removes a disposable Compose database; port
`15432` must be free. Or run against a dedicated test database:

```sh
TEST_DATABASE_URL='postgres://…' go test -race -tags integration -v ./...
```

CLI and PostgreSQL code live in `cmd/garmin-daily/`; `garmin/` is a separate
standard-library-only Personal API client. Tests use synthetic HTTP fixtures and
cover validation, repeat imports, corrections, missing readings, exact decimals,
transaction rollback and connection recovery.

CI runs formatting, vet, unit and PostgreSQL integration checks, then builds and
smoke-tests the container. Verified default-branch builds publish Linux amd64 and
arm64 images to [GHCR](https://github.com/users/zewelor/packages/container/package/garmin-daily).
Use the published digest to pin a deployment; [image tags and retention](docs/usage.md#container-images)
are documented separately.

## References and credits

- [Garmin Personal API](https://developer.garmin.com/gc-developer-program/personal/personal-api/),
  [API key FAQ](https://developer.garmin.com/gc-developer-program/personal/personal-faq/),
  [Swagger](https://developer.garmin.com/gc-developer-program/personal/swagger-docs/) and
  [official Python client](https://github.com/garmin/connect-personal-api-python).
- [garmin-grafana](https://github.com/arpanghosh8453/garmin-grafana) inspired the
  dashboard presentation. This dashboard queries GarminDaily's PostgreSQL schema;
  the projects' dashboard JSON files are not interchangeable.

## License

[MIT](LICENSE). The container includes a copy at `/LICENSE`.
