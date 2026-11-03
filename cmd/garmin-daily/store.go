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

//go:embed activity_schema.sql
var activitySchemaSQL string

//go:embed monitoring_schema.sql
var monitoringSchemaSQL string

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
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, schemaSQL); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, activitySchemaSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, monitoringSchemaSQL)
		return err
	})
	if err != nil {
		return databaseError("migration", err)
	}
	return nil
}

func migrateActivities(ctx context.Context, conn *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, activitySchemaSQL)
		return err
	})
	if err != nil {
		return databaseError("activity migration", err)
	}
	return nil
}

func migrateMonitoring(ctx context.Context, conn *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, monitoringSchemaSQL)
		return err
	})
	if err != nil {
		return databaseError("monitoring migration", err)
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
		n, err := upsert(ctx, tx, "daily_health", `day steps distance_m calories_total calories_active calories_resting heart_rate_resting heart_rate_min heart_rate_max moderate_minutes vigorous_minutes stress_avg stress_max body_battery_min body_battery_max body_battery_latest body_battery_charged body_battery_drained respiration_avg respiration_min respiration_max respiration_latest stress_rest_ms stress_activity_ms stress_uncategorized_ms stress_low_ms stress_medium_ms stress_high_ms`,
			day, h.Movement.Steps.Value, databaseNumber{h.Movement.Steps.Distance}, h.Calories.Total, h.Calories.Active, h.Calories.Resting, h.HeartRate.Resting, h.HeartRate.Min, h.HeartRate.Max, h.Intensity.Moderate, h.Intensity.Vigorous, h.Stress.Average, h.Stress.Max, h.Battery.Min, h.Battery.Max, h.Battery.Latest, h.Battery.Charged, h.Battery.Drained,
			h.Respiration.Average, h.Respiration.Min, h.Respiration.Max, h.Respiration.Latest,
			h.Stress.RestMillis, h.Stress.ActivityMillis, h.Stress.UncategorizedMillis, h.Stress.LowMillis, h.Stress.MediumMillis, h.Stress.HighMillis)
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

func saveActivities(ctx context.Context, conn *pgx.Conn, activities []garmin.Activity, from, to string) (int64, error) {
	seen := make(map[int64]struct{}, len(activities))
	for _, activity := range activities {
		if err := validateActivity(activity, from, to); err != nil {
			return 0, err
		}
		if _, ok := seen[activity.ID]; ok {
			return 0, fmt.Errorf("duplicate activity ID in write range")
		}
		seen[activity.ID] = struct{}{}
	}
	if len(activities) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var changed int64
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, activity := range activities {
			day, startedAt, _ := activityDates(activity)
			tag, err := tx.Exec(ctx, `INSERT INTO activities (
				activity_id,day,started_at,name,sport,duration_s,distance_m,average_speed_mps,calories,
				heart_rate_avg,heart_rate_max,training_effect_aerobic,training_effect_anaerobic,
				training_load,sets,reps,depth_max_cm,body_battery_change,
				heart_rate_zone_1_s,heart_rate_zone_2_s,heart_rate_zone_3_s,heart_rate_zone_4_s,heart_rate_zone_5_s
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
			ON CONFLICT (activity_id) DO UPDATE SET
				day=EXCLUDED.day,started_at=EXCLUDED.started_at,name=EXCLUDED.name,sport=EXCLUDED.sport,
				duration_s=EXCLUDED.duration_s,distance_m=EXCLUDED.distance_m,average_speed_mps=EXCLUDED.average_speed_mps,
				calories=EXCLUDED.calories,heart_rate_avg=EXCLUDED.heart_rate_avg,heart_rate_max=EXCLUDED.heart_rate_max,
				training_effect_aerobic=EXCLUDED.training_effect_aerobic,
				training_effect_anaerobic=EXCLUDED.training_effect_anaerobic,training_load=EXCLUDED.training_load,
				sets=EXCLUDED.sets,reps=EXCLUDED.reps,depth_max_cm=EXCLUDED.depth_max_cm,
				body_battery_change=EXCLUDED.body_battery_change,
				heart_rate_zone_1_s=EXCLUDED.heart_rate_zone_1_s,heart_rate_zone_2_s=EXCLUDED.heart_rate_zone_2_s,
				heart_rate_zone_3_s=EXCLUDED.heart_rate_zone_3_s,heart_rate_zone_4_s=EXCLUDED.heart_rate_zone_4_s,
				heart_rate_zone_5_s=EXCLUDED.heart_rate_zone_5_s
			WHERE ROW(activities.day,activities.started_at,activities.name,activities.sport,activities.duration_s,
				activities.distance_m,activities.average_speed_mps,activities.calories,activities.heart_rate_avg,
				activities.heart_rate_max,activities.training_effect_aerobic,activities.training_effect_anaerobic,
				activities.training_load,activities.sets,activities.reps,activities.depth_max_cm,
				activities.body_battery_change,activities.heart_rate_zone_1_s,activities.heart_rate_zone_2_s,
				activities.heart_rate_zone_3_s,activities.heart_rate_zone_4_s,activities.heart_rate_zone_5_s
			) IS DISTINCT FROM ROW(EXCLUDED.day,EXCLUDED.started_at,EXCLUDED.name,
				EXCLUDED.sport,EXCLUDED.duration_s,EXCLUDED.distance_m,EXCLUDED.average_speed_mps,EXCLUDED.calories,
				EXCLUDED.heart_rate_avg,EXCLUDED.heart_rate_max,EXCLUDED.training_effect_aerobic,
				EXCLUDED.training_effect_anaerobic,EXCLUDED.training_load,EXCLUDED.sets,EXCLUDED.reps,
				EXCLUDED.depth_max_cm,EXCLUDED.body_battery_change,EXCLUDED.heart_rate_zone_1_s,
				EXCLUDED.heart_rate_zone_2_s,EXCLUDED.heart_rate_zone_3_s,EXCLUDED.heart_rate_zone_4_s,
				EXCLUDED.heart_rate_zone_5_s)`,
				activity.ID, day, startedAt, nullableActivityName(activity.Name), activity.Type.Key,
				databaseNumber{activity.Duration}, databaseNumber{activity.Distance}, databaseNumber{activity.AverageSpeed},
				databaseNumber{activity.Calories}, databaseNumber{activity.HeartRateAverage}, databaseNumber{activity.HeartRateMax},
				databaseNumber{activity.AerobicEffect}, databaseNumber{activity.AnaerobicEffect},
				databaseNumber{activity.TrainingLoad}, activity.TotalSets, activity.TotalReps, databaseNumber{activity.MaxDepth},
				activity.BodyBatteryChange, databaseNumber{activity.Zone1Seconds}, databaseNumber{activity.Zone2Seconds},
				databaseNumber{activity.Zone3Seconds}, databaseNumber{activity.Zone4Seconds}, databaseNumber{activity.Zone5Seconds})
			if err != nil {
				return err
			}
			changed += tag.RowsAffected()
			for i, exercise := range activity.Exercises {
				tag, err = tx.Exec(ctx, `INSERT INTO activity_exercises (
					activity_id,exercise_index,category,subcategory,sets,reps,duration_ms,max_weight_g,volume_g
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (activity_id,exercise_index) DO UPDATE SET
					category=EXCLUDED.category,subcategory=EXCLUDED.subcategory,sets=EXCLUDED.sets,reps=EXCLUDED.reps,
					duration_ms=EXCLUDED.duration_ms,max_weight_g=EXCLUDED.max_weight_g,volume_g=EXCLUDED.volume_g
				WHERE ROW(activity_exercises.category,activity_exercises.subcategory,activity_exercises.sets,
					activity_exercises.reps,activity_exercises.duration_ms,activity_exercises.max_weight_g,
					activity_exercises.volume_g) IS DISTINCT FROM ROW(EXCLUDED.category,EXCLUDED.subcategory,
					EXCLUDED.sets,EXCLUDED.reps,EXCLUDED.duration_ms,EXCLUDED.max_weight_g,EXCLUDED.volume_g)`,
					activity.ID, i, exercise.Category, exercise.Subcategory, exercise.Sets, exercise.Reps,
					databaseNumber{exercise.Duration}, databaseNumber{exercise.MaxWeight}, databaseNumber{exercise.Volume})
				if err != nil {
					return err
				}
				changed += tag.RowsAffected()
			}
			tag, err = tx.Exec(ctx, "DELETE FROM activity_exercises WHERE activity_id=$1 AND exercise_index >= $2", activity.ID, len(activity.Exercises))
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
