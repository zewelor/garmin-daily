package garmin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRetryDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	const key = "private-api-key"
	const token = "private-token"
	const body = "private-response-body"
	require := func(t *testing.T, output string, parts ...string) {
		t.Helper()
		for _, part := range parts {
			if !strings.Contains(output, part) {
				t.Errorf("output %q does not contain %q", output, part)
			}
		}
		for _, secret := range []string{key, token, body, "Authorization"} {
			if strings.Contains(output, secret) {
				t.Errorf("output leaked %q", secret)
			}
		}
	}

	t.Run("wait then success during authentication", func(t *testing.T) {
		logs.Reset()
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(503)
				fmt.Fprint(w, body)
				return
			}
			fmt.Fprint(w, token)
		}))
		defer s.Close()
		if err := NewClient(s.URL, s.Client()).Authenticate(context.Background(), key); err != nil || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		require(t, logs.String(), "operation=authentication", "http_status=503", "attempt=1", "retry_after=0s", "decision=wait")
	})

	t.Run("over limit during data retrieval", func(t *testing.T) {
		logs.Reset()
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method == http.MethodPost {
				fmt.Fprint(w, token)
				return
			}
			w.Header().Set("Retry-After", "121")
			w.WriteHeader(429)
			fmt.Fprint(w, body)
		}))
		defer s.Close()
		c := NewClient(s.URL, s.Client())
		if err := c.Authenticate(context.Background(), key); err != nil {
			t.Fatal(err)
		}
		_, err := c.DailySummary(context.Background(), "2026-11-08")
		if err == nil || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		require(t, logs.String()+err.Error(), "operation=data", "http_status=429", "attempt=1", "retry_after=121s", "decision=limit_exceeded")
	})

	t.Run("HTTP date", func(t *testing.T) {
		logs.Reset()
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", time.Now().Add(180*time.Second).UTC().Format(http.TimeFormat))
			w.WriteHeader(503)
		}))
		defer s.Close()
		err := NewClient(s.URL, s.Client()).Authenticate(context.Background(), key)
		if err == nil {
			t.Fatal("expected limit error")
		}
		require(t, logs.String()+err.Error(), "http_status=503", "attempt=1", "decision=limit_exceeded")
		match := regexp.MustCompile(`retry_after=([^ ]+)`).FindStringSubmatch(logs.String())
		if len(match) != 2 {
			t.Fatalf("missing computed delay: %s", logs.String())
		}
		delay, parseErr := time.ParseDuration(match[1])
		if parseErr != nil || delay < 170*time.Second || delay > 180*time.Second {
			t.Errorf("computed delay=%q err=%v", match[1], parseErr)
		}
	})

	t.Run("past HTTP date then success", func(t *testing.T) {
		logs.Reset()
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat))
				w.WriteHeader(429)
				return
			}
			fmt.Fprint(w, token)
		}))
		defer s.Close()
		if err := NewClient(s.URL, s.Client()).Authenticate(context.Background(), key); err != nil || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		require(t, logs.String(), "http_status=429", "retry_after=0s", "decision=wait")
	})

	t.Run("invalid header", func(t *testing.T) {
		logs.Reset()
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", body)
			w.WriteHeader(429)
		}))
		defer s.Close()
		err := NewClient(s.URL, s.Client()).Authenticate(context.Background(), key)
		if err == nil {
			t.Fatal("expected invalid header error")
		}
		require(t, logs.String()+err.Error(), "invalid", "Retry-After", "429", "attempt=1")
	})

	t.Run("attempt limit", func(t *testing.T) {
		logs.Reset()
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls < 3 {
				w.Header().Set("Retry-After", "0")
			} else {
				w.Header().Set("Retry-After", body)
			}
			w.WriteHeader(429)
			fmt.Fprint(w, body)
		}))
		defer s.Close()
		err := NewClient(s.URL, s.Client()).Authenticate(context.Background(), key)
		if err == nil || calls != 3 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		require(t, logs.String()+err.Error(), "attempt=1", "attempt=2", "attempt=3", "decision=attempt_limit")
		if strings.Contains(err.Error(), "invalid") {
			t.Errorf("last response should not parse Retry-After: %v", err)
		}
	})

	t.Run("cancellation during wait", func(t *testing.T) {
		logs.Reset()
		calls := 0
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(503)
			time.AfterFunc(50*time.Millisecond, cancel)
		}))
		defer s.Close()
		err := NewClient(s.URL, s.Client()).Authenticate(ctx, key)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		require(t, logs.String(), "http_status=503", "retry_after=30s", "decision=wait")
	})
}
