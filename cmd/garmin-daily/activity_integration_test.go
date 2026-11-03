//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zewelor/garmin-daily/garmin"
)

func activityNumber(value string) *json.Number {
	n := json.Number(value)
	return &n
}

func integrationActivity() garmin.Activity {
	change := int16(-12)
	sets, reps := int32(2), int32(10)
	return garmin.Activity{
		ID: 9001, Day: "2026-10-26", StartedAt: time.Date(2026, 10, 25, 23, 15, 0, 0, time.UTC),
		Sport: "strength_training", DurationS: activityNumber("3661.123456789012345"),
		DistanceM: activityNumber("0"), AverageSpeedMPS: activityNumber("1e-10"),
		Calories: activityNumber("123.45"), HeartRateAvg: activityNumber("0"),
		TrainingEffectAerobic: activityNumber("1.5"), TrainingLoad: activityNumber("1.234567890123456789"),
		Sets: &sets, Reps: &reps, BodyBatteryChange: &change,
		Exercises: []garmin.ActivityExercise{
			{Category: stringPointer("strength_training"), Subcategory: stringPointer("bench_press"), Sets: &sets, Reps: &reps,
				DurationMS: activityNumber("1234.5"), MaxWeightG: activityNumber("12000"), VolumeG: activityNumber("120000.123456789")},
			{Category: stringPointer("strength_training"), Subcategory: stringPointer("row"), Sets: &sets, Reps: &reps,
				DurationMS: activityNumber("0"), MaxWeightG: activityNumber("0"), VolumeG: activityNumber("0")},
		},
	}
}

func stringPointer(value string) *string { return &value }

func TestIntegrationActivityUpsertAndExerciseSnapshot(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	a := integrationActivity()
	if changed, err := saveActivities(ctx, conn, []garmin.Activity{a}, a.Day, a.Day); err != nil || changed != 3 {
		t.Fatalf("initial activity save changed=%d err=%v", changed, err)
	}
	var day, sport, duration, distance, speed, battery, volume string
	var started time.Time
	var missing bool
	err := conn.QueryRow(ctx, `SELECT day::text,sport,duration_s::text,distance_m::text,average_speed_mps::text,
		body_battery_change::text,started_at,name IS NULL FROM activities WHERE activity_id=9001`).Scan(
		&day, &sport, &duration, &distance, &speed, &battery, &started, &missing)
	if err != nil {
		t.Fatal(err)
	}
	if day != a.Day || sport != a.Sport || duration != "3661.123456789012345" || distance != "0" || speed != "0.0000000001" || battery != "-12" || !started.Equal(a.StartedAt) || !missing {
		t.Fatalf("stored activity values differ: day=%s sport=%s duration=%s distance=%s speed=%s battery=%s started=%s name_null=%v", day, sport, duration, distance, speed, battery, started, missing)
	}
	if err := conn.QueryRow(ctx, `SELECT volume_g::text FROM activity_exercises WHERE activity_id=9001 AND exercise_index=0`).Scan(&volume); err != nil || volume != "120000.123456789" {
		t.Fatalf("exercise volume=%s err=%v", volume, err)
	}
	var xminBefore, xminAfter string
	if err := conn.QueryRow(ctx, "SELECT xmin::text FROM activities WHERE activity_id=9001").Scan(&xminBefore); err != nil {
		t.Fatal(err)
	}
	if changed, err := saveActivities(ctx, conn, []garmin.Activity{a}, a.Day, a.Day); err != nil || changed != 0 {
		t.Fatalf("repeat save changed=%d err=%v", changed, err)
	}
	if err := conn.QueryRow(ctx, "SELECT xmin::text FROM activities WHERE activity_id=9001").Scan(&xminAfter); err != nil || xminAfter != xminBefore {
		t.Fatalf("unchanged activity rewritten: before=%s after=%s err=%v", xminBefore, xminAfter, err)
	}
	corrected := a
	corrected.Name = nil
	corrected.DistanceM = nil
	corrected.Exercises = corrected.Exercises[:1]
	if changed, err := saveActivities(ctx, conn, []garmin.Activity{corrected}, a.Day, a.Day); err != nil || changed != 2 {
		t.Fatalf("correction and exercise removal changed=%d err=%v", changed, err)
	}
	var exerciseCount, dailyCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM activity_exercises WHERE activity_id=9001").Scan(&exerciseCount); err != nil || exerciseCount != 1 {
		t.Fatalf("exercise snapshot count=%d err=%v", exerciseCount, err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM daily_health").Scan(&dailyCount); err != nil || dailyCount != 0 {
		t.Fatalf("activity incorrectly requires daily row: count=%d err=%v", dailyCount, err)
	}
	if changed, err := saveActivities(ctx, conn, nil, a.Day, a.Day); err != nil || changed != 0 {
		t.Fatalf("empty response changed=%d err=%v", changed, err)
	}
	var activityCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM activities WHERE activity_id=9001").Scan(&activityCount); err != nil || activityCount != 1 {
		t.Fatalf("activity absent from a response was deleted: count=%d err=%v", activityCount, err)
	}
}

func TestIntegrationActivitySchemaMigrations(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("garmin_activity_migrate_%d", time.Now().UnixNano())}.Sanitize()
	freshSchema := ""
	if _, err := conn.Exec(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		conn.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanup.Close(context.Background())
		if freshSchema != "" {
			if _, err := cleanup.Exec(context.Background(), "DROP SCHEMA "+freshSchema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
		if _, err := cleanup.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		conn.Close(context.Background())
	})
	if _, err := conn.Exec(context.Background(), "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), schemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), "INSERT INTO daily_health(day) VALUES ('2026-10-26')"); err != nil {
		t.Fatal(err)
	}
	if err := migrateActivities(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name IN ('activities','activity_exercises')`, strings.Trim(schema, `"`)).Scan(&count); err != nil || count != 2 {
		t.Fatalf("activity tables=%d err=%v", count, err)
	}
	if err := conn.QueryRow(context.Background(), "SELECT count(*) FROM daily_health WHERE day='2026-10-26'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade changed daily row count=%d err=%v", count, err)
	}
	if err := migrateActivities(context.Background(), conn); err == nil {
		t.Fatal("repeated activity migration succeeded")
	}

	freshSchema = pgx.Identifier{fmt.Sprintf("garmin_activity_fresh_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := conn.Exec(context.Background(), "CREATE SCHEMA "+freshSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), "SET search_path TO "+freshSchema); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name IN ('daily_health','daily_sleep','daily_hrv','activities','activity_exercises')`, strings.Trim(freshSchema, `"`)).Scan(&count); err != nil || count != 5 {
		t.Fatalf("fresh schema tables=%d err=%v", count, err)
	}
	if err := migrate(context.Background(), conn); err == nil {
		t.Fatal("repeated fresh migration succeeded")
	}
	if _, err := conn.Exec(context.Background(), "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationActivityTransactionRollsBackOnFailureAndCancellation(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	a := integrationActivity()
	if _, err := conn.Exec(ctx, `CREATE FUNCTION reject_activity_exercise() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated activity write failure'; END $$; CREATE TRIGGER reject_activity_exercise BEFORE INSERT ON activity_exercises FOR EACH ROW EXECUTE FUNCTION reject_activity_exercise()`); err != nil {
		t.Fatal(err)
	}
	if _, err := saveActivities(ctx, conn, []garmin.Activity{a}, a.Day, a.Day); err == nil {
		t.Fatal("expected exercise write failure")
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM activities").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed exercise write left activity rows=%d err=%v", count, err)
	}
	if _, err := conn.Exec(ctx, "DROP TRIGGER reject_activity_exercise ON activity_exercises; DROP FUNCTION reject_activity_exercise()"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE FUNCTION pause_activity_exercise() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(10); RETURN NEW; END $$; CREATE TRIGGER pause_activity_exercise BEFORE INSERT ON activity_exercises FOR EACH ROW EXECUTE FUNCTION pause_activity_exercise()`); err != nil {
		t.Fatal(err)
	}
	control, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close(ctx)
	var schema string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	writeCtx, cancel := context.WithCancel(ctx)
	pid := conn.PgConn().PID()
	result := make(chan error, 1)
	go func() { _, err := saveActivities(writeCtx, conn, []garmin.Activity{a}, a.Day, a.Day); result <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var paused bool
		if err := control.QueryRow(ctx, "SELECT coalesce(wait_event='PgSleep',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&paused); err != nil {
			t.Fatal(err)
		}
		if paused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("activity transaction did not reach exercise trigger")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("canceled activity transaction succeeded")
	}
	if err := control.QueryRow(ctx, "SELECT count(*) FROM activities").Scan(&count); err != nil || count != 0 {
		t.Fatalf("canceled transaction left activity rows=%d err=%v", count, err)
	}
	if _, err := control.Exec(ctx, "DROP TRIGGER pause_activity_exercise ON activity_exercises; DROP FUNCTION pause_activity_exercise()"); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationActivityPageFailureWritesNothing(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/personalapitoken-service/personal-api/personal-api-key/token" {
			fmt.Fprint(w, "test.bearer.token")
			return
		}
		calls++
		if r.URL.Query().Get("start") == "0" {
			items := make([]string, 99)
			for i := range items {
				items[i] = fmt.Sprintf(`{"activityId":%d,"startTimeLocal":"2026-10-26 00:15:00","startTimeGMT":"2026-10-25 23:15:00","activityType":{"typeKey":"cycling"}}`, i+1)
			}
			fmt.Fprint(w, "[", strings.Join(items, ","), "]")
			return
		}
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "synthetic upstream failure")
	}))
	defer server.Close()
	c := garmin.NewClient(server.URL, server.Client())
	if err := c.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if err := syncActivitiesRange(ctx, c, conn, day, day, io.Discard); err == nil {
		t.Fatal("activity page failure succeeded")
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM activities").Scan(&count); err != nil || count != 0 {
		t.Fatalf("page failure wrote activity rows=%d err=%v", count, err)
	}
	if calls != 4 {
		t.Fatalf("activity requests=%d, want one successful page plus three failed attempts", calls)
	}
}
