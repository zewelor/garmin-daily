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

func applyLegacyVersion(t *testing.T, conn *pgx.Conn, version int) {
	t.Helper()
	ctx := context.Background()
	for step := 1; step <= version; step++ {
		if _, err := conn.Exec(ctx, migrations[step-1].sql); err != nil {
			t.Fatalf("apply legacy version %d: %v", step, err)
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
	assertMigrationRows(t, conn, schema, 3)
	if err := migrate(ctx, conn); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	assertMigrationRows(t, conn, schema, 3)
}

func TestIntegrationMigrateAdoptsKnownLegacyVersions(t *testing.T) {
	for legacyVersion := 1; legacyVersion <= 3; legacyVersion++ {
		t.Run(fmt.Sprintf("version_%d", legacyVersion), func(t *testing.T) {
			ctx := context.Background()
			conn, _, schema := migrationTestDatabase(t)
			applyLegacyVersion(t, conn, legacyVersion)
			if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
				t.Fatal(err)
			}
			if legacyVersion >= 2 {
				if _, err := conn.Exec(ctx, `INSERT INTO activities(activity_id,day,started_at,sport) VALUES (7,'2026-01-01','2026-01-01 12:00:00+00','run')`); err != nil {
					t.Fatal(err)
				}
			}
			if legacyVersion >= 3 {
				if _, err := conn.Exec(ctx, `INSERT INTO weight_measurements(measured_at,day,weight_kg) VALUES ('2026-01-01 08:00:00+00','2026-01-01',70.5)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrate(ctx, conn); err != nil {
				t.Fatal(err)
			}
			assertMigrationRows(t, conn, schema, 3)
			var steps, activities, weights int
			if err := conn.QueryRow(ctx, "SELECT steps FROM daily_health WHERE day='2026-01-01'").Scan(&steps); err != nil || steps != 123 {
				t.Fatalf("daily row not preserved: steps=%d err=%v", steps, err)
			}
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM activities").Scan(&activities); err != nil {
				t.Fatal(err)
			}
			if want := boolInt(legacyVersion >= 2); activities != want {
				t.Fatalf("activity rows=%d, want %d", activities, want)
			}
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM weight_measurements").Scan(&weights); err != nil {
				t.Fatal(err)
			}
			if want := boolInt(legacyVersion >= 3); weights != want {
				t.Fatalf("weight rows=%d, want %d", weights, want)
			}
		})
	}
}

func TestIntegrationCatalogFingerprints(t *testing.T) {
	for version := 0; version <= len(migrations); version++ {
		version := version
		t.Run(fmt.Sprintf("application_v%d", version), func(t *testing.T) {
			ctx := context.Background()
			conn, _, schema := migrationTestDatabase(t)
			applyLegacyVersion(t, conn, version)
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := catalogShape(ctx, tx, schema, applicationTables)
			if err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			want := applicationFingerprint(version)
			if actual.fingerprint != want {
				t.Fatalf("fingerprint mismatch: actual=%s expected=%s", actual.fingerprint, want)
			}
		})
	}

	t.Run("metadata", func(t *testing.T) {
		ctx := context.Background()
		conn, _, schema := migrationTestDatabase(t)
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, migrationMetadataSQL); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		actual, err := catalogShape(ctx, tx, schema, []string{"schema_migrations"})
		if err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if actual.fingerprint != migrationMetadataFingerprint {
			t.Fatalf("fingerprint mismatch: actual=%s expected=%s", actual.fingerprint, migrationMetadataFingerprint)
		}
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestIntegrationMigrateRejectsPartialAndWrongTypeSchemas(t *testing.T) {
	tests := []struct {
		name string
		ddl  string
	}{
		{name: "partial", ddl: `CREATE TABLE daily_health (day date PRIMARY KEY)`},
		{name: "wrong_type", ddl: schemaSQL + ` ALTER TABLE daily_health ALTER COLUMN steps TYPE bigint`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			conn, _, _ := migrationTestDatabase(t)
			if _, err := conn.Exec(ctx, test.ddl); err != nil {
				t.Fatal(err)
			}
			if err := migrate(ctx, conn); err == nil {
				t.Fatal("migrate accepted an unknown unversioned schema")
			}
		})
	}
}

func TestIntegrationMigrateRejectsUnknownAndGappedVersions(t *testing.T) {
	tests := []struct {
		name string
		edit string
	}{
		{name: "future", edit: `INSERT INTO schema_migrations(version,name) VALUES (4,'future')`},
		{name: "gap", edit: `DELETE FROM schema_migrations WHERE version=2`},
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
			if err := migrate(ctx, conn); err == nil {
				t.Fatal("migrate accepted unknown or gapped version metadata")
			}
		})
	}
}

func TestIntegrationMigrateRejectsWrongMetadataAndExternalForeignKey(t *testing.T) {
	t.Run("metadata_type", func(t *testing.T) {
		ctx := context.Background()
		conn, _, _ := migrationTestDatabase(t)
		if err := migrate(ctx, conn); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `ALTER TABLE schema_migrations ALTER COLUMN name TYPE varchar`); err != nil {
			t.Fatal(err)
		}
		if err := migrate(ctx, conn); err == nil {
			t.Fatal("migrate accepted a mismatched schema_migrations table")
		}
	})

	t.Run("same_named_external_foreign_key", func(t *testing.T) {
		ctx := context.Background()
		conn, _, schema := migrationTestDatabase(t)
		externalSchema := schema + "_external"
		if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{externalSchema}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{externalSchema}.Sanitize()+" CASCADE"); err != nil {
				t.Error(err)
			}
		})
		applyLegacyVersion(t, conn, 1)
		if _, err := conn.Exec(ctx, "CREATE TABLE "+pgx.Identifier{externalSchema, "daily_health"}.Sanitize()+" (day date PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `ALTER TABLE daily_sleep DROP CONSTRAINT daily_sleep_day_fkey`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, "ALTER TABLE daily_sleep ADD CONSTRAINT daily_sleep_day_fkey FOREIGN KEY (day) REFERENCES "+pgx.Identifier{externalSchema, "daily_health"}.Sanitize()+"(day)"); err != nil {
			t.Fatal(err)
		}
		if err := migrate(ctx, conn); err == nil {
			t.Fatal("migrate accepted a foreign key to an external same-named table")
		}
	})
}

func TestIntegrationMigrateRollbackOnCanceledPendingDDL(t *testing.T) {
	ctx := context.Background()
	conn, dsn, schema := migrationTestDatabase(t)
	applyLegacyVersion(t, conn, 2)
	if _, err := conn.Exec(ctx, `INSERT INTO daily_health(day,steps) VALUES ('2026-01-01',123)`); err != nil {
		t.Fatal(err)
	}
	control, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close(ctx)
	qualifiedHealth := pgx.Identifier{schema, "daily_health"}.Sanitize()
	if _, err := control.Exec(ctx, "BEGIN; LOCK TABLE "+qualifiedHealth+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- migrate(ctx, conn) }()
	deadline := time.Now().Add(5 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		var waitType *string
		if err := control.QueryRow(ctx, "SELECT wait_event_type FROM pg_stat_activity WHERE pid=$1", int64(conn.PgConn().PID())).Scan(&waitType); err != nil {
			t.Fatal(err)
		}
		if waitType != nil && *waitType == "Lock" {
			waiting = true
			break
		}
		select {
		case err := <-result:
			t.Fatalf("migration returned before reaching pending DDL: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("migration did not wait for the locked pending DDL")
	}
	var canceled bool
	if err := control.QueryRow(ctx, "SELECT pg_cancel_backend($1)", int64(conn.PgConn().PID())).Scan(&canceled); err != nil || !canceled {
		t.Fatalf("cancel migration query: canceled=%v err=%v", canceled, err)
	}
	if err := <-result; err == nil {
		t.Fatal("canceled pending migration succeeded")
	}
	if _, err := control.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	var metadataExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&metadataExists); err != nil || metadataExists {
		t.Fatalf("migration metadata survived rollback: exists=%v err=%v", metadataExists, err)
	}
	var respirationColumn bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='daily_health' AND column_name='respiration_avg'
	)`, schema).Scan(&respirationColumn); err != nil || respirationColumn {
		t.Fatalf("pending v3 DDL survived rollback: column=%v err=%v", respirationColumn, err)
	}
	var steps int
	if err := conn.QueryRow(ctx, "SELECT steps FROM daily_health WHERE day='2026-01-01'").Scan(&steps); err != nil || steps != 123 {
		t.Fatalf("legacy data changed after rollback: steps=%d err=%v", steps, err)
	}
	if err := migrate(ctx, conn); err != nil {
		t.Fatalf("retry after releasing lock: %v", err)
	}
	assertMigrationRows(t, conn, schema, 3)
}
