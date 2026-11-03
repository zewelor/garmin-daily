//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/zewelor/garmin-daily/garmin"

	"github.com/jackc/pgx/v5"
)

func testDatabase(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := pgx.Identifier{fmt.Sprintf("garmin_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanup.Close(context.Background())
		if _, err := cleanup.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
		conn.Close(context.Background())
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+name); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestIntegrationImportRecovery(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	s := fixtureServer(t, healthFixture, sleepFixture, hrvFixture)
	defer s.Close()
	c := garmin.NewClient(s.URL, s.Client())
	if err := c.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	d, err := fetchDay(ctx, c, "2026-10-25")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := saveDay(ctx, conn, d); err != nil || n != 3 {
		t.Fatalf("first import changed=%d err=%v", n, err)
	}
	var steps, duration, score, night int
	var distance, heart string
	var start, end time.Time
	err = conn.QueryRow(ctx, `SELECT steps,distance_m::text,duration_s,score,heart_rate_avg::text,night_avg,started_at,ended_at FROM daily_health JOIN daily_sleep USING(day) JOIN daily_hrv USING(day)`).Scan(&steps, &distance, &duration, &score, &heart, &night, &start, &end)
	if err != nil {
		t.Fatal(err)
	}
	if steps != 12345 || distance != "9876.54" || duration != 36000 || score != 88 || heart != "51.25" || night != 52 || start.UnixMilli() != 1792879200000 || end.Sub(start) != 10*time.Hour {
		t.Fatal("stored values differ from source")
	}
	var xminBefore, xminAfter string
	if err := conn.QueryRow(ctx, "SELECT xmin::text FROM daily_health").Scan(&xminBefore); err != nil {
		t.Fatal(err)
	}
	if n, err := saveDay(ctx, conn, d); err != nil || n != 0 {
		t.Fatalf("repeat changed=%d err=%v", n, err)
	}
	if err := conn.QueryRow(ctx, "SELECT xmin::text FROM daily_health").Scan(&xminAfter); err != nil {
		t.Fatal(err)
	}
	if xminBefore != xminAfter {
		t.Fatal("unchanged record was rewritten")
	}
	corrected := d
	v := int32(12346)
	corrected.Health.Movement.Steps.Value = &v
	if n, err := saveDay(ctx, conn, corrected); err != nil || n != 1 {
		t.Fatalf("correction changed=%d err=%v", n, err)
	}
	corrected.Health.Movement.Steps.Value = nil
	corrected.Health.Movement.Steps.Distance = nil
	corrected.Sleep.Duration = nil
	corrected.HRV.Night = nil
	if _, err := saveDay(ctx, conn, corrected); err != nil {
		t.Fatal(err)
	}
	var allNull bool
	if err := conn.QueryRow(ctx, `SELECT steps IS NULL AND distance_m IS NULL AND duration_s IS NULL AND night_avg IS NULL FROM daily_health JOIN daily_sleep USING(day) JOIN daily_hrv USING(day)`).Scan(&allNull); err != nil || !allNull {
		t.Fatalf("missing fields: null=%v err=%v", allNull, err)
	}
	if _, err := saveDay(ctx, conn, d); err != nil {
		t.Fatal(err)
	} // late completion
	if _, err := conn.Exec(ctx, `CREATE FUNCTION reject_hrv() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated interrupted write'; END $$; CREATE TRIGGER reject_hrv BEFORE INSERT OR UPDATE ON daily_hrv FOR EACH ROW EXECUTE FUNCTION reject_hrv()`); err != nil {
		t.Fatal(err)
	}
	interrupted := d
	interrupted.Health.Day = "2026-10-26"
	interrupted.Sleep.Day = "2026-10-26"
	interrupted.HRV.Day = "2026-10-26"
	if _, err := saveDay(ctx, conn, interrupted); err == nil {
		t.Fatal("expected interrupted transaction")
	}
	for _, table := range []string{"daily_health", "daily_sleep", "daily_hrv"} {
		var count int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("partial day or lost prior commit: %s count=%d err=%v", table, count, err)
		}
	}
	if _, err := conn.Exec(ctx, "DROP TRIGGER reject_hrv ON daily_hrv"); err != nil {
		t.Fatal(err)
	}
	if _, err := saveDay(ctx, conn, interrupted); err != nil {
		t.Fatal(err)
	}
	if n, err := saveDay(ctx, conn, interrupted); err != nil || n != 0 {
		t.Fatalf("resume repeat changed=%d err=%v", n, err)
	}
	// A canceled run cannot replace a completed day.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := saveDay(canceled, conn, d); err == nil {
		t.Fatal("canceled save succeeded")
	}
	t.Log("first import → identical import → correction → NULL → late completion → interrupted transaction → resume passed")
}

func TestIntegrationHTTPFailureLeavesDayUntouched(t *testing.T) {
	conn := testDatabase(t)
	ctx := context.Background()
	s := fixtureServer(t, healthFixture, `{`, hrvFixture)
	defer s.Close()
	c := garmin.NewClient(s.URL, s.Client())
	if err := c.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	day, _ := time.Parse(time.DateOnly, "2026-10-25")
	if err := syncRange(ctx, c, conn, day, day, false, io.Discard); err == nil {
		t.Fatal("invalid sleep response accepted")
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM daily_health").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial day n=%d err=%v", n, err)
	}
}

func TestIntegrationExactDecimals(t *testing.T) {
	conn := testDatabase(t)
	ctx := context.Background()
	s := fixtureServer(t, healthFixture, sleepFixture, hrvFixture)
	defer s.Close()
	c := garmin.NewClient(s.URL, s.Client())
	if err := c.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	d, err := fetchDay(ctx, c, "2026-10-25")
	if err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]string{"1234.567890123456789": "1234.567890123456789", "1.234e3": "1234", "1e-10": "0.0000000001"} {
		n := json.Number(source)
		d.Health.Movement.Steps.Distance = &n
		if _, err := saveDay(ctx, conn, d); err != nil {
			t.Fatal(err)
		}
		var value string
		if err := conn.QueryRow(ctx, "SELECT distance_m::text FROM daily_health").Scan(&value); err != nil || value != want {
			t.Fatalf("precision loss %s want=%s err=%v", value, want, err)
		}
	}
}

func TestIntegrationLostConnectionRollsBackDay(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	var schema string
	if err := conn.QueryRow(ctx, "SHOW search_path").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	control, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close(ctx)
	if _, err := control.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	s := fixtureServer(t, healthFixture, sleepFixture, hrvFixture)
	defer s.Close()
	c := garmin.NewClient(s.URL, s.Client())
	if err := c.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	d, err := fetchDay(ctx, c, "2026-10-25")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE FUNCTION pause_hrv() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(10); RETURN NEW; END $$; CREATE TRIGGER pause_hrv BEFORE INSERT ON daily_hrv FOR EACH ROW EXECUTE FUNCTION pause_hrv()`); err != nil {
		t.Fatal(err)
	}
	pid := conn.PgConn().PID()
	result := make(chan error, 1)
	go func() { _, err := saveDay(ctx, conn, d); result <- err }()
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
			t.Fatal("write did not reach the HRV trigger")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var terminated bool
	if err := control.QueryRow(ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate own test connection: %v", err)
	}
	if err := <-result; err == nil {
		t.Fatal("connection loss was reported as success")
	}
	for _, table := range []string{"daily_health", "daily_sleep", "daily_hrv"} {
		var count int
		if err := control.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial write after disconnect: %s rows=%d err=%v", table, count, err)
		}
	}
	if _, err := control.Exec(ctx, "DROP TRIGGER pause_hrv ON daily_hrv"); err != nil {
		t.Fatal(err)
	}
	if n, err := saveDay(ctx, control, d); err != nil || n != 3 {
		t.Fatalf("resume after lost connection: changed=%d err=%v", n, err)
	}
	t.Log("connection terminated during third-table write: no partial day; reconnect and resume passed")
}
