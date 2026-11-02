package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSyncDateFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"completed days", []string{"--last-days", "3", "--exclude-today", "--timezone", "Atlantic/Madeira"}, "GARMIN_API_KEY is required"},
		{"completed one day", []string{"--last-days", "1", "--exclude-today", "--timezone", "UTC"}, "GARMIN_API_KEY is required"},
		{"explicit false in rolling mode", []string{"--last-days", "1", "--exclude-today=false", "--timezone", "UTC"}, "GARMIN_API_KEY is required"},
		{"rolling", []string{"--last-days", "7", "--timezone", "Europe/London"}, "GARMIN_API_KEY is required"},
		{"one day", []string{"--last-days", "1", "--timezone", "UTC"}, "GARMIN_API_KEY is required"},
		{"absolute", []string{"--from", "2026-10-25", "--to", "2026-10-31"}, "GARMIN_API_KEY is required"},
		{"zero days", []string{"--last-days", "0", "--exclude-today", "--timezone", "Europe/London"}, "--last-days must be positive"},
		{"negative days", []string{"--last-days", "-1", "--exclude-today", "--timezone", "Europe/London"}, "--last-days must be positive"},
		{"bad days", []string{"--last-days", "bad", "--timezone", "Europe/London"}, `invalid value "bad" for flag -last-days`},
		{"missing timezone", []string{"--last-days", "7"}, "--timezone is required with --last-days"},
		{"empty timezone", []string{"--last-days", "7", "--timezone", ""}, "--timezone is required with --last-days"},
		{"bad timezone", []string{"--last-days", "7", "--timezone", "Atlantic/Unknown"}, "invalid --timezone"},
		{"host timezone", []string{"--last-days", "7", "--timezone", "Local"}, "invalid --timezone"},
		{"mixed from", []string{"--last-days", "7", "--timezone", "Europe/London", "--from", "2026-10-25"}, "--last-days cannot be combined with --from or --to"},
		{"mixed to", []string{"--last-days", "7", "--timezone", "Europe/London", "--to", "2026-10-31"}, "--last-days cannot be combined with --from or --to"},
		{"mixed empty from", []string{"--last-days", "7", "--timezone", "Europe/London", "--from", ""}, "--last-days cannot be combined with --from or --to"},
		{"timezone with absolute", []string{"--from", "2026-10-25", "--to", "2026-10-31", "--timezone", "UTC"}, "--timezone requires --last-days"},
		{"empty timezone with absolute", []string{"--from", "2026-10-25", "--to", "2026-10-31", "--timezone", ""}, "--timezone requires --last-days"},
		{"exclude today without rolling range", []string{"--exclude-today"}, "--exclude-today requires --last-days"},
		{"exclude today with absolute range", []string{"--from", "2026-10-25", "--to", "2026-10-31", "--exclude-today"}, "--exclude-today requires --last-days"},
		{"explicit false with absolute range", []string{"--from", "2026-10-25", "--to", "2026-10-31", "--exclude-today=false"}, "--exclude-today requires --last-days"},
		{"exclude today mixed with absolute range", []string{"--last-days", "3", "--timezone", "UTC", "--exclude-today", "--from", "2026-10-25"}, "--last-days cannot be combined with --from or --to"},
		{"missing absolute bound", []string{"--from", "2026-10-25"}, "--from and --to must be valid YYYY-MM-DD dates with from <= to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(context.Background(), append([]string{"sync"}, tt.args...), func(string) string { return "" }, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRecentDaysRange(t *testing.T) {
	tests := []struct {
		name         string
		now          string
		days         int
		excludeToday bool
		timezone     string
		from         string
		to           string
	}{
		{"summer timezone ahead of UTC date", "2026-07-15T23:30:00Z", 7, false, "Europe/London", "2026-07-10", "2026-07-16"},
		{"one day before local midnight", "2026-07-15T22:59:59Z", 1, false, "Europe/London", "2026-07-15", "2026-07-15"},
		{"one day at local midnight", "2026-07-15T23:00:00Z", 1, false, "Europe/London", "2026-07-16", "2026-07-16"},
		{"spring DST transition", "2026-03-29T12:00:00Z", 2, false, "Europe/London", "2026-03-28", "2026-03-29"},
		{"day after spring DST transition", "2026-03-30T00:30:00Z", 2, false, "Europe/London", "2026-03-29", "2026-03-30"},
		{"autumn DST transition", "2026-10-25T12:00:00Z", 2, false, "Europe/London", "2026-10-24", "2026-10-25"},
		{"year boundary", "2027-01-01T00:00:00Z", 3, false, "Europe/London", "2026-12-30", "2027-01-01"},
		{"Madeira before local midnight", "2026-07-15T22:59:59Z", 3, true, "Atlantic/Madeira", "2026-07-12", "2026-07-14"},
		{"Madeira at local midnight", "2026-07-15T23:00:00Z", 1, true, "Atlantic/Madeira", "2026-07-15", "2026-07-15"},
		{"Madeira summer date ahead of UTC", "2026-07-15T23:30:00Z", 3, true, "Atlantic/Madeira", "2026-07-13", "2026-07-15"},
		{"completed days through spring DST date", "2026-03-30T12:00:00Z", 3, true, "Europe/London", "2026-03-27", "2026-03-29"},
		{"completed days through autumn DST date", "2026-10-26T12:00:00Z", 3, true, "Europe/London", "2026-10-23", "2026-10-25"},
		{"completed days across year boundary", "2027-01-01T12:00:00Z", 3, true, "Europe/London", "2026-12-29", "2026-12-31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			from, to, err := recentDaysRange(now, tt.days, tt.timezone, tt.excludeToday)
			if err != nil {
				t.Fatal(err)
			}
			if from.Format(time.DateOnly) != tt.from || to.Format(time.DateOnly) != tt.to {
				t.Fatalf("recentDaysRange() = %s through %s, want %s through %s", from.Format(time.DateOnly), to.Format(time.DateOnly), tt.from, tt.to)
			}
			count := 0
			for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
				count++
			}
			if count != tt.days {
				t.Fatalf("range includes %d calendar dates, want %d", count, tt.days)
			}
		})
	}
}
