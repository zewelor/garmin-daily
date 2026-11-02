package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/zewelor/garmin-daily/garmin"
)

//go:embed schema.sql
var schemaSQL string

// Do not log connection strings, server error details or row contents.
func databaseError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("PostgreSQL %s failed (SQLSTATE %s)", operation, pgErr.Code)
	}
	return fmt.Errorf("PostgreSQL %s failed (connection, timeout or cancellation)", operation)
}

func migrate(ctx context.Context, conn *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, schemaSQL); return err })
	if err != nil {
		return databaseError("migration", err)
	}
	return nil
}

// databaseNumber adapts a source JSON decimal to pgx without float conversion.
type databaseNumber struct{ value *json.Number }

func (n databaseNumber) NumericValue() (pgtype.Numeric, error) {
	var value pgtype.Numeric
	if n.value == nil {
		return value, nil
	}
	if err := value.ScanScientific(n.value.String()); err != nil {
		return value, fmt.Errorf("invalid decimal measurement")
	}
	if value.NaN || value.InfinityModifier != pgtype.Finite || value.Int == nil || value.Int.Sign() < 0 {
		return value, fmt.Errorf("invalid decimal measurement")
	}
	return value, nil
}

func timestamp(v *garmin.EpochMillis) any {
	if v == nil {
		return nil
	}
	return v.Time
}

// Identifiers are internal constants. A shared builder keeps the update and
// unchanged-row comparison aligned with each table's explicit column list.
func upsert(ctx context.Context, tx pgx.Tx, table, columns string, args ...any) (int64, error) {
	cols := strings.Fields(columns)
	placeholders := make([]string, len(cols))
	updates := make([]string, 0, len(cols)-1)
	current := make([]string, 0, len(cols)-1)
	incoming := make([]string, 0, len(cols)-1)
	for i, col := range cols {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		if i > 0 {
			updates = append(updates, col+"=EXCLUDED."+col)
			current = append(current, table+"."+col)
			incoming = append(incoming, "EXCLUDED."+col)
		}
	}
	sql := "INSERT INTO " + table + " (" + strings.Join(cols, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ") ON CONFLICT (day) DO UPDATE SET " + strings.Join(updates, ",") + " WHERE ROW(" + strings.Join(current, ",") + ") IS DISTINCT FROM ROW(" + strings.Join(incoming, ",") + ")"
	tag, err := tx.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

func saveDay(ctx context.Context, conn *pgx.Conn, d dayData) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := d.validate(d.Health.Day); err != nil {
		return 0, err
	}
	day, _ := time.Parse(time.DateOnly, d.Health.Day)
	var changed int64
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		h, s, r := d.Health, d.Sleep, d.HRV
		n, err := upsert(ctx, tx, "daily_health", `day steps distance_m calories_total calories_active calories_resting heart_rate_resting heart_rate_min heart_rate_max moderate_minutes vigorous_minutes stress_avg stress_max body_battery_min body_battery_max body_battery_latest body_battery_charged body_battery_drained`,
			day, h.Movement.Steps.Value, databaseNumber{h.Movement.Steps.Distance}, h.Calories.Total, h.Calories.Active, h.Calories.Resting, h.HeartRate.Resting, h.HeartRate.Min, h.HeartRate.Max, h.Intensity.Moderate, h.Intensity.Vigorous, h.Stress.Average, h.Stress.Max, h.Battery.Min, h.Battery.Max, h.Battery.Latest, h.Battery.Charged, h.Battery.Drained)
		if err != nil {
			return err
		}
		changed += n
		n, err = upsert(ctx, tx, "daily_sleep", `day started_at ended_at duration_s nap_s deep_s light_s rem_s awake_s unmeasurable_s score heart_rate_avg stress_avg respiration_avg spo2_avg`,
			day, timestamp(s.Start), timestamp(s.End), s.Duration, s.Nap, s.Deep, s.Light, s.REM, s.Awake, s.Unmeasurable, s.Scores.Overall.Value, databaseNumber{s.HeartRate}, databaseNumber{s.Stress}, databaseNumber{s.Respiration}, databaseNumber{s.SpO2})
		if err != nil {
			return err
		}
		changed += n
		n, err = upsert(ctx, tx, "daily_hrv", `day night_avg weekly_avg status baseline_low baseline_high baseline_low_upper`, day, r.Night, r.Weekly, r.Status, r.Baseline.Low, r.Baseline.High, r.Baseline.LowUpper)
		changed += n
		return err
	})
	if err != nil {
		return 0, databaseError("day transaction", err)
	}
	return changed, nil
}
