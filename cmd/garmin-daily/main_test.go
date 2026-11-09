package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRejectCLIInputsBeforeIO(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}, {"sync"}, {"sync", "--from", "2026-02-30", "--to", "2026-03-01"}, {"sync", "--from", "2026-10-25", "--to", "2026-10-24"}, {"migrate", "extra"}, {"sync", "--from", "2026-10-25", "--to", "2026-10-25"}} {
		if err := run(context.Background(), args, func(string) string { return "" }, io.Discard); err == nil {
			t.Fatalf("accepted invalid input %v", args)
		}
	}
}

func TestRetryWaitEnvironmentControlsAuthentication(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 504, Header: http.Header{"Retry-After": []string{"2"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		value string
		wait  bool
	}{{"1s", false}, {"2s", true}} {
		t.Run(tc.value, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := run(ctx, []string{"sync", "--dry-run", "--from", "2026-11-08", "--to", "2026-11-08"}, func(name string) string {
				switch name {
				case "GARMIN_API_KEY":
					return "key"
				case "GARMIN_RETRY_MAX_WAIT":
					return tc.value
				}
				return ""
			}, io.Discard)
			if tc.wait && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected wait until cancellation, got %v", err)
			}
			if !tc.wait && (err == nil || !strings.Contains(err.Error(), "exceeds 1s")) {
				t.Fatalf("expected configured limit error, got %v", err)
			}
		})
	}
}

func TestRejectInvalidRetryWaitBeforeIO(t *testing.T) {
	for _, value := range []string{"invalid", "-1s", "0", "120", "999999999999999999999h"} {
		t.Run(value, func(t *testing.T) {
			getenv := func(key string) string {
				switch key {
				case "GARMIN_API_KEY":
					return "key"
				case "GARMIN_RETRY_MAX_WAIT":
					return value
				}
				return ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := run(ctx, []string{"sync", "--dry-run", "--from", "2026-11-08", "--to", "2026-11-08"}, getenv, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "GARMIN_RETRY_MAX_WAIT") {
				t.Fatalf("expected configuration error, got %v", err)
			}
		})
	}
}

func TestValidRetryWaitReachesAuthentication(t *testing.T) {
	for _, value := range []string{"", "2m", "150s"} {
		t.Run(value, func(t *testing.T) {
			getenv := func(key string) string {
				switch key {
				case "GARMIN_API_KEY":
					return "key"
				case "GARMIN_RETRY_MAX_WAIT":
					return value
				}
				return ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := run(ctx, []string{"sync", "--dry-run", "--from", "2026-11-08", "--to", "2026-11-08"}, getenv, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "authentication:") {
				t.Fatalf("expected authentication attempt, got %v", err)
			}
		})
	}
}
