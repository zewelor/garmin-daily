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

func syncRange(ctx context.Context, c *garmin.Client, conn *pgx.Conn, from, to time.Time, dryRun bool, out io.Writer) error {
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		date := day.Format(time.DateOnly)
		d, err := fetchDay(ctx, c, date)
		if err != nil {
			return fmt.Errorf("%s: %w", date, err)
		}
		if dryRun {
			err = printRows(out, dayRows(d))
		} else {
			var changed int64
			changed, err = saveDay(ctx, conn, d)
			if err == nil {
				_, err = fmt.Fprintf(out, "day=%s changed_rows=%d\n", date, changed)
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", date, err)
		}
	}
	return nil
}

func syncActivitiesRange(ctx context.Context, c *garmin.Client, conn *pgx.Conn, from, to time.Time, dryRun bool, out io.Writer) error {
	fromDate, toDate := from.Format(time.DateOnly), to.Format(time.DateOnly)
	activities, err := c.Activities(ctx, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("activity fetch: %w", err)
	}
	rootActivities := make([]garmin.Activity, 0, len(activities))
	for _, activity := range activities {
		if activity.ParentID != nil && *activity.ParentID > 0 {
			continue
		}
		rootActivities = append(rootActivities, activity)
	}
	if err := validateActivities(rootActivities, fromDate, toDate); err != nil {
		return err
	}
	if dryRun {
		for _, a := range rootActivities {
			if err := printRows(out, activityRows(a)); err != nil {
				return err
			}
			if err := printExercisePrune(out, a.ID, len(a.Exercises)); err != nil {
				return err
			}
		}
		return nil
	}
	changed, err := saveActivities(ctx, conn, rootActivities, fromDate, toDate)
	if err != nil {
		return fmt.Errorf("activity save: %w", err)
	}
	_, err = fmt.Fprintf(out, "activities=%d changed_rows=%d\n", len(rootActivities), changed)
	return err
}
