package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/zewelor/garmin-daily/garmin"
)

// Do not log connection strings, server error details or row contents.
func databaseError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("PostgreSQL %s failed (SQLSTATE %s)", operation, pgErr.Code)
	}
	return fmt.Errorf("PostgreSQL %s failed (connection, timeout or cancellation)", operation)
}

func checkDatabaseVersion(ctx context.Context, conn *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var version int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		return databaseError("version check", err)
	}
	if version < 180000 {
		return fmt.Errorf("PostgreSQL 18 or newer is required (found %d)", version/10000)
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

func (n databaseNumber) MarshalJSON() ([]byte, error) {
	value, err := n.NumericValue()
	if err != nil {
		return nil, err
	}
	return value.MarshalJSON()
}

func dayRows(d dayData) []row {
	h, s, r := d.Health, d.Sleep, d.HRV
	day, _ := time.Parse(time.DateOnly, h.Day)
	date := pgtype.Date{Time: day, Valid: true}
	return []row{
		{"daily_health", "day", `day steps distance_m calories_total calories_active calories_resting heart_rate_resting heart_rate_min heart_rate_max moderate_minutes vigorous_minutes stress_avg stress_max body_battery_min body_battery_max body_battery_latest body_battery_charged body_battery_drained respiration_avg respiration_min respiration_max respiration_latest stress_rest_ms stress_activity_ms stress_uncategorized_ms stress_low_ms stress_medium_ms stress_high_ms`,
			[]any{date, h.Movement.Steps.Value, databaseNumber{h.Movement.Steps.Distance}, h.Calories.Total, h.Calories.Active, h.Calories.Resting, h.HeartRate.Resting, h.HeartRate.Min, h.HeartRate.Max, h.Intensity.Moderate, h.Intensity.Vigorous, h.Stress.Average, h.Stress.Max, h.Battery.Min, h.Battery.Max, h.Battery.Latest, h.Battery.Charged, h.Battery.Drained,
				h.Respiration.Average, h.Respiration.Min, h.Respiration.Max, h.Respiration.Latest,
				h.Stress.RestMillis, h.Stress.ActivityMillis, h.Stress.UncategorizedMillis, h.Stress.LowMillis, h.Stress.MediumMillis, h.Stress.HighMillis}},
		{"daily_sleep", "day", `day started_at ended_at duration_s nap_s deep_s light_s rem_s awake_s unmeasurable_s score heart_rate_avg stress_avg respiration_avg spo2_avg`,
			[]any{date, timestamp(s.Start), timestamp(s.End), s.Duration, s.Nap, s.Deep, s.Light, s.REM, s.Awake, s.Unmeasurable, s.Scores.Overall.Value, databaseNumber{s.HeartRate}, databaseNumber{s.Stress}, databaseNumber{s.Respiration}, databaseNumber{s.SpO2}}},
		{"daily_hrv", "day", `day night_avg weekly_avg status baseline_low baseline_high baseline_low_upper`,
			[]any{date, r.Night, r.Weekly, r.Status, r.Baseline.Low, r.Baseline.High, r.Baseline.LowUpper}},
	}
}

func saveDay(ctx context.Context, conn *pgx.Conn, d dayData) (int64, error) {
	if err := d.validate(d.Health.Day); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var changed int64
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, r := range dayRows(d) {
			n, err := upsert(ctx, tx, r)
			if err != nil {
				return err
			}
			changed += n
		}
		return nil
	})
	if err != nil {
		return 0, databaseError("day transaction", err)
	}
	return changed, nil
}

func activityDates(a garmin.Activity) (time.Time, time.Time, error) {
	local, err := time.Parse(time.DateTime, a.StartLocal)
	if err != nil || local.Format(time.DateTime) != a.StartLocal {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid activity local start time")
	}
	startedAt, err := time.ParseInLocation(time.DateTime, a.StartGMT, time.UTC)
	if err != nil || startedAt.Format(time.DateTime) != a.StartGMT {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid activity GMT start time")
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return day, startedAt, nil
}

func nullableActivityName(name string) any {
	if name == "" {
		return nil
	}
	return name
}

func validateActivity(a garmin.Activity, from, to string) error {
	day, _, err := activityDates(a)
	dayString := day.Format(time.DateOnly)
	if a.ID <= 0 || err != nil || !validDay(dayString) || dayString < from || dayString > to || strings.TrimSpace(a.Type.Key) == "" {
		return fmt.Errorf("invalid activity identity or timestamp")
	}
	if a.TotalSets != nil && *a.TotalSets < 0 || a.TotalReps != nil && *a.TotalReps < 0 {
		return fmt.Errorf("negative activity count")
	}
	if a.BodyBatteryChange != nil && (*a.BodyBatteryChange < -100 || *a.BodyBatteryChange > 100) {
		return fmt.Errorf("body battery change outside supported range")
	}
	for _, value := range []*json.Number{
		a.Duration, a.Distance, a.AverageSpeed, a.Calories, a.HeartRateAverage, a.HeartRateMax,
		a.AerobicEffect, a.AnaerobicEffect, a.TrainingLoad, a.MaxDepth,
		a.Zone1Seconds, a.Zone2Seconds, a.Zone3Seconds, a.Zone4Seconds, a.Zone5Seconds,
	} {
		if _, err := (databaseNumber{value}).NumericValue(); err != nil {
			return err
		}
	}
	for _, exercise := range a.Exercises {
		if exercise == nil {
			return fmt.Errorf("invalid summarized exercise set")
		}
		if exercise.Sets != nil && *exercise.Sets < 0 || exercise.Reps != nil && *exercise.Reps < 0 {
			return fmt.Errorf("negative exercise count")
		}
		for _, value := range []*json.Number{exercise.Duration, exercise.MaxWeight, exercise.Volume} {
			if _, err := (databaseNumber{value}).NumericValue(); err != nil {
				return err
			}
		}
	}
	return nil
}

func activityRows(a garmin.Activity) []row {
	day, startedAt, _ := activityDates(a)
	rows := []row{{"activities", "activity_id", `activity_id day started_at name sport duration_s distance_m average_speed_mps calories heart_rate_avg heart_rate_max training_effect_aerobic training_effect_anaerobic training_load sets reps depth_max_cm body_battery_change heart_rate_zone_1_s heart_rate_zone_2_s heart_rate_zone_3_s heart_rate_zone_4_s heart_rate_zone_5_s`,
		[]any{a.ID, pgtype.Date{Time: day, Valid: true}, startedAt, nullableActivityName(a.Name), a.Type.Key,
			databaseNumber{a.Duration}, databaseNumber{a.Distance}, databaseNumber{a.AverageSpeed}, databaseNumber{a.Calories},
			databaseNumber{a.HeartRateAverage}, databaseNumber{a.HeartRateMax}, databaseNumber{a.AerobicEffect}, databaseNumber{a.AnaerobicEffect},
			databaseNumber{a.TrainingLoad}, a.TotalSets, a.TotalReps, databaseNumber{a.MaxDepth}, a.BodyBatteryChange,
			databaseNumber{a.Zone1Seconds}, databaseNumber{a.Zone2Seconds}, databaseNumber{a.Zone3Seconds}, databaseNumber{a.Zone4Seconds}, databaseNumber{a.Zone5Seconds}}}}
	for i, exercise := range a.Exercises {
		rows = append(rows, row{"activity_exercises", "activity_id exercise_index", `activity_id exercise_index category subcategory sets reps duration_ms max_weight_g volume_g`,
			[]any{a.ID, i, exercise.Category, exercise.Subcategory, exercise.Sets, exercise.Reps, databaseNumber{exercise.Duration}, databaseNumber{exercise.MaxWeight}, databaseNumber{exercise.Volume}}})
	}
	return rows
}

func validateActivities(activities []garmin.Activity, from, to string) error {
	seen := make(map[int64]bool, len(activities))
	for _, a := range activities {
		if err := validateActivity(a, from, to); err != nil {
			return err
		}
		if seen[a.ID] {
			return fmt.Errorf("duplicate activity ID in write range")
		}
		seen[a.ID] = true
	}
	return nil
}

func saveActivities(ctx context.Context, conn *pgx.Conn, activities []garmin.Activity, from, to string) (int64, error) {
	if err := validateActivities(activities, from, to); err != nil {
		return 0, err
	}
	if len(activities) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var changed int64
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, a := range activities {
			for _, r := range activityRows(a) {
				n, err := upsert(ctx, tx, r)
				if err != nil {
					return err
				}
				changed += n
			}
			tag, err := tx.Exec(ctx, "DELETE FROM activity_exercises WHERE activity_id=$1 AND exercise_index >= $2", a.ID, len(a.Exercises))
			if err != nil {
				return err
			}
			changed += tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return 0, databaseError("activity range transaction", err)
	}
	return changed, nil
}
