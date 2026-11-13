package main

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The SQL files are the immutable source for their numbered migrations.
//
//go:embed schema.sql
var schemaSQL string

//go:embed activity_schema.sql
var activitySchemaSQL string

//go:embed monitoring_schema.sql
var monitoringSchemaSQL string

//go:embed weight_delta_schema.sql
var weightDeltaSchemaSQL string

const migrationMetadataSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version integer PRIMARY KEY,
	name text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

type migration struct {
	name string
	sql  string
}

var migrations = []migration{
	{name: "daily", sql: schemaSQL},
	{name: "activities", sql: activitySchemaSQL},
	{name: "monitoring", sql: monitoringSchemaSQL},
	{name: "weight_delta", sql: weightDeltaSchemaSQL},
}

type migrationRefusal string

func (e migrationRefusal) Error() string { return "migration refused: " + string(e) }

func setMigrationSearchPath(ctx context.Context, tx pgx.Tx, schema string) error {
	_, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgx.Identifier{schema}.Sanitize()+", pg_catalog")
	return err
}

func readMigrationVersion(ctx context.Context, tx pgx.Tx, schema string) (int, error) {
	table := pgx.Identifier{schema, "schema_migrations"}.Sanitize()
	rows, err := tx.Query(ctx, "SELECT version, name FROM "+table+" ORDER BY version")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	applied := 0
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return 0, err
		}
		if version > len(migrations) {
			return 0, migrationRefusal("database has a future migration version")
		}
		if version != applied+1 || name != migrations[version-1].name {
			return 0, migrationRefusal("migration metadata has a gap or unexpected version name")
		}
		applied = version
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return applied, nil
}

func migrate(ctx context.Context, conn *pgx.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := checkDatabaseVersion(ctx, conn); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var schema string
		if err := tx.QueryRow(ctx, "SELECT COALESCE(current_schema(),'')").Scan(&schema); err != nil {
			return err
		}
		if schema == "" {
			return migrationRefusal("current_schema() is not set")
		}
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1::text,$2::bigint))", schema, int64(0x47444d)); err != nil {
			return err
		}
		if err := setMigrationSearchPath(ctx, tx, schema); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, migrationMetadataSQL); err != nil {
			return err
		}
		applied, err := readMigrationVersion(ctx, tx, schema)
		if err != nil {
			return err
		}

		for version := applied; version < len(migrations); version++ {
			step := migrations[version]
			if _, err := tx.Exec(ctx, step.sql); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version,name) VALUES ($1,$2)", version+1, step.name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var refusal migrationRefusal
		if errors.As(err, &refusal) {
			return err
		}
		return databaseError("migration", err)
	}
	return nil
}
