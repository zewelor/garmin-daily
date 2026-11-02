package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zewelor/garmin-daily/garmin"
)

func fetchDay(ctx context.Context, c *garmin.Client, day string) (dayData, error) {
	var d dayData
	if !validDay(day) {
		return d, fmt.Errorf("invalid date")
	}
	h, err := c.DailySummary(ctx, day)
	if err != nil {
		return d, fmt.Errorf("daily summary: %w", err)
	}
	d.Health = h
	s, err := c.DailySleep(ctx, day)
	if err != nil {
		return d, fmt.Errorf("sleep: %w", err)
	}
	d.Sleep.Day = day
	if s != nil {
		d.Sleep = *s
	}
	r, err := c.DailyHRV(ctx, day)
	if err != nil {
		return d, fmt.Errorf("HRV: %w", err)
	}
	d.HRV.Day = day
	if r != nil {
		d.HRV = *r
	}
	return d, d.validate(day)
}

func syncRange(ctx context.Context, c *garmin.Client, conn *pgx.Conn, from, to time.Time, out io.Writer) error {
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		date := day.Format(time.DateOnly)
		d, err := fetchDay(ctx, c, date)
		if err != nil {
			return fmt.Errorf("%s: %w", date, err)
		}
		changed, err := saveDay(ctx, conn, d)
		if err != nil {
			return fmt.Errorf("%s: %w", date, err)
		}
		fmt.Fprintf(out, "day=%s changed_rows=%d\n", date, changed)
	}
	return nil
}
