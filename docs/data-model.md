# Data model

This reference describes the PostgreSQL rows written by `garmin-daily`. See the
[README](../README.md) for setup and operation, and the
[SQL schema](../cmd/garmin-daily/schema.sql) for the exact DDL.

Additional monitoring fields available through `fetch` are documented in the
[fetch reference](fetch.md). They are not yet part of PostgreSQL storage.

## Tables and keys

The schema stores one Garmin account's data in the selected database or
PostgreSQL schema. It has no account or user key, so use a separate database or
schema for each account.

| Table | Primary key | Contents |
| --- | --- | --- |
| `daily_health` | `day DATE` | Garmin daily health summary |
| `daily_sleep` | `day DATE` | Daily sleep summary |
| `daily_hrv` | `day DATE` | Daily HRV summary |

`daily_sleep.day` and `daily_hrv.day` are foreign keys to
`daily_health.day`. All three tables use Garmin's `calendarDate`; sleep is not
split at midnight or a daylight-saving change. The schema does not store
device metadata, raw API responses, intraday samples, or precomputed 7/30/90-day
aggregates. There are no generated IDs, extra indexes, or JSONB columns.
Garmin-provided averages, including weekly HRV, are stored as source metrics;
the importer does not calculate replacement averages. With only a few rows,
PostgreSQL page overhead dominates, so measure storage on a representative
dataset.

## Field mapping

Each database column below maps directly to the named Personal API field;
source measurements are stored without correction. Nested field paths include
their full parent path.

| Table column | Personal API field | Unit or meaning |
| --- | --- | --- |
| all tables: `day` | `calendarDate` | Garmin calendar date; see the no-night sleep exception below |
| `daily_health.steps` | `movement.steps.value` | steps |
| `daily_health.distance_m` | `movement.steps.distanceInMeters` | metres |
| `daily_health.calories_total` | `calories.burnedTotal` | kcal as reported by Garmin |
| `daily_health.calories_active` | `calories.burnedActive` | kcal as reported by Garmin |
| `daily_health.calories_resting` | `calories.burnedResting` | kcal as reported by Garmin |
| `daily_health.heart_rate_resting` | `heartRate.restingValue` | beats per minute |
| `daily_health.heart_rate_min` | `heartRate.minValue` | beats per minute |
| `daily_health.heart_rate_max` | `heartRate.maxValue` | beats per minute |
| `daily_health.moderate_minutes` | `intensityMinutes.moderate` | minutes |
| `daily_health.vigorous_minutes` | `intensityMinutes.vigorous` | minutes; not doubled |
| `daily_health.stress_avg` | `stress.avgLevel` | 0–100 |
| `daily_health.stress_max` | `stress.maxLevel` | 0–100 |
| `daily_health.body_battery_min` | `bodyBattery.minValue` | 0–100 |
| `daily_health.body_battery_max` | `bodyBattery.maxValue` | 0–100 |
| `daily_health.body_battery_latest` | `bodyBattery.latestValue` | 0–100 |
| `daily_health.body_battery_charged` | `bodyBattery.chargedValue` | daily points charged |
| `daily_health.body_battery_drained` | `bodyBattery.drainedValue` | daily points drained |
| `daily_sleep.started_at` | `sleepStartTimestampGMT` | epoch milliseconds converted to UTC |
| `daily_sleep.ended_at` | `sleepEndTimestampGMT` | epoch milliseconds converted to UTC |
| `daily_sleep.duration_s` | `sleepTimeSeconds` | seconds; main sleep excludes naps |
| `daily_sleep.nap_s` | `napTimeSeconds` | seconds |
| `daily_sleep.deep_s` | `deepSleepSeconds` | seconds |
| `daily_sleep.light_s` | `lightSleepSeconds` | seconds |
| `daily_sleep.rem_s` | `remSleepSeconds` | seconds |
| `daily_sleep.awake_s` | `awakeSleepSeconds` | seconds |
| `daily_sleep.unmeasurable_s` | `unmeasurableSleepSeconds` | seconds |
| `daily_sleep.score` | `sleepScores.overall.value` | 0–100 |
| `daily_sleep.heart_rate_avg` | `avgHeartRate` | beats per minute |
| `daily_sleep.stress_avg` | `avgSleepStress` | 0–100 |
| `daily_sleep.respiration_avg` | `averageRespirationValue` | breaths per minute |
| `daily_sleep.spo2_avg` | `averageSpO2Value` | percent |
| `daily_hrv.night_avg` | `hrvSummary.lastNightAvg` | source value |
| `daily_hrv.weekly_avg` | `hrvSummary.weeklyAvg` | source value |
| `daily_hrv.status` | `hrvSummary.status` | source status text |
| `daily_hrv.baseline_low` | `hrvSummary.baseline.balancedLow` | source value |
| `daily_hrv.baseline_high` | `hrvSummary.baseline.balancedUpper` | source value |
| `daily_hrv.baseline_low_upper` | `hrvSummary.baseline.lowUpper` | source value |

Garmin's Personal API schema does not assign a unit to the HRV fields. The
importer stores them unchanged and does not convert or infer a unit. Confirm
their unit before labeling a dashboard axis.

## PostgreSQL types and value handling

| PostgreSQL type | Used for |
| --- | --- |
| `smallint` | Daily heart rates, intensity minutes, stress, Body Battery values, and sleep score |
| `integer` | Steps, calories, sleep durations, and HRV values |
| `numeric` | Fractional distance and sleep measurements |
| `timestamptz` | Sleep start and end instants |
| `text` | HRV status |

Stress, Body Battery levels, sleep score, sleep stress, and SpO2 are constrained
to 0–100. Other measured quantities have non-negative checks; when both sleep
timestamps are present, the end must not precede the start. Fractional JSON
numbers are decoded and stored as decimal values without a
`float64` conversion. `numeric` columns have no artificial precision or scale
limit. Unsupported values fail instead of being clipped or changed to zero.

## Missing data, validation, and writes

- A missing JSON field or JSON `null` becomes SQL `NULL`, replacing any earlier
  value on the next import. Numeric zero remains zero.
- HTTP 204 for the sleep or HRV endpoint means all measurement columns in that
  table are `NULL` for the health-summary date.
- A sleep response with explicit `calendarDate: null` is accepted only when
  `sleepNeed.calendarDate` matches the requested date, `napTimeSeconds` is present,
  and every modeled nightly measurement is absent. That metadata supplies the
  date; naps retain their source value, including zero, and nightly fields remain `NULL`.
- Otherwise, an empty or `null` HTTP 200 body, invalid JSON, missing or mismatched
  `calendarDate`, a missing or duplicate health summary, a missing `hrvSummary`,
  or an invalid measurement aborts that day. Negative measurements are rejected;
  values constrained to 0–100 must also stay within that range. HTTP 404 is an
  error, not an inferred “no measurements” result.
- The importer fetches and validates health, sleep, and HRV before writing.
  It then upserts the three rows in one transaction for that date. Any write
  failure rolls back the entire day's transaction.
- Rows use `ON CONFLICT (day)`. An existing row is updated only when its values
  differ; `IS DISTINCT FROM` treats `NULL` consistently. Re-importing identical
  data does not write an unchanged row. There is no `updated_at` column.

Garmin HTTP requests have a 20-second timeout and an 8 MiB response limit.
HTTP 429 and 5xx responses are retried up to three attempts, respecting
`Retry-After` up to 30 seconds; a longer delay fails the run. HTTP 401, other
HTTP errors, network failures, and timeouts are not retried. Database connection
and per-day transaction operations each have a 20-second timeout. SIGINT and
SIGTERM cancel I/O. Logs omit API keys, tokens, database connection strings,
and response bodies.

## API client and protocol

The `garmin/` package accepts an API base URL and a caller-supplied `http.Client`.
Authenticate with `Authenticate(ctx, key)` before calling `DailySummary`,
`DailySleep` or `DailyHRV`. Sleep and HRV return `nil, nil` for HTTP 204. Use the
client sequentially; the caller owns timeouts and redirect policy.

The CLI holds one token in memory for the run. It does not persist or refresh
sessions. The token endpoint is handled as plain text with `Accept: */*`.
Sleep timestamps are decoded as numeric epoch milliseconds, matching the
observed API format rather than guessing alternate date formats from Swagger.
