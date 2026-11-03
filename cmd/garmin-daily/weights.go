package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/zewelor/garmin-daily/garmin"
)

func weightKilograms(w garmin.WeightMeasurement) (pgtype.Numeric, error) {
	value, err := (databaseNumber{w.Weight}).NumericValue()
	if err != nil || !value.Valid {
		return pgtype.Numeric{}, fmt.Errorf("missing or invalid weight")
	}
	// Numeric is Int * 10^Exp. The verified live source uses grams, so moving
	// the decimal exponent preserves every digit without a float64 conversion.
	if value.Exp < -math.MaxInt16+3 {
		return pgtype.Numeric{}, fmt.Errorf("weight precision exceeds supported range")
	}
	value.Exp -= 3
	return value, nil
}

func validateWeights(weights []garmin.WeightMeasurement, from, to string) error {
	if !validDay(from) || !validDay(to) || from > to {
		return fmt.Errorf("invalid weight date range")
	}
	seen := make(map[int64]bool)
	for _, w := range weights {
		if !validDay(w.Day) || w.Day < from || w.Day > to || w.Timestamp == nil ||
			w.Timestamp.UnixMilli() < 0 || w.Timestamp.UnixMilli() > 253402300799999 {
			return fmt.Errorf("invalid weight date or timestamp")
		}
		if seen[w.Timestamp.UnixMilli()] {
			return fmt.Errorf("duplicate weight timestamp")
		}
		seen[w.Timestamp.UnixMilli()] = true
		if _, err := weightKilograms(w); err != nil {
			return err
		}
	}
	return nil
}

func saveWeights(ctx context.Context, conn *pgx.Conn, weights []garmin.WeightMeasurement, from, to string) (int64, error) {
	if err := validateWeights(weights, from, to); err != nil {
		return 0, err
	}
	if len(weights) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var changed int64
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, w := range weights {
			kg, err := weightKilograms(w)
			if err != nil {
				return err
			}
			day, _ := time.Parse(time.DateOnly, w.Day)
			tag, err := tx.Exec(ctx, `INSERT INTO weight_measurements (measured_at,day,weight_kg)
				VALUES ($1,$2,$3) ON CONFLICT (measured_at) DO UPDATE
				SET day=EXCLUDED.day,weight_kg=EXCLUDED.weight_kg
				WHERE ROW(weight_measurements.day,weight_measurements.weight_kg)
				IS DISTINCT FROM ROW(EXCLUDED.day,EXCLUDED.weight_kg)`, w.Timestamp.Time, day, kg)
			if err != nil {
				return err
			}
			changed += tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return 0, databaseError("weight transaction", err)
	}
	return changed, nil
}

func syncWeightsRange(ctx context.Context, c *garmin.Client, conn *pgx.Conn, from, to time.Time, out io.Writer) error {
	fromDate, toDate := from.Format(time.DateOnly), to.Format(time.DateOnly)
	weights, err := c.WeightMeasurements(ctx, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("weight fetch: %w", err)
	}
	changed, err := saveWeights(ctx, conn, weights, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("weight save: %w", err)
	}
	_, err = fmt.Fprintf(out, "weights=%d changed_rows=%d\n", len(weights), changed)
	return err
}
