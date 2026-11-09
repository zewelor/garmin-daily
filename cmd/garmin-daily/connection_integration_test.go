//go:build integration

package main

import (
	"context"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIntegrationCLIConnectionFormats(t *testing.T) {
	t.Setenv("PGSSLCERTMODE", "disable")
	for _, format := range []string{"url", "pg-env", "mixed", "pg-env-bad-password"} {
		t.Run(format, func(t *testing.T) {
			for _, entry := range os.Environ() {
				key, value, _ := strings.Cut(entry, "=")
				if strings.HasPrefix(key, "PG") {
					// Register restoration before removing the inherited setting.
					t.Setenv(key, value)
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			conn, dsn, schema := migrationTestDatabase(t)
			config, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatal("invalid integration database configuration")
			}
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal("invalid integration database URL")
			}
			query := u.Query()
			query.Set("search_path", schema)
			u.RawQuery = query.Encode()
			sslmode := query.Get("sslmode")
			if sslmode == "" {
				sslmode = "prefer"
			}
			for key, value := range map[string]string{
				"PGHOST": config.Host, "PGPORT": strconv.Itoa(int(config.Port)),
				"PGDATABASE": config.Database, "PGUSER": config.User,
				"PGPASSWORD": config.Password, "PGSSLMODE": sslmode,
			} {
				t.Setenv(key, value)
			}
			switch format {
			case "url":
				t.Setenv("PGUSER", "unused_user")
				t.Setenv("PGPASSWORD", "unused-password")
			case "pg-env", "pg-env-bad-password":
				t.Setenv("DATABASE_URL", "")
				t.Setenv("PGOPTIONS", "-c search_path="+schema)
			case "mixed":
				u.User = nil
			}
			if format == "url" || format == "mixed" {
				t.Setenv("DATABASE_URL", u.String())
				t.Setenv("PGHOST", "127.0.0.2")
				t.Setenv("PGPORT", "1")
				t.Setenv("PGDATABASE", "unused_database")
				if sslmode == "disable" {
					t.Setenv("PGSSLMODE", "require")
				}
			}
			if format == "pg-env-bad-password" {
				t.Setenv("PGPASSWORD", "intentionally-wrong-password")
			}
			err = run(context.Background(), []string{"migrate"}, os.Getenv, io.Discard)
			if format == "pg-env-bad-password" {
				if err == nil || !strings.Contains(err.Error(), "PostgreSQL connect failed") {
					t.Fatalf("expected connection error, got %v", err)
				}
				if strings.Contains(err.Error(), "intentionally-wrong-password") {
					t.Fatal("connection error exposed the password")
				}
				var count int
				if err := conn.QueryRow(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_schema=$1", schema).Scan(&count); err != nil || count != 0 {
					t.Fatalf("failed connection changed schema: count=%d error=%v", count, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertMigrationRows(t, conn, schema, len(migrations))
		})
	}
}
