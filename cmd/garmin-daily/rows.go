package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Row definitions are internal constants. The same columns and values feed
// PostgreSQL writes and the dry-run preview.
type row struct {
	table, keys, columns string
	values               []any
}

func upsert(ctx context.Context, tx pgx.Tx, r row) (int64, error) {
	columns, keys := strings.Fields(r.columns), strings.Fields(r.keys)
	if len(columns) != len(r.values) || len(keys) == 0 {
		return 0, fmt.Errorf("invalid database row definition")
	}
	parameters := make([]string, len(columns))
	var updates []string
	for i, column := range columns {
		parameters[i] = "$" + strconv.Itoa(i+1)
		if !slices.Contains(keys, column) {
			updates = append(updates, column+"=EXCLUDED."+column)
		}
	}
	query := "INSERT INTO " + r.table + " AS stored (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(parameters, ",") +
		") ON CONFLICT (" + strings.Join(keys, ",") + ") DO UPDATE SET " + strings.Join(updates, ",") +
		" WHERE ROW(stored.*) IS DISTINCT FROM ROW(EXCLUDED.*)"
	tag, err := tx.Exec(ctx, query, r.values...)
	return tag.RowsAffected(), err
}

func printRows(out io.Writer, rows []row) error {
	for _, r := range rows {
		columns := strings.Fields(r.columns)
		if len(columns) != len(r.values) {
			return fmt.Errorf("invalid database row definition")
		}
		values := make(map[string]any, len(columns))
		for i, name := range columns {
			values[name] = r.values[i]
		}
		if err := json.NewEncoder(out).Encode(map[string]any{"table": r.table, "values": values}); err != nil {
			return fmt.Errorf("write dry-run output: %w", err)
		}
	}
	return nil
}

func printExercisePrune(out io.Writer, activityID int64, count int) error {
	if err := json.NewEncoder(out).Encode(map[string]any{
		"table": "activity_exercises", "delete": map[string]any{"activity_id": activityID, "exercise_index_gte": count},
	}); err != nil {
		return fmt.Errorf("write dry-run output: %w", err)
	}
	return nil
}
