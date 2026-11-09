package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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

const migrationMetadataSQL = `CREATE TABLE schema_migrations (
	version integer PRIMARY KEY,
	name text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`

type migration struct {
	name                   string
	sql                    string
	applicationFingerprint string
}

// These SHA-256 values are generated offline from catalogShape's ordered JSON.
// Recompute with `TEST_DATABASE_URL=... go test -tags=integration ./cmd/garmin-daily -run TestIntegrationCatalogFingerprints -v`;
// the test reports actual values when a constant no longer matches.
const emptyApplicationFingerprint = "290ea3e24aaaef4338292f7029566088ff74fc1f6bb5a3c7abbd76c385fec916"
const migrationMetadataFingerprint = "99b0e791d10c2a6f23ea7ffc2849b4bbe8ebd442a38f33a0f23d4f0393b89256"

var migrations = [...]migration{
	{name: "daily", sql: schemaSQL, applicationFingerprint: "270f3b9fb037f74c7a4828e323330bf2c2f95a0f27af9d4584a8b4d36df2b484"},
	{name: "activities", sql: activitySchemaSQL, applicationFingerprint: "e08c0b638510148042b9c5d47aabe84026bc135558a1c0362f4365875bf85555"},
	{name: "monitoring", sql: monitoringSchemaSQL, applicationFingerprint: "dbb2491eda8704b5c5b86628fec3500876f84a671f14baea346fdd9929354a95"},
	{name: "weight_delta", sql: weightDeltaSchemaSQL, applicationFingerprint: "c8f440eee413fe4d254d472b8720789ffe888c5d28ce18a68405f89cd6fe7aa9"},
}

var applicationTables = []string{
	"daily_health", "daily_sleep", "daily_hrv",
	"activities", "activity_exercises", "weight_measurements",
}

type migrationRefusal string

func (e migrationRefusal) Error() string { return "migration refused: " + string(e) }

type schemaShape struct {
	fingerprint string
	relations   int
}

// catalogShape fingerprints only the structural contract used by this app.
// Its output is compared with shapes built from the embedded, versioned SQL.
func catalogShape(ctx context.Context, tx pgx.Tx, schema string, tables []string) (schemaShape, error) {
	shape := schemaShape{}
	var catalog string
	err := tx.QueryRow(ctx, `
		WITH owned AS (
			SELECT c.* FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname=$1 AND c.relname=ANY($2)
		)
		SELECT (SELECT count(*) FROM owned), jsonb_build_object(
			'relations', COALESCE((SELECT jsonb_agg(jsonb_build_array(relname,relkind::text) ORDER BY relname) FROM owned), '[]'::jsonb),
			'columns', COALESCE((SELECT jsonb_agg(jsonb_build_array(c.relname,a.attnum,a.attname,
				format_type(a.atttypid,a.atttypmod),a.attnotnull,COALESCE(pg_get_expr(d.adbin,d.adrelid,true),''),
				a.attidentity::text,a.attgenerated::text) ORDER BY c.relname,a.attnum)
				FROM owned c JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
				LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum), '[]'::jsonb),
			'constraints', COALESCE((SELECT jsonb_agg(jsonb_build_array(c.relname,con.conname,con.contype::text,
				COALESCE(pg_get_expr(con.conbin,con.conrelid,true),''),COALESCE(ref.relname,''),COALESCE(ref.relnamespace=c.relnamespace,false),con.conkey,con.confkey,
				con.confupdtype::text,con.confdeltype::text,con.confmatchtype::text,con.convalidated,
				con.condeferrable,con.condeferred,con.connoinherit,con.conislocal,con.coninhcount)
				ORDER BY c.relname,con.conname)
				FROM owned c JOIN pg_constraint con ON con.conrelid=c.oid
				LEFT JOIN pg_class ref ON ref.oid=con.confrelid), '[]'::jsonb),
			'triggers', COALESCE((SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname) ORDER BY c.relname,t.tgname)
				FROM owned c JOIN pg_trigger t ON t.tgrelid=c.oid AND NOT t.tgisinternal), '[]'::jsonb)
		)::text`, schema, tables).Scan(&shape.relations, &catalog)
	if err != nil {
		return shape, err
	}
	sum := sha256.Sum256([]byte(catalog))
	shape.fingerprint = hex.EncodeToString(sum[:])
	return shape, nil
}

func setMigrationSearchPath(ctx context.Context, tx pgx.Tx, schema string) error {
	_, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgx.Identifier{schema}.Sanitize()+", pg_catalog")
	return err
}

func applicationFingerprint(version int) string {
	if version == 0 {
		return emptyApplicationFingerprint
	}
	return migrations[version-1].applicationFingerprint
}

func readMigrationVersion(ctx context.Context, tx pgx.Tx, schema string) (int, error) {
	table := pgx.Identifier{schema, "schema_migrations"}.Sanitize()
	rows, err := tx.Query(ctx, "SELECT version, name FROM "+table+" ORDER BY version")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	versionCount := 0
	expectedVersion := 1
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return 0, err
		}
		if version > len(migrations) {
			return 0, migrationRefusal("database has a future migration version")
		}
		if version != expectedVersion || name != migrations[version-1].name {
			return 0, migrationRefusal("migration metadata has a gap or unexpected version name")
		}
		versionCount++
		expectedVersion++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return versionCount, nil
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

		actualMetadata, err := catalogShape(ctx, tx, schema, []string{"schema_migrations"})
		if err != nil {
			return err
		}
		metadataExists := actualMetadata.relations > 0
		if metadataExists && actualMetadata.fingerprint != migrationMetadataFingerprint {
			return migrationRefusal("schema_migrations has an unexpected structure")
		}
		actualApplication, err := catalogShape(ctx, tx, schema, applicationTables)
		if err != nil {
			return err
		}

		var applied int
		if !metadataExists {
			legacyVersion := -1
			for version := 0; version <= len(migrations); version++ {
				if actualApplication.fingerprint == applicationFingerprint(version) {
					legacyVersion = version
				}
			}
			if legacyVersion < 0 {
				return migrationRefusal("unversioned application schema is partial or does not match a known version")
			}
			if _, err := tx.Exec(ctx, migrationMetadataSQL); err != nil {
				return err
			}
			for version := 1; version <= legacyVersion; version++ {
				if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version,name) VALUES ($1,$2)", version, migrations[version-1].name); err != nil {
					return err
				}
			}
			applied = legacyVersion
		} else {
			applied, err = readMigrationVersion(ctx, tx, schema)
			if err != nil {
				return err
			}
			if actualApplication.fingerprint != applicationFingerprint(applied) {
				return migrationRefusal("application schema does not match its recorded version")
			}
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
