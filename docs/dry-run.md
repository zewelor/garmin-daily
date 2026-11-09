# Dry-run reference

Add `--dry-run` to `sync` or `sync-activities` with any supported date range.
The command uses the same API requests, validation, root-activity filter and
PostgreSQL row mappings as a normal import. It does not read `DATABASE_URL`,
connect to PostgreSQL or write database rows.

```sh
garmin-daily sync --dry-run --from 2026-10-25 --to 2026-10-31
```

## Output

Stdout contains JSON Lines. Each incoming row has a `table` and `values` object
using the exact PostgreSQL column names and normalized values:

```json
{"table":"weight_measurements","values":{"day":"2026-10-28","measured_at":"2026-10-28T08:00:00Z","weight_delta":-900,"weight_kg":82.3456}}
```

Dates are `YYYY-MM-DD`; timestamps are UTC instants. Decimal values retain source
precision without conversion through `float64`. Missing optional measurements
are `null`. Reported zero remains zero, except when combining compatible weight
entries sharing a timestamp: if a group contains both zero and one nonzero
weight or weight delta, it prints the nonzero one. Fields and units are listed in the
[data model](data-model.md).

For every activity, a `delete` object also describes the exercise snapshot
cleanup performed after its upserts. For example:

```json
{"table":"activity_exercises","delete":{"activity_id":123,"exercise_index_gte":2}}
```

This means stored exercise positions 2 and above would be removed for activity
123. With an empty exercise list, the threshold is 0. Activities and weights
absent from the API response do not imply deletion.

Weight is printed as `weight_kg`. Although Swagger labels the source `weight`
as kg, a controlled live check against the account holder's entered value
confirmed grams. Both modes divide by 1000 using exact decimal arithmetic.
`weight_delta` retains Garmin's signed source value; its unit has not been
independently verified.
BMI, body composition, current zone configuration and other unpersisted fields
are ignored. Activity zone durations are stored in seconds; daily moderate and
vigorous minutes remain separate, without doubling vigorous minutes.

## Verification limits

The preview shows incoming values and cleanup predicates. It cannot determine
which rows are new or changed, how many rows would be deleted, or whether SQL
writes and permissions succeed. Those require a database import. A later fetch
can also differ if Garmin updates a measurement.

Output is streamed: a failed run can leave a partial preview. Check the exit
status before using it. Output-write errors fail the command. Redirect stdout
with your shell to keep a preview; files contain personal health data and
should stay outside the repository.

Automated checks use synthetic HTTP data and an isolated PostgreSQL schema.
They cover exact decimals, missing values, multiple weigh-ins, activity filtering,
exercise snapshots, repeat imports and failures. They do not prove availability
of every device-specific metric on a real account.
