# Dry-run reference

`sync --dry-run` downloads and validates monitoring data without reading
`DATABASE_URL`, connecting to a database or writing database rows. It uses the
same date-range parsing and daily download loop as normal `sync`. It prints
daily progress and summary counts; optional `--output NEW_DIRECTORY` keeps
inspection artifacts. `--output` is accepted only with `--dry-run`.

Normal `sync` stores daily summaries including respiration and stress durations,
top-level activities including time in HR zones, exercise summaries and weight
measurements. The dry run uses the same daily validation and weight conversion
checks. Additional optional API fields remain inspection-only: current zone
configuration, body composition, daily SpO₂, VO₂max and training stress score.
Dry-run inspection also retains child sessions, which the database import
excludes to avoid counting parent and child sessions together. The dry run does
not test database connectivity, permissions or SQL writes.

## Selected monitoring fields

| Data | Fields and units |
| --- | --- |
| Existing daily metrics | Health, sleep and HRV fields described in [data model](data-model.md), including active/resting/total kcal and separate moderate/vigorous minutes. |
| Stress distribution | Total, rest, activity, uncategorized, low, medium and high stress durations in milliseconds; Garmin's qualifier. |
| Respiration | Daily average, minimum, maximum and latest rate, in breaths per minute. |
| SpO₂ | Daily average, minimum and latest percentage, when measured. |
| Weight measurements | Every entry, including multiple measurements per day: calendar date, original local date, UTC timestamp, weight, signed weight change and available body-composition values. |
| Heart-rate zones | Current sport, calculation method, boundaries for zones 1–5, max/resting/lactate-threshold heart rates and automatic resting-HR setting. |
| Activities | ID, name, type, local/UTC start times, parent status/ID, duration/moving/elapsed seconds, distance in metres, calories/BMR kcal, average/max HR, moderate/vigorous minutes, aerobic/anaerobic Training Effect, effect label, training load, training stress score, VO₂max and seconds in HR zones 1–5. |

The activity list endpoint supplies these selected fields; per-activity detail
and file-download endpoints are not required. All pages are read until an empty
page, advancing by the actual number of returned records. Duplicate IDs or
out-of-range dates fail the download. Child sessions are included; do not sum
parent and child totals indiscriminately. Zone configuration is current, not a
history of settings used for past sessions. Zone reads set `generateDefaults=false`.

Numbers retain source decimal precision. Missing optional measurements become
JSON `null`, never inferred zeros. Garmin-reported zero remains zero. Daily
summary, sleep and HRV dates are validated by the existing importer. Additional
measurement validation rejects invalid ranges and negative durations while
allowing a signed weight change. An empty weight/activity/zone array is valid;
an empty or `null` HTTP 200 response is an error.

## Weight and timestamp discrepancies

The [official Swagger](https://developer.garmin.com/gc-developer-program/personal/swagger-docs/)
describes `DateWeightDTO.weight` as kilograms. A controlled live measurement,
checked against the account holder's manually entered kg value, was instead
returned in **grams**. The dry-run artifact preserves the original `weight`
value. Normal `sync` explicitly converts those grams to `weight_kg` by shifting
the decimal exponent, without passing through `float64`. Body-composition fields
are not mapped to the database.

Weight `date` and `timestampGMT` arrive as numeric epoch milliseconds, despite
Swagger declaring strings. UTC weight and sleep timestamps are decoded and
exported as integer epoch milliseconds. Weight `date` remains the original
local wall-clock number; it is not treated as a UTC instant. Activity timestamps
retain the source `yyyy-MM-dd HH:mm:ss` strings: `startTimeGMT` is UTC and
`startTimeLocal` is local time without an offset.

Body-composition fields were null in the controlled live sample. Their populated
response formats and units would need live verification before any future database mapping.
VO₂max, training stress score and daily SpO₂ were also absent in that sample;
synthetic tests cover optional values, but absence is not proof of populated
device-specific behavior. Recorded sessions may supply different subsets of
Training Effect and time-in-zone metrics.

## Artifacts and verification

Artifacts are written only when `--output` is supplied. The directory must not
exist; it is created with mode `0700` and its files with mode `0600`.

- `response-NNN.json`: exact GET response bytes. HTTP 204 files are empty. Raw
  responses retain fields outside the selected monitoring scope.
- `data.json`: selected decoded fields for the requested dates, weight entries,
  current zone configuration and recorded activities. It is not a full API export.
- `manifest.json`: requested dates, completion timestamp and each GET response's
  path, status, filename, byte count and SHA-256. It is written last on success.

For a controlled check, use a short explicit range containing known weight
entries and recorded sessions. Check the original responses against `data.json`
by measurement date/time or activity ID; compare exact numbers, missing values,
zone times, weight entries and page counts. Confirm hashes against the files.
API keys and token responses are excluded from artifacts. Original responses
include personal data and account metadata; keep the private output directory
outside the repository and any public artifacts.

The automated HTTP and file tests use synthetic data, including multiple
weigh-ins on one day, exact decimals, optional values, short server-limited
pages, repeated pages and failures partway through a download. They do not
prove that every device-specific metric is available on a real account.
