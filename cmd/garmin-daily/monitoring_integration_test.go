//go:build integration

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zewelor/garmin-daily/garmin"
)

func TestIntegrationDryRunMatchesDatabaseRows(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	var schema string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	installPreviewHTTP(t)
	getenv := func(name string) string {
		switch name {
		case "DATABASE_URL":
			return dsn.String()
		case "GARMIN_API_KEY":
			return "synthetic-key"
		}
		return ""
	}
	args := []string{"sync", "--from", "2026-11-02", "--to", "2026-11-02"}
	var preview bytes.Buffer
	if err := run(ctx, append(args, "--dry-run"), getenv, &preview); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM daily_health").Scan(&count); err != nil || count != 0 {
		t.Fatalf("dry-run wrote rows: count=%d error=%v", count, err)
	}
	if err := run(ctx, args, getenv, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	wantCounts := make(map[string]int)
	for _, r := range decodePreview(t, preview.String()) {
		if r.Values == nil {
			continue
		}
		wantCounts[r.Table]++
		values, err := json.Marshal(r.Values)
		if err != nil {
			t.Fatal(err)
		}
		// Let PostgreSQL compare values using their actual column types, so date,
		// timestamp and numeric formatting differences do not hide precision loss.
		sql := "SELECT count(*) FROM " + r.Table + " AS actual WHERE actual IS NOT DISTINCT FROM jsonb_populate_record(NULL::" + r.Table + ", $1::jsonb)"
		if err := conn.QueryRow(ctx, sql, values).Scan(&count); err != nil || count != 1 {
			t.Fatalf("preview differs from stored %s row: matches=%d error=%v", r.Table, count, err)
		}
	}
	for table, want := range wantCounts {
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("stored %s count=%d want=%d error=%v", table, count, want, err)
		}
	}
}

func monitoringTestDay(day string) dayData {
	var d dayData
	d.Health.Day = day
	d.Sleep.Day = day
	d.HRV.Day = day
	return d
}

func monitoringTestWeight(day string, at time.Time, grams string) garmin.WeightMeasurement {
	weight := json.Number(grams)
	return garmin.WeightMeasurement{
		Day:       day,
		Timestamp: &garmin.EpochMillis{Time: at.UTC()},
		Weight:    &weight,
	}
}

func TestIntegrationDailyMonitoringPersistence(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	const day = "2026-10-27"
	d := monitoringTestDay(day)
	d.Health.Respiration.Average = monitoringTestInt16(15)
	d.Health.Respiration.Min = monitoringTestInt16(7)
	d.Health.Respiration.Max = monitoringTestInt16(27)
	d.Health.Respiration.Latest = monitoringTestInt16(14)
	d.Health.Stress.RestMillis = monitoringTestInt64(1000)
	d.Health.Stress.ActivityMillis = monitoringTestInt64(0)
	d.Health.Stress.UncategorizedMillis = monitoringTestInt64(20)
	d.Health.Stress.LowMillis = monitoringTestInt64(30)
	d.Health.Stress.MediumMillis = monitoringTestInt64(40)
	d.Health.Stress.HighMillis = monitoringTestInt64(50)

	if changed, err := saveDay(ctx, conn, d); err != nil || changed != 3 {
		t.Fatalf("initial daily monitoring save changed=%d err=%v", changed, err)
	}
	var avg, min, max, latest sql.NullInt16
	var rest, activity, uncategorized, low, medium, high sql.NullInt32
	read := func() {
		t.Helper()
		err := conn.QueryRow(ctx, `SELECT respiration_avg,respiration_min,respiration_max,respiration_latest,
			stress_rest_ms,stress_activity_ms,stress_uncategorized_ms,stress_low_ms,stress_medium_ms,stress_high_ms
			FROM daily_health WHERE day=$1`, day).Scan(
			&avg, &min, &max, &latest, &rest, &activity, &uncategorized, &low, &medium, &high)
		if err != nil {
			t.Fatal(err)
		}
	}
	read()
	if !avg.Valid || avg.Int16 != 15 || !min.Valid || min.Int16 != 7 || !max.Valid || max.Int16 != 27 || !latest.Valid || latest.Int16 != 14 ||
		!rest.Valid || rest.Int32 != 1000 || !activity.Valid || activity.Int32 != 0 || !uncategorized.Valid || uncategorized.Int32 != 20 ||
		!low.Valid || low.Int32 != 30 || !medium.Valid || medium.Int32 != 40 || !high.Valid || high.Int32 != 50 {
		t.Fatalf("daily monitoring values differ: respiration=%+v/%+v/%+v/%+v stress=%+v/%+v/%+v/%+v/%+v/%+v",
			avg, min, max, latest, rest, activity, uncategorized, low, medium, high)
	}

	d.Health.Respiration.Average = monitoringTestInt16(16)
	d.Health.Respiration.Min = nil
	d.Health.Respiration.Max = monitoringTestInt16(28)
	d.Health.Respiration.Latest = monitoringTestInt16(0)
	d.Health.Stress.RestMillis = monitoringTestInt64(1001)
	d.Health.Stress.ActivityMillis = nil
	d.Health.Stress.UncategorizedMillis = monitoringTestInt64(0)
	d.Health.Stress.LowMillis = nil
	d.Health.Stress.MediumMillis = monitoringTestInt64(0)
	d.Health.Stress.HighMillis = monitoringTestInt64(55)
	if changed, err := saveDay(ctx, conn, d); err != nil || changed != 1 {
		t.Fatalf("daily monitoring correction changed=%d err=%v", changed, err)
	}
	read()
	if !avg.Valid || avg.Int16 != 16 || min.Valid || !max.Valid || max.Int16 != 28 || !latest.Valid || latest.Int16 != 0 ||
		!rest.Valid || rest.Int32 != 1001 || activity.Valid || !uncategorized.Valid || uncategorized.Int32 != 0 ||
		low.Valid || !medium.Valid || medium.Int32 != 0 || !high.Valid || high.Int32 != 55 {
		t.Fatalf("daily monitoring correction/null/zero values differ: respiration=%+v/%+v/%+v/%+v stress=%+v/%+v/%+v/%+v/%+v/%+v",
			avg, min, max, latest, rest, activity, uncategorized, low, medium, high)
	}
}

func TestIntegrationActivityHeartRateZonePersistence(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	a := integrationActivity()
	const day = "2026-10-26"
	a.Zone1Seconds = activityNumber("123.45678901234567890123")
	a.Zone3Seconds = activityNumber("0")
	a.Zone5Seconds = activityNumber("7.000000000000000001")
	if changed, err := saveActivities(ctx, conn, []garmin.Activity{a}, day, day); err != nil || changed != 3 {
		t.Fatalf("initial zone save changed=%d err=%v", changed, err)
	}
	var zone1, zone3, zone5 string
	var zone2Null, zone4Null bool
	if err := conn.QueryRow(ctx, `SELECT heart_rate_zone_1_s::text,heart_rate_zone_2_s IS NULL,
		heart_rate_zone_3_s::text,heart_rate_zone_4_s IS NULL,heart_rate_zone_5_s::text
		FROM activities WHERE activity_id=$1`, a.ID).Scan(&zone1, &zone2Null, &zone3, &zone4Null, &zone5); err != nil {
		t.Fatal(err)
	}
	if zone1 != "123.45678901234567890123" || !zone2Null || zone3 != "0" || !zone4Null || zone5 != "7.000000000000000001" {
		t.Fatalf("zone values lost precision or NULLs: %q null=%v %q null=%v %q", zone1, zone2Null, zone3, zone4Null, zone5)
	}

	corrected := a
	corrected.Zone1Seconds = nil
	corrected.Zone2Seconds = activityNumber("99.1234567890123456789")
	corrected.Zone3Seconds = nil
	corrected.Zone4Seconds = activityNumber("0")
	corrected.Zone5Seconds = nil
	if changed, err := saveActivities(ctx, conn, []garmin.Activity{corrected}, day, day); err != nil || changed != 1 {
		t.Fatalf("zone correction changed=%d err=%v", changed, err)
	}
	var zone1Null, zone3Null, zone5Null bool
	var zone2, zone4 string
	if err := conn.QueryRow(ctx, `SELECT heart_rate_zone_1_s IS NULL,heart_rate_zone_2_s::text,
		heart_rate_zone_3_s IS NULL,heart_rate_zone_4_s::text,heart_rate_zone_5_s IS NULL
		FROM activities WHERE activity_id=$1`, a.ID).Scan(&zone1Null, &zone2, &zone3Null, &zone4, &zone5Null); err != nil {
		t.Fatal(err)
	}
	if !zone1Null || zone2 != "99.1234567890123456789" || !zone3Null || zone4 != "0" || !zone5Null {
		t.Fatalf("zone correction did not preserve exact values and NULLs: null=%v %q null=%v %q null=%v", zone1Null, zone2, zone3Null, zone4, zone5Null)
	}
}

func TestIntegrationWeightMeasurementsUpsertAndRetention(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	const day = "2026-10-27"
	first := monitoringTestWeight(day, time.Date(2026, 10, 27, 7, 30, 0, 0, time.UTC), "82345.67890123456789")
	second := monitoringTestWeight(day, time.Date(2026, 10, 27, 19, 45, 0, 0, time.UTC), "80000")
	measurements := []garmin.WeightMeasurement{first, second}
	if changed, err := saveWeights(ctx, conn, measurements, day, day); err != nil || changed != 2 {
		t.Fatalf("initial weight save changed=%d err=%v", changed, err)
	}
	var firstKg, secondKg string
	if err := conn.QueryRow(ctx, `SELECT weight_kg::text FROM weight_measurements WHERE measured_at=$1`, first.Timestamp.Time).Scan(&firstKg); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT weight_kg::text FROM weight_measurements WHERE measured_at=$1`, second.Timestamp.Time).Scan(&secondKg); err != nil {
		t.Fatal(err)
	}
	if firstKg != "82.34567890123456789" || secondKg != "80" {
		t.Fatalf("gram-to-kilogram conversion lost precision: first=%q second=%q", firstKg, secondKg)
	}
	var xminBefore, xminAfter string
	if err := conn.QueryRow(ctx, `SELECT xmin::text FROM weight_measurements WHERE measured_at=$1`, first.Timestamp.Time).Scan(&xminBefore); err != nil {
		t.Fatal(err)
	}
	if changed, err := saveWeights(ctx, conn, measurements, day, day); err != nil || changed != 0 {
		t.Fatalf("repeat weight save changed=%d err=%v", changed, err)
	}
	if err := conn.QueryRow(ctx, `SELECT xmin::text FROM weight_measurements WHERE measured_at=$1`, first.Timestamp.Time).Scan(&xminAfter); err != nil || xminAfter != xminBefore {
		t.Fatalf("unchanged weight was rewritten: before=%s after=%s err=%v", xminBefore, xminAfter, err)
	}

	corrected := first
	corrected.Weight = activityNumber("81000")
	if changed, err := saveWeights(ctx, conn, []garmin.WeightMeasurement{corrected}, day, day); err != nil || changed != 1 {
		t.Fatalf("weight correction changed=%d err=%v", changed, err)
	}
	if err := conn.QueryRow(ctx, `SELECT weight_kg::text FROM weight_measurements WHERE measured_at=$1`, first.Timestamp.Time).Scan(&firstKg); err != nil || firstKg != "81" {
		t.Fatalf("corrected weight=%q err=%v", firstKg, err)
	}
	corrected.WeightDelta = activityNumber("-12.0000000000057")
	if changed, err := saveWeights(ctx, conn, []garmin.WeightMeasurement{corrected}, day, day); err != nil || changed != 1 {
		t.Fatalf("delta update changed=%d err=%v", changed, err)
	}
	var delta string
	if err := conn.QueryRow(ctx, `SELECT weight_delta::text FROM weight_measurements WHERE measured_at=$1`, first.Timestamp.Time).Scan(&delta); err != nil || delta != "-12.0000000000057" {
		t.Fatalf("stored delta=%q err=%v", delta, err)
	}
	corrected.WeightDelta = nil
	if changed, err := saveWeights(ctx, conn, []garmin.WeightMeasurement{corrected}, day, day); err != nil || changed != 1 {
		t.Fatalf("missing delta update changed=%d err=%v", changed, err)
	}
	var deltaMissing bool
	if err := conn.QueryRow(ctx, `SELECT weight_delta IS NULL FROM weight_measurements WHERE measured_at=$1`, first.Timestamp.Time).Scan(&deltaMissing); err != nil || !deltaMissing {
		t.Fatalf("missing delta was not persisted: is_null=%v err=%v", deltaMissing, err)
	}
	if changed, err := saveWeights(ctx, conn, nil, day, day); err != nil || changed != 0 {
		t.Fatalf("empty weight response changed=%d err=%v", changed, err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM weight_measurements").Scan(&count); err != nil || count != 2 {
		t.Fatalf("empty response removed existing measurements: count=%d err=%v", count, err)
	}
}

func TestIntegrationWeightValidationRejectsWholeBatch(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	const day = "2026-10-27"
	valid := monitoringTestWeight(day, time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC), "80000")
	invalid := []struct {
		name        string
		measurement garmin.WeightMeasurement
	}{
		{name: "missing weight", measurement: func() garmin.WeightMeasurement { w := valid; w.Weight = nil; return w }()},
		{name: "missing timestamp", measurement: func() garmin.WeightMeasurement { w := valid; w.Timestamp = nil; return w }()},
		{name: "negative weight", measurement: func() garmin.WeightMeasurement { w := valid; w.Weight = activityNumber("-1"); return w }()},
		{name: "date outside requested range", measurement: func() garmin.WeightMeasurement { w := valid; w.Day = "2026-10-28"; return w }()},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			measurement := tc.measurement
			if measurement.Timestamp != nil {
				measurement.Timestamp = &garmin.EpochMillis{Time: measurement.Timestamp.Add(time.Hour)}
			}
			if changed, err := saveWeights(ctx, conn, []garmin.WeightMeasurement{valid, measurement}, day, day); err == nil || changed != 0 {
				t.Fatalf("invalid batch changed=%d err=%v", changed, err)
			}
			var count int
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM weight_measurements").Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid batch partially wrote weights: count=%d err=%v", count, err)
			}
		})
	}
	duplicate := valid
	duplicate.Weight = activityNumber("81000")
	if changed, err := saveWeights(ctx, conn, []garmin.WeightMeasurement{valid, duplicate}, day, day); err == nil || changed != 0 {
		t.Fatalf("duplicate timestamps changed=%d err=%v", changed, err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM weight_measurements").Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate timestamps partially wrote weights: count=%d err=%v", count, err)
	}
	withDelta := valid
	withDelta.WeightDelta = activityNumber("-100")
	conflictingDelta := valid
	conflictingDelta.WeightDelta = activityNumber("200")
	if changed, err := saveWeights(ctx, conn, []garmin.WeightMeasurement{withDelta, conflictingDelta}, day, day); err == nil || changed != 0 {
		t.Fatalf("conflicting deltas changed=%d err=%v", changed, err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM weight_measurements").Scan(&count); err != nil || count != 0 {
		t.Fatalf("conflicting deltas partially wrote weights: count=%d err=%v", count, err)
	}
}

func TestIntegrationWeightTransactionRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	const day = "2026-10-27"
	if _, err := conn.Exec(ctx, `CREATE FUNCTION reject_second_weight() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.measured_at = '2026-10-27 12:00:00+00'::timestamptz THEN
				RAISE EXCEPTION 'simulated second weight write failure';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER reject_second_weight BEFORE INSERT OR UPDATE ON weight_measurements
		FOR EACH ROW EXECUTE FUNCTION reject_second_weight()`); err != nil {
		t.Fatal(err)
	}
	measurements := []garmin.WeightMeasurement{
		monitoringTestWeight(day, time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC), "80000"),
		monitoringTestWeight(day, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), "81000"),
	}
	if changed, err := saveWeights(ctx, conn, measurements, day, day); err == nil || changed != 0 {
		t.Fatalf("trigger failure changed=%d err=%v", changed, err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM weight_measurements").Scan(&count); err != nil || count != 0 {
		t.Fatalf("weight transaction left a partial batch: count=%d err=%v", count, err)
	}
}

func monitoringTestInt16(value int16) *int16 { return &value }
func monitoringTestInt64(value int64) *int64 { return &value }

func TestIntegrationSyncMonitoringEndToEnd(t *testing.T) {
	ctx := context.Background()
	conn := testDatabase(t)
	var schema string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/personalapitoken-service/personal-api/personal-api-key/token":
			fmt.Fprint(w, "synthetic-token")
		case "/usersummary-service/v2/dailysummaries":
			fmt.Fprintf(w, `{"data":[{"calendarDate":%q,"respiration":{"avgValue":15},"stress":{"restDurationInMillis":900}}]}`, r.URL.Query().Get("startDate"))
		case "/sleep-service/sleep/dailySleep", "/hrv-service/hrv/2026-10-27", "/hrv-service/hrv/2026-10-28":
			w.WriteHeader(204)
		case "/activitylist-service/activities/search/activities":
			if r.URL.Query().Get("start") == "1" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, `[{"activityId":123,"activityType":{"typeKey":"running"},"startTimeLocal":"2026-10-28 00:15:00","startTimeGMT":"2026-10-27 23:15:00","hrTimeInZone_2":2.25}]`)
		case "/weight-service/weight/all/2026-10-27/2026-10-28":
			fmt.Fprint(w, `[{"calendarDate":"2026-10-28","timestampGMT":1793143800000,"weight":0,"weightDelta":-900.0000000000057},{"calendarDate":"2026-10-28","timestampGMT":1793143800000,"weight":82345.6,"weightDelta":0},{"calendarDate":"2026-10-28","timestampGMT":1793143800000,"weight":82345.600,"weightDelta":null},{"calendarDate":"2026-10-28","timestampGMT":1793172600000,"weight":82400}]`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	original := http.DefaultTransport
	base := server.Client().Transport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(server.URL, "http://")
		return base.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	getenv := func(name string) string {
		if name == "GARMIN_API_KEY" {
			return "synthetic-key"
		}
		if name == "DATABASE_URL" {
			return dsn.String()
		}
		return ""
	}
	args := []string{"sync", "--from", "2026-10-27", "--to", "2026-10-28"}
	var out strings.Builder
	if err := run(ctx, args, getenv, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "activities=1 changed_rows=1") || !strings.Contains(out.String(), "weights=2 changed_rows=2") {
		t.Fatalf("unexpected sync summary: %s", &out)
	}
	var days, weights int
	var zone, kg string
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM daily_health WHERE respiration_avg=15 AND stress_rest_ms=900").Scan(&days); err != nil || days != 2 {
		t.Fatalf("daily monitoring count=%d err=%v", days, err)
	}
	if err := conn.QueryRow(ctx, "SELECT heart_rate_zone_2_s::text FROM activities WHERE activity_id=123").Scan(&zone); err != nil || zone != "2.25" {
		t.Fatalf("stored zone time=%s err=%v", zone, err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*),min(weight_kg)::text FROM weight_measurements WHERE day='2026-10-28'").Scan(&weights, &kg); err != nil || weights != 2 || kg != "82.3456" {
		t.Fatalf("weight calendar day, count or units differ: count=%d kg=%s err=%v", weights, kg, err)
	}
	var delta string
	if err := conn.QueryRow(ctx, "SELECT weight_delta::text FROM weight_measurements WHERE weight_kg=82.3456").Scan(&delta); err != nil || delta != "-900.0000000000057" {
		t.Fatalf("weight delta=%q err=%v", delta, err)
	}
	var missingDelta bool
	if err := conn.QueryRow(ctx, "SELECT weight_delta IS NULL FROM weight_measurements WHERE weight_kg=82.4").Scan(&missingDelta); err != nil || !missingDelta {
		t.Fatalf("missing weight delta: is_null=%v err=%v", missingDelta, err)
	}
	out.Reset()
	if err := run(ctx, args, getenv, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "changed_rows=0") != 4 {
		t.Fatalf("repeat sync rewrote unchanged data: %s", &out)
	}
}
