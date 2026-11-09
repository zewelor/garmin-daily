# Data model

This reference describes the PostgreSQL rows written by `garmin-daily`. See the
[README](../README.md) for setup and operation, and the
[daily SQL schema](../cmd/garmin-daily/schema.sql) and
[activity SQL schema](../cmd/garmin-daily/activity_schema.sql) and
[monitoring migration](../cmd/garmin-daily/monitoring_schema.sql) for the exact DDL.

The [dry-run reference](dry-run.md) describes how to preview these rows.
Zone settings, body composition, daily SpO₂ and VO₂max are not modeled or stored.

## Tables and keys

The schema stores one Garmin account's data in the selected database or
PostgreSQL schema. It has no account or user key, so use a separate database or
schema for each account.

| Table | Primary key | Contents |
| --- | --- | --- |
| `daily_health` | `day DATE` | Garmin daily health summary |
| `daily_sleep` | `day DATE` | Daily sleep summary |
| `daily_hrv` | `day DATE` | Daily HRV summary |
| `activities` | `activity_id BIGINT` | Activity summary selected from the requested local-date range |
| `activity_exercises` | `(activity_id, exercise_index)` | Indexed exercise summary elements for an activity |
| `weight_measurements` | `measured_at TIMESTAMPTZ` | Every body-weight measurement, including several on one day |

`daily_sleep.day` and `daily_hrv.day` are foreign keys to
`daily_health.day`. All three tables use Garmin's `calendarDate`; sleep is not
split at midnight or a daylight-saving change. The schema does not store
device metadata, raw API responses, intraday sensor series, or precomputed 7/30/90-day
aggregates. There are no generated IDs, secondary indexes, or JSONB columns.
Garmin-provided averages, including weekly HRV, are stored as source metrics;
the importer does not calculate replacement averages. With only a few rows,
PostgreSQL page overhead dominates, so measure storage on a representative
dataset.

## Field mapping

Each database column below maps directly to the named Personal API field;
source measurements are stored without correction. Body mass is explicitly
converted from source grams to kilograms using exact decimal arithmetic.
Nested field paths include their full parent path.

| Table column | Personal API field | Unit or meaning |
| --- | --- | --- |
| daily tables: `day` | `calendarDate` | Garmin calendar date; see the no-night sleep exception below |
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
| `daily_health.stress_rest_ms` | `stress.restDurationInMillis` | milliseconds |
| `daily_health.stress_activity_ms` | `stress.activityDurationInMillis` | milliseconds |
| `daily_health.stress_uncategorized_ms` | `stress.uncategorizedDurationInMillis` | milliseconds |
| `daily_health.stress_low_ms` | `stress.lowStressDurationInMillis` | milliseconds |
| `daily_health.stress_medium_ms` | `stress.mediumStressDurationInMillis` | milliseconds |
| `daily_health.stress_high_ms` | `stress.highStressDurationInMillis` | milliseconds |
| `daily_health.respiration_avg` | `respiration.avgValue` | breaths per minute |
| `daily_health.respiration_min` | `respiration.minValue` | breaths per minute |
| `daily_health.respiration_max` | `respiration.maxValue` | breaths per minute |
| `daily_health.respiration_latest` | `respiration.latestValue` | breaths per minute |
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
| `activities.day` | Date portion of `startTimeLocal` | Source-local Garmin calendar date; no foreign key to daily health |
| `activities.started_at` | `startTimeGMT` | Parsed as UTC; the source string has no timezone suffix |
| `activities.name` | `activityName` | Optional activity name |
| `activities.sport` | `activityType.typeKey` | Required Garmin activity type key |
| `activities.duration_s` | `duration` | seconds |
| `activities.distance_m` | `distance` | metres |
| `activities.average_speed_mps` | `averageSpeed` | metres per second |
| `activities.calories` | `calories` | kcal as reported by Garmin |
| `activities.heart_rate_avg` | `averageHR` | beats per minute |
| `activities.heart_rate_max` | `maxHR` | beats per minute |
| `activities.training_effect_aerobic` | `aerobicTrainingEffect` | Garmin training effect |
| `activities.training_effect_anaerobic` | `anaerobicTrainingEffect` | Garmin training effect |
| `activities.training_load` | `activityTrainingLoad` | Garmin activity training load |
| `activities.sets` | `totalSets` | summary set count |
| `activities.reps` | `totalReps` | summary repetition count |
| `activities.depth_max_cm` | `maxDepth` | centimetres |
| `activities.body_battery_change` | `differenceBodyBattery` | signed change, constrained to -100 through 100 |
| `activities.heart_rate_zone_1_s` | `hrTimeInZone_1` | seconds |
| `activities.heart_rate_zone_2_s` | `hrTimeInZone_2` | seconds |
| `activities.heart_rate_zone_3_s` | `hrTimeInZone_3` | seconds |
| `activities.heart_rate_zone_4_s` | `hrTimeInZone_4` | seconds |
| `activities.heart_rate_zone_5_s` | `hrTimeInZone_5` | seconds |
| `activity_exercises.exercise_index` | Position in `summarizedExerciseSets` | Zero-based source array index |
| `activity_exercises.category` | `summarizedExerciseSets[].category` | Optional category |
| `activity_exercises.subcategory` | `summarizedExerciseSets[].subCategory` | Optional subcategory |
| `activity_exercises.sets` | `summarizedExerciseSets[].sets` | set count |
| `activity_exercises.reps` | `summarizedExerciseSets[].reps` | repetition count |
| `activity_exercises.duration_ms` | `summarizedExerciseSets[].duration` | milliseconds |
| `activity_exercises.max_weight_g` | `summarizedExerciseSets[].maxWeight` | grams |
| `activity_exercises.volume_g` | `summarizedExerciseSets[].volume` | grams × repetitions, summed across sets |
| `weight_measurements.measured_at` | `timestampGMT` | epoch milliseconds converted to a UTC instant |
| `weight_measurements.day` | `calendarDate` | Garmin local calendar date, not derived from the UTC timestamp |
| `weight_measurements.weight_kg` | `weight` | source grams divided by 1000, preserving decimal precision |

The live weight unit was checked against a manually entered account value;
Swagger incorrectly labels it as kg. Local `date`, signed `weightDelta`, BMI
and body composition are ignored. Missing required measurement time
or mass, an out-of-range date, or duplicate timestamps abort the weight batch.

Garmin's Personal API schema does not assign a unit to the HRV fields. The
importer stores them unchanged and does not convert or infer a unit. Confirm
their unit before labeling a dashboard axis.

## PostgreSQL types and value handling

| PostgreSQL type | Used for |
| --- | --- |
| `smallint` | Daily heart rates, respiration, intensity minutes, stress, Body Battery values, and sleep score |
| `integer` | Steps, calories, sleep durations, stress milliseconds, HRV values, and activity/exercise counts |
| `numeric` | Fractional daily and activity measurements, durations, weights, and volume |
| `timestamptz` | Sleep start/end, activity start and body-weight measurement instants |
| `text` | HRV status, activity name/type and exercise labels |

Stress, Body Battery levels, sleep score, sleep stress, and SpO2 are constrained
to 0–100. Activity `body_battery_change` is signed and constrained to -100
through 100; other activity and exercise measurements are non-negative. Daily
sleep timestamps, when both present, must be ordered. Fractional JSON numbers
are decoded and stored as decimal values without a `float64` conversion.
`numeric` columns have no artificial precision or scale limit. Unsupported
values fail instead of being clipped or changed to zero.

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
- Database imports omit child sessions with a positive `parentId`, preventing parent/child double counting.
  Dry-run applies the same filter.
- Activity rows are keyed by `activity_id`. Corrections replace their stored
  summary, while activities absent from a later API response are retained.
- Exercise summaries are snapshots indexed by their position in Garmin's array.
  Re-imports update changed positions and delete obsolete tail positions. The
  activity row and all exercise changes are part of one activity-range transaction.
- Activity pages are all fetched and validated before any activity write starts.
  A page or validation failure leaves the activity range untouched. The activity
  batch is written in one transaction, so a database error or cancellation rolls
  back all activity and exercise changes in that range.
- Weight entries are upserted by their UTC measurement timestamp. Each date can
  have multiple entries. Corrections update an existing timestamp, and unchanged
  entries are not rewritten. Entries absent from a later response are retained;
  an empty weight range creates no rows and infers no deletions.
- All weight entries are validated before a single range transaction begins.
  A write failure or cancellation rolls back that entire weight batch.
- `sync` commits daily rows one date at a time, then activity/exercise summaries,
  then weights. A later failure retains earlier committed steps; rerun the range
  to complete or correct it.

Garmin HTTP requests have a 20-second timeout and an 8 MiB response limit.
HTTP 429 and 5xx responses are retried up to three attempts, respecting
`Retry-After` up to the per-retry delay limit, which defaults to 2 minutes and can
be set with `GARMIN_RETRY_MAX_WAIT` (for example, `150s`). A longer delay fails
the run. The limit applies separately to each retry and each request. HTTP 401, other
HTTP errors, network failures, and timeouts are not retried. Retry diagnostics on
stderr identify authentication or data retrieval, HTTP status, attempt number,
and the decision to wait, stop at the configured delay limit, or stop at the attempt
limit. When considered, a valid `Retry-After` is shown as a duration, including
the computed wait for an HTTP date. An absent header is marked `absent` and the
default delay is shown; an invalid header fails without logging its value. On the
last attempt the header is not evaluated. Database connection
and per-day transaction operations each have a 20-second timeout. Activity range
transactions have a two-minute timeout. SIGINT and SIGTERM cancel I/O. Logs omit
API keys, tokens, database connection strings, response bodies, activity names,
IDs and body-weight values. Dry-run intentionally prints incoming row values.

## PostgreSQL design reference

The [garmin-grafana PostgreSQL backend](https://github.com/tasmith039/garmin-grafana/blob/postgres-backend/docs/postgres-backend.md)
is a useful example of separate timestamp-keyed measurements and repeatable
upserts. GarminDaily keeps its explicit typed schema and exact decimal values.
Unlike that InfluxDB-compatible backend, it applies source `NULL` corrections
and fails a transaction on incompatible values; it does not create columns or
widen types automatically during synchronization.

## API client and protocol

The `garmin/` package accepts an API base URL and a caller-supplied `http.Client`.
Authenticate with `Authenticate(ctx, key)` before calling `DailySummary`,
`DailySleep`, `DailyHRV` or `Activities`. Sleep and HRV return `nil, nil` for
HTTP 204. `Activities` requests 100 summaries per page, ordered ascending, includes child sessions,
and continues until an empty page ends pagination, including when the server limits pages below 100.
Use the client sequentially; the caller owns timeouts and redirect policy.

The CLI holds one token in memory for the run. It does not persist or refresh
sessions. The token endpoint is handled as plain text with `Accept: */*`.
Sleep timestamps are decoded as numeric epoch milliseconds, matching the
observed API format rather than guessing alternate date formats from Swagger.
