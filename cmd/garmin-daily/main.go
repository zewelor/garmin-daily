package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/zewelor/garmin-daily/garmin"
)

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: garmin-daily migrate | sync [--dry-run] | sync-activities [--dry-run] [date range flags]")
	}
	command := args[0]
	if command != "sync" && command != "sync-activities" && command != "migrate" {
		return fmt.Errorf("unknown command; use migrate, sync or sync-activities")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	var from, to, timezone string
	var lastDays int
	var excludeToday, dryRun bool
	syncCommand := command == "sync" || command == "sync-activities"
	if syncCommand {
		flags.BoolVar(&dryRun, "dry-run", false, "print incoming PostgreSQL rows without connecting to the database")
		flags.StringVar(&from, "from", "", "first Garmin calendar date (inclusive)")
		flags.StringVar(&to, "to", "", "last Garmin calendar date (inclusive)")
		flags.IntVar(&lastDays, "last-days", 0, "number of Garmin calendar dates ending today")
		flags.StringVar(&timezone, "timezone", "", "IANA timezone for --last-days (required)")
		flags.BoolVar(&excludeToday, "exclude-today", false, "end --last-days on yesterday instead of today")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var start, end time.Time
	if syncCommand {
		var absolute, rolling, zoned, excludeTodaySet bool
		flags.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "from", "to":
				absolute = true
			case "last-days":
				rolling = true
			case "timezone":
				zoned = true
			case "exclude-today":
				excludeTodaySet = true
			}
		})
		if rolling && absolute {
			return fmt.Errorf("--last-days cannot be combined with --from or --to")
		}
		if zoned && !rolling {
			return fmt.Errorf("--timezone requires --last-days")
		}
		if excludeTodaySet && !rolling {
			return fmt.Errorf("--exclude-today requires --last-days")
		}
		if rolling {
			var err error
			start, end, err = recentDaysRange(time.Now(), lastDays, timezone, excludeToday)
			if err != nil {
				return err
			}
		} else {
			if !validDay(from) || !validDay(to) || from > to {
				return fmt.Errorf("--from and --to must be valid YYYY-MM-DD dates with from <= to")
			}
			start, _ = time.Parse(time.DateOnly, from)
			end, _ = time.Parse(time.DateOnly, to)
		}
	}
	key := strings.TrimSpace(getenv("GARMIN_API_KEY"))
	if syncCommand && key == "" {
		return fmt.Errorf("GARMIN_API_KEY is required")
	}
	retryMaxWait := garmin.DefaultRetryMaxWait
	if syncCommand {
		if value := getenv("GARMIN_RETRY_MAX_WAIT"); value != "" {
			var err error
			retryMaxWait, err = time.ParseDuration(value)
			if err != nil || retryMaxWait <= 0 {
				return fmt.Errorf("GARMIN_RETRY_MAX_WAIT must be a positive duration, e.g. 2m")
			}
		}
	}
	var conn *pgx.Conn
	if !dryRun {
		// An empty DSN lets pgx use the standard PG* environment variables.
		dsn := getenv("DATABASE_URL")
		connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		var err error
		conn, err = pgx.Connect(connectCtx, dsn)
		cancel()
		if err != nil {
			return databaseError("connect", err)
		}
		defer conn.Close(context.Background())
		if command == "migrate" {
			return migrate(ctx, conn)
		}
		if err := checkDatabaseVersion(ctx, conn); err != nil {
			return err
		}
	}
	httpClient := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c := garmin.NewClient(garmin.BaseURL, httpClient)
	c.RetryMaxWait = retryMaxWait
	if err := c.Authenticate(ctx, key); err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	if command == "sync" {
		if err := syncRange(ctx, c, conn, start, end, dryRun, out); err != nil {
			return err
		}
	}
	if err := syncActivitiesRange(ctx, c, conn, start, end, dryRun, out); err != nil {
		return err
	}
	if command == "sync" {
		return syncWeightsRange(ctx, c, conn, start, end, dryRun, out)
	}
	return nil
}

func recentDaysRange(now time.Time, days int, timezone string, excludeToday bool) (time.Time, time.Time, error) {
	if days <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("--last-days must be positive")
	}
	if timezone == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("--timezone is required with --last-days")
	}
	if timezone == "Local" {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --timezone: use a named IANA timezone or UTC")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --timezone: %w", err)
	}
	year, month, day := now.In(location).Date()
	end := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if excludeToday {
		end = end.AddDate(0, 0, -1)
	}
	return end.AddDate(0, 0, 1-days), end, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "garmin-daily:", err)
		os.Exit(1)
	}
}
