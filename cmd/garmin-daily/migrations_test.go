//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestIntegrationMigrateRejectsOlderPostgreSQL(t *testing.T) {
	ctx := context.Background()
	conn, dsn, schema := migrationTestDatabase(t)
	var version int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version >= 180000 {
		t.Skip("requires PostgreSQL older than 18")
	}
	if err := migrate(ctx, conn); err == nil || !strings.Contains(err.Error(), "PostgreSQL 18 or newer is required") {
		t.Fatalf("expected minimum-version error, got %v", err)
	}
	databaseURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := databaseURL.Query()
	query.Set("search_path", schema)
	databaseURL.RawQuery = query.Encode()
	for _, command := range []string{"migrate", "sync", "sync-activities"} {
		args := []string{command}
		if command != "migrate" {
			args = append(args, "--from", "2026-11-02", "--to", "2026-11-02")
		}
		err := run(ctx, args, func(name string) string {
			switch name {
			case "DATABASE_URL":
				return databaseURL.String()
			case "GARMIN_API_KEY":
				return "synthetic-key"
			}
			return ""
		}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "PostgreSQL 18 or newer is required") {
			t.Fatalf("%s: expected minimum-version error, got %v", command, err)
		}
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, schema).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unsupported server schema changed: relations=%d error=%v", count, err)
	}
}

func migrationTestDatabase(t *testing.T) (*pgx.Conn, string, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("garmin_migration_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(ctx)
		t.Fatal(err)
	}
	if err := setTestSearchPath(ctx, conn, schema); err != nil {
		conn.Close(ctx)
		admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close(context.Background())
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close(context.Background())
	})
	return conn, dsn, schema
}

func setTestSearchPath(ctx context.Context, conn *pgx.Conn, schema string) error {
	_, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()+", pg_catalog")
	return err
}

func applyMigrationSteps(t *testing.T, conn *pgx.Conn, version int) {
	t.Helper()
	ctx := context.Background()
	for step := 1; step <= version; step++ {
		if _, err := conn.Exec(ctx, migrations[step-1].sql); err != nil {
			t.Fatalf("apply migration %d: %v", step, err)
		}
	}
}

func assertMigrationRows(t *testing.T, conn *pgx.Conn, schema string, want int) {
	t.Helper()
	var count int
	table := pgx.Identifier{schema, "schema_migrations"}.Sanitize()
	if err := conn.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("migration rows=%d, want %d", count, want)
	}
}

func TestIntegrationMigrateFreshRepeatAndConcurrent(t *testing.T) {
	ctx := context.Background()
	conn, dsn, schema := migrationTestDatabase(t)
	second, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	if err := setTestSearchPath(ctx, second, schema); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, candidate := range []*pgx.Conn{conn, second} {
		go func(candidate *pgx.Conn) {
			<-start
			results <- migrate(ctx, candidate)
		}(candidate)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	assertMigrationRows(t, conn, schema, len(migrations))
	if err := migrate(ctx, conn); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	assertMigrationRows(t, conn, schema, len(migrations))
}

func TestIntegrationMigrateRejectsUnversionedApplicationTables(t *testing.T) {
	for version := 1; version <= len(migrations); version++ {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			ctx := context.Background()
			conn, _, _ := migrationTestDatabase(t)
			applyMigrationSteps(t, conn, version)
			if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
				t.Fatal(err)
			}
			if version >= 3 {
				if _, err := conn.Exec(ctx, `INSERT INTO weight_measurements(measured_at,day,weight_kg) VALUES ('2026-01-01 08:00:00+00','2026-01-01',70.5)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrate(ctx, conn); err == nil || !strings.Contains(err.Error(), "42P07") {
				t.Fatalf("expected duplicate-table SQLSTATE without legacy adoption, got %v", err)
			}
			var metadataExists bool
			if err := conn.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&metadataExists); err != nil || metadataExists {
				t.Fatalf("failed migration left history: exists=%v err=%v", metadataExists, err)
			}
			var steps int
			if err := conn.QueryRow(ctx, `SELECT steps FROM daily_health`).Scan(&steps); err != nil || steps != 123 {
				t.Fatalf("existing data changed: steps=%d err=%v", steps, err)
			}
			if version >= 3 {
				var weight string
				if err := conn.QueryRow(ctx, `SELECT weight_kg::text FROM weight_measurements`).Scan(&weight); err != nil || weight != "70.5" {
					t.Fatalf("existing weight changed: weight=%q err=%v", weight, err)
				}
			}
		})
	}
}

func TestIntegrationMigrateUsesReadableHistoryWithoutShapeCheck(t *testing.T) {
	ctx := context.Background()
	conn, _, schema := migrationTestDatabase(t)
	if err := migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `ALTER TABLE schema_migrations ADD COLUMN note text`); err != nil {
		t.Fatal(err)
	}
	before := migrationHistory(t, conn, len(migrations))
	if err := migrate(ctx, conn); err != nil {
		t.Fatalf("readable history should not require a structural fingerprint: %v", err)
	}
	assertMigrationRows(t, conn, schema, len(migrations))
	if after := migrationHistory(t, conn, len(migrations)); after != before {
		t.Fatalf("existing history changed: before=%s after=%s", before, after)
	}
}

func TestIntegrationMigrateReportsUnreadableHistory(t *testing.T) {
	ctx := context.Background()
	conn, _, _ := migrationTestDatabase(t)
	if _, err := conn.Exec(ctx, `CREATE TABLE schema_migrations (version integer PRIMARY KEY); INSERT INTO schema_migrations VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, conn); err == nil || !strings.Contains(err.Error(), "42703") {
		t.Fatalf("expected missing-column SQLSTATE, got %v", err)
	}
	var version int
	if err := conn.QueryRow(ctx, `SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("unreadable history changed: version=%d err=%v", version, err)
	}
	var healthExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('daily_health') IS NOT NULL`).Scan(&healthExists); err != nil || healthExists {
		t.Fatalf("failed history read created application tables: exists=%v err=%v", healthExists, err)
	}
}

func TestIntegrationMigrateRollsBackFreshDatabase(t *testing.T) {
	ctx := context.Background()
	conn, _, schema := migrationTestDatabase(t)
	appendTestMigrations(t, migration{name: "fail", sql: `SELECT * FROM nonexistent_migration_table`})
	if err := migrate(ctx, conn); err == nil || !strings.Contains(err.Error(), "42P01") {
		t.Fatalf("expected undefined-table SQLSTATE, got %v", err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, schema).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed fresh migration left relations: count=%d err=%v", count, err)
	}
	migrations[len(migrations)-1].sql = `SELECT 1`
	if err := migrate(ctx, conn); err != nil {
		t.Fatalf("retry fresh migration: %v", err)
	}
	assertMigrationRows(t, conn, schema, len(migrations))
}

func TestIntegrationMigratePreservesVersionedHistory(t *testing.T) {
	for version := 1; version <= len(migrations); version++ {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			ctx := context.Background()
			conn, _, schema := migrationTestDatabase(t)
			applyMigrationSteps(t, conn, version)
			if _, err := conn.Exec(ctx, migrationMetadataSQL); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < version; i++ {
				if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES ($1,$2,'2026-01-01 00:00:00+00')`, i+1, migrations[i].name); err != nil {
					t.Fatal(err)
				}
			}
			before := migrationHistory(t, conn, version)
			if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
				t.Fatal(err)
			}
			if version >= 3 {
				if _, err := conn.Exec(ctx, `INSERT INTO weight_measurements(measured_at,day,weight_kg) VALUES ('2026-01-01 08:00:00+00','2026-01-01',70.5)`); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := migrate(ctx, conn); err != nil {
					t.Fatal(err)
				}
			}
			assertMigrationRows(t, conn, schema, len(migrations))
			if after := migrationHistory(t, conn, version); after != before {
				t.Fatalf("existing history changed: before=%s after=%s", before, after)
			}
			var steps int
			if err := conn.QueryRow(ctx, `SELECT steps FROM daily_health`).Scan(&steps); err != nil || steps != 123 {
				t.Fatalf("existing data changed: steps=%d err=%v", steps, err)
			}
			if version >= 3 {
				var weight string
				var missingDelta bool
				if err := conn.QueryRow(ctx, `SELECT weight_kg::text,weight_delta IS NULL FROM weight_measurements`).Scan(&weight, &missingDelta); err != nil || weight != "70.5" || !missingDelta {
					t.Fatalf("existing weight=%q delta_is_null=%v err=%v", weight, missingDelta, err)
				}
			}
		})
	}
}

func TestIntegrationMigrateTrustsRecordedApplicationVersion(t *testing.T) {
	for _, edit := range []string{
		`ALTER TABLE daily_health ALTER COLUMN steps TYPE bigint`,
		`ALTER TABLE daily_sleep DROP CONSTRAINT daily_sleep_day_fkey`,
	} {
		t.Run(edit, func(t *testing.T) {
			ctx := context.Background()
			conn, _, schema := migrationTestDatabase(t)
			if err := migrate(ctx, conn); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
				t.Fatal(err)
			}
			before := migrationHistory(t, conn, len(migrations))
			if _, err := conn.Exec(ctx, edit); err != nil {
				t.Fatal(err)
			}
			if err := migrate(ctx, conn); err != nil {
				t.Fatalf("valid history should be trusted despite application drift: %v", err)
			}
			assertMigrationRows(t, conn, schema, len(migrations))
			if after := migrationHistory(t, conn, len(migrations)); after != before {
				t.Fatalf("existing migration history changed: before=%s after=%s", before, after)
			}
			var steps int
			if err := conn.QueryRow(ctx, `SELECT steps FROM daily_health WHERE day='2026-01-01'`).Scan(&steps); err != nil || steps != 123 {
				t.Fatalf("data changed: steps=%d err=%v", steps, err)
			}
		})
	}
}

func migrationHistory(t *testing.T, conn *pgx.Conn, through int) string {
	t.Helper()
	var history string
	if err := conn.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(m) ORDER BY version)::text,'[]') FROM schema_migrations m WHERE version <= $1`, through).Scan(&history); err != nil {
		t.Fatal(err)
	}
	return history
}

// Migration tests run sequentially; restore the production list after each test.
func appendTestMigrations(t *testing.T, steps ...migration) {
	t.Helper()
	original := migrations
	migrations = append(append([]migration(nil), original...), steps...)
	t.Cleanup(func() { migrations = original })
}

func TestIntegrationMigrateDataStepOnceWithConcurrentRuns(t *testing.T) {
	ctx := context.Background()
	conn, dsn, schema := migrationTestDatabase(t)
	if err := migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123);
		INSERT INTO weight_measurements(measured_at,day,weight_kg) VALUES ('2026-01-01 08:00:00+00','2026-01-01',70.5);
		UPDATE schema_migrations SET applied_at='2026-01-01 00:00:00+00'
	`); err != nil {
		t.Fatal(err)
	}
	previousVersion := len(migrations)
	before := migrationHistory(t, conn, previousVersion)
	appendTestMigrations(t, migration{name: "correct_data", sql: `
		UPDATE daily_health SET steps=steps+1;
		UPDATE weight_measurements SET weight_kg=weight_kg+0.1
	`})
	second, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	if err := setTestSearchPath(ctx, second, schema); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, candidate := range []*pgx.Conn{conn, second} {
		go func(candidate *pgx.Conn) {
			<-start
			results <- migrate(ctx, candidate)
		}(candidate)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("concurrent data migration: %v", err)
		}
	}
	if err := migrate(ctx, conn); err != nil {
		t.Fatalf("repeat data migration: %v", err)
	}
	var steps int
	var weight string
	if err := conn.QueryRow(ctx, `SELECT steps FROM daily_health`).Scan(&steps); err != nil || steps != 124 {
		t.Fatalf("data step was not applied exactly once: steps=%d err=%v", steps, err)
	}
	if err := conn.QueryRow(ctx, `SELECT weight_kg::text FROM weight_measurements`).Scan(&weight); err != nil || weight != "70.6" {
		t.Fatalf("weight correction was not applied exactly once: weight=%q err=%v", weight, err)
	}
	assertMigrationRows(t, conn, schema, len(migrations))
	if after := migrationHistory(t, conn, previousVersion); after != before {
		t.Fatalf("existing history changed: before=%s after=%s", before, after)
	}
}

func TestIntegrationMigrateSchemaStep(t *testing.T) {
	ctx := context.Background()
	conn, _, schema := migrationTestDatabase(t)
	if err := migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
		t.Fatal(err)
	}
	appendTestMigrations(t, migration{name: "extra_column", sql: `ALTER TABLE daily_health ADD COLUMN extra integer`})
	for range 2 {
		if err := migrate(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}
	assertMigrationRows(t, conn, schema, len(migrations))
	var steps int
	var extra *int
	if err := conn.QueryRow(ctx, `SELECT steps,extra FROM daily_health`).Scan(&steps, &extra); err != nil || steps != 123 || extra != nil {
		t.Fatalf("data changed during schema upgrade: steps=%d extra=%v err=%v", steps, extra, err)
	}
}

func TestIntegrationMigrateRejectsUnknownAndGappedVersions(t *testing.T) {
	tests := []struct {
		name string
		edit string
	}{
		{name: "future", edit: `INSERT INTO schema_migrations(version,name) VALUES (5,'future')`},
		{name: "gap", edit: `DELETE FROM schema_migrations WHERE version=2`},
		{name: "wrong_name", edit: `UPDATE schema_migrations SET name='unexpected' WHERE version=2`},
		{name: "zero", edit: `INSERT INTO schema_migrations(version,name) VALUES (0,'invalid')`},
		{name: "negative", edit: `INSERT INTO schema_migrations(version,name) VALUES (-1,'invalid')`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			conn, _, _ := migrationTestDatabase(t)
			if err := migrate(ctx, conn); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, test.edit); err != nil {
				t.Fatal(err)
			}
			before := migrationHistory(t, conn, len(migrations)+1)
			if err := migrate(ctx, conn); err == nil {
				t.Fatal("migrate accepted unknown or gapped version metadata")
			}
			if after := migrationHistory(t, conn, len(migrations)+1); after != before {
				t.Fatalf("refused history changed: before=%s after=%s", before, after)
			}
		})
	}
}

func TestIntegrationMigrateRollsBackAppliedSteps(t *testing.T) {
	for _, failure := range []string{"bad_sql", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			conn, dsn, schema := migrationTestDatabase(t)
			if err := migrate(ctx, conn); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, `
				INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123);
				INSERT INTO weight_measurements(measured_at,day,weight_kg) VALUES ('2026-01-01 08:00:00+00','2026-01-01',70.5);
				CREATE TABLE migration_gate (id integer)
			`); err != nil {
				t.Fatal(err)
			}
			before := migrationHistory(t, conn, len(migrations))
			failingSQL := `SELECT * FROM nonexistent_migration_table`
			if failure == "canceled" {
				failingSQL = `SELECT * FROM migration_gate`
			}
			appendTestMigrations(t,
				migration{name: "change_data", sql: `
					CREATE TABLE migration_probe (id integer);
					UPDATE daily_health SET steps=steps+1;
					UPDATE weight_measurements SET weight_kg=weight_kg+0.1
				`},
				migration{name: "fail", sql: failingSQL},
			)
			control, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close(ctx)
			if err := setTestSearchPath(ctx, control, schema); err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if failure == "canceled" {
				if _, err := control.Exec(ctx, `BEGIN; LOCK TABLE migration_gate IN ACCESS EXCLUSIVE MODE`); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- migrate(runCtx, conn) }()
				deadline := time.Now().Add(5 * time.Second)
				for {
					var blocked bool
					if err := control.QueryRow(ctx, `SELECT $1::integer=ANY(pg_blocking_pids($2))`, int32(control.PgConn().PID()), int32(conn.PgConn().PID())).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					select {
					case err := <-result:
						t.Fatalf("migration returned before the second step: %v", err)
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("migration did not reach the blocked second step")
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
				err = <-result
				if _, rollbackErr := control.Exec(ctx, `ROLLBACK`); rollbackErr != nil {
					t.Fatal(rollbackErr)
				}
			} else {
				err = migrate(runCtx, conn)
			}
			if err == nil {
				t.Fatal("failed migration succeeded")
			}
			if failure == "bad_sql" && !strings.Contains(err.Error(), "42P01") {
				t.Fatalf("expected undefined-table SQLSTATE, got %v", err)
			}
			var probeExists bool
			if err := control.QueryRow(ctx, `SELECT to_regclass('migration_probe') IS NOT NULL`).Scan(&probeExists); err != nil || probeExists {
				t.Fatalf("first-step DDL survived rollback: exists=%v err=%v", probeExists, err)
			}
			var steps int
			var weight string
			if err := control.QueryRow(ctx, `SELECT steps FROM daily_health`).Scan(&steps); err != nil || steps != 123 {
				t.Fatalf("first-step data survived rollback: steps=%d err=%v", steps, err)
			}
			if err := control.QueryRow(ctx, `SELECT weight_kg::text FROM weight_measurements`).Scan(&weight); err != nil || weight != "70.5" {
				t.Fatalf("first-step weight survived rollback: weight=%q err=%v", weight, err)
			}
			if after := migrationHistory(t, control, len(migrations)); after != before {
				t.Fatalf("failed steps changed history: before=%s after=%s", before, after)
			}
			// Retry on a usable connection after correcting the test-only SQL.
			migrations[len(migrations)-1].sql = `SELECT 1`
			if err := migrate(ctx, control); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
			assertMigrationRows(t, control, schema, len(migrations))
		})
	}
}

func TestIntegrationMigrateIsolatesSelectedSchema(t *testing.T) {
	ctx := context.Background()
	conn, _, schema := migrationTestDatabase(t)
	other, _, otherSchema := migrationTestDatabase(t)
	if err := migrate(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `
		INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',999);
		INSERT INTO schema_migrations(version,name) VALUES (99,'future')
	`); err != nil {
		t.Fatal(err)
	}
	before := migrationHistory(t, other, 99)
	// Metadata exists only in a later search_path schema; it must not be adopted.
	searchPath := pgx.Identifier{schema}.Sanitize() + ", " + pgx.Identifier{otherSchema}.Sanitize()
	if _, err := conn.Exec(ctx, "SET search_path TO "+searchPath); err != nil {
		t.Fatal(err)
	}
	var originalPath string
	if err := conn.QueryRow(ctx, `SHOW search_path`).Scan(&originalPath); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrate(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
		t.Fatal(err)
	}
	assertMigrationRows(t, conn, schema, len(migrations))
	if after := migrationHistory(t, other, 99); after != before {
		t.Fatalf("other schema history changed: before=%s after=%s", before, after)
	}
	for _, test := range []struct {
		conn *pgx.Conn
		want int
	}{{conn, 123}, {other, 999}} {
		var steps int
		if err := test.conn.QueryRow(ctx, `SELECT steps FROM daily_health`).Scan(&steps); err != nil || steps != test.want {
			t.Fatalf("schema data changed: steps=%d want=%d err=%v", steps, test.want, err)
		}
	}
	var restoredPath string
	if err := conn.QueryRow(ctx, `SHOW search_path`).Scan(&restoredPath); err != nil || restoredPath != originalPath {
		t.Fatalf("session search_path changed: actual=%q want=%q err=%v", restoredPath, originalPath, err)
	}
}

func TestIntegrationMigrateRejectsMissingCurrentSchema(t *testing.T) {
	ctx := context.Background()
	conn, _, schema := migrationTestDatabase(t)
	if _, err := conn.Exec(ctx, `SET search_path TO nonexistent_migration_schema`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, conn); err == nil || !strings.Contains(err.Error(), "current_schema() is not set") {
		t.Fatalf("expected missing-schema refusal, got %v", err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, schema).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing-schema migration created relations: count=%d err=%v", count, err)
	}
}

func TestIntegrationSyncCommandsDoNotMigrate(t *testing.T) {
	for _, command := range []string{"sync", "sync-activities"} {
		t.Run(command, func(t *testing.T) {
			ctx := context.Background()
			conn, dsn, schema := migrationTestDatabase(t)
			databaseURL, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			query := databaseURL.Query()
			query.Set("search_path", schema)
			databaseURL.RawQuery = query.Encode()
			installPreviewHTTP(t)
			err = run(ctx, []string{command, "--from", "2026-11-02", "--to", "2026-11-02"}, func(name string) string {
				switch name {
				case "DATABASE_URL":
					return databaseURL.String()
				case "GARMIN_API_KEY":
					return "synthetic-key"
				}
				return ""
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "42P01") {
				t.Fatalf("expected missing-table SQLSTATE, got %v", err)
			}
			var count int
			if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1`, schema).Scan(&count); err != nil || count != 0 {
				t.Fatalf("sync created relations: count=%d err=%v", count, err)
			}
		})
	}
}
