package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
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

func weightDeltaNumber(delta *json.Number) (pgtype.Numeric, error) {
	var value pgtype.Numeric
	if delta == nil {
		return value, nil
	}
	if err := value.ScanScientific(delta.String()); err != nil || !value.Valid || value.NaN || value.InfinityModifier != pgtype.Finite || value.Int == nil || value.Exp < -math.MaxInt16 {
		return pgtype.Numeric{}, fmt.Errorf("invalid weight delta")
	}
	return value, nil
}

// A missing value is distinct from zero; among present values, prefer a
// nonzero value and reject incompatible nonzero values.
func mergeWeightValue(a, b *json.Number) (*json.Number, error) {
	if a == nil {
		return b, nil
	}
	if b == nil {
		return a, nil
	}
	first, ok := new(big.Rat).SetString(a.String())
	if !ok {
		return nil, fmt.Errorf("invalid weight value")
	}
	second, ok := new(big.Rat).SetString(b.String())
	if !ok {
		return nil, fmt.Errorf("invalid weight value")
	}
	if first.Sign() == 0 {
		return b, nil
	}
	if second.Sign() == 0 || first.Cmp(second) == 0 {
		return a, nil
	}
	return nil, fmt.Errorf("conflicting weight measurements for timestamp")
}

func normalizeWeights(weights []garmin.WeightMeasurement, from, to string) ([]garmin.WeightMeasurement, error) {
	if !validDay(from) || !validDay(to) || from > to {
		return nil, fmt.Errorf("invalid weight date range")
	}
	seen := make(map[int64]int)
	var result []garmin.WeightMeasurement
	for _, w := range weights {
		if !validDay(w.Day) || w.Day < from || w.Day > to || w.Timestamp == nil ||
			w.Timestamp.UnixMilli() < 0 || w.Timestamp.UnixMilli() > 253402300799999 {
			return nil, fmt.Errorf("invalid weight date or timestamp")
		}
		if _, err := weightKilograms(w); err != nil {
			return nil, err
		}
		if _, err := weightDeltaNumber(w.WeightDelta); err != nil {
			return nil, err
		}
		if i, exists := seen[w.Timestamp.UnixMilli()]; exists {
			if result[i].Day != w.Day {
				return nil, fmt.Errorf("conflicting weight measurements for timestamp")
			}
			var err error
			result[i].Weight, err = mergeWeightValue(result[i].Weight, w.Weight)
			if err != nil {
				return nil, err
			}
			result[i].WeightDelta, err = mergeWeightValue(result[i].WeightDelta, w.WeightDelta)
			if err != nil {
				return nil, err
			}
			continue
		}
		seen[w.Timestamp.UnixMilli()] = len(result)
		result = append(result, w)
	}
	return result, nil
}

func saveWeights(ctx context.Context, conn *pgx.Conn, weights []garmin.WeightMeasurement, from, to string) (int64, error) {
	weights, err := normalizeWeights(weights, from, to)
	if err != nil {
		return 0, err
	}
	if len(weights) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var changed int64
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, w := range weights {
			r, err := weightRow(w)
			if err != nil {
				return err
			}
			n, err := upsert(ctx, tx, r)
			if err != nil {
				return err
			}
			changed += n
		}
		return nil
	})
	if err != nil {
		return 0, databaseError("weight transaction", err)
	}
	return changed, nil
}

func weightRow(w garmin.WeightMeasurement) (row, error) {
	kg, err := weightKilograms(w)
	if err != nil {
		return row{}, err
	}
	delta, err := weightDeltaNumber(w.WeightDelta)
	if err != nil {
		return row{}, err
	}
	day, _ := time.Parse(time.DateOnly, w.Day)
	return row{"weight_measurements", "measured_at", "measured_at day weight_kg weight_delta", []any{
		w.Timestamp.Time, pgtype.Date{Time: day, Valid: true}, kg, delta,
	}}, nil
}

func syncWeightsRange(ctx context.Context, c *garmin.Client, conn *pgx.Conn, from, to time.Time, dryRun bool, out io.Writer) error {
	fromDate, toDate := from.Format(time.DateOnly), to.Format(time.DateOnly)
	weights, err := c.WeightMeasurements(ctx, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("weight fetch: %w", err)
	}
	weights, err = normalizeWeights(weights, fromDate, toDate)
	if err != nil {
		return err
	}
	if dryRun {
		for _, w := range weights {
			r, err := weightRow(w)
			if err != nil {
				return err
			}
			if err := printRows(out, []row{r}); err != nil {
				return err
			}
		}
		return nil
	}
	changed, err := saveWeights(ctx, conn, weights, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("weight save: %w", err)
	}
	_, err = fmt.Fprintf(out, "weights=%d changed_rows=%d\n", len(weights), changed)
	return err
}
