package garmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultRetryWaitAcceptsTwoMinutes(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := NewClient(s.URL, s.Client()).Authenticate(ctx, "key")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected to wait until cancellation, got %v", err)
	}
}

func TestConfiguredRetryWait(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		limit  time.Duration
		wait   bool
	}{
		{"accept at limit", "2", 2 * time.Second, true},
		{"reject above limit", "2", time.Second, false},
		{"reject date above limit", time.Now().Add(3 * time.Minute).UTC().Format(http.TimeFormat), time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", tc.header)
				w.WriteHeader(http.StatusGatewayTimeout)
			}))
			defer s.Close()
			c := NewClient(s.URL, s.Client())
			c.RetryMaxWait = tc.limit
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := c.Authenticate(ctx, "key")
			if tc.wait && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected to wait until cancellation, got %v", err)
			}
			if !tc.wait && (err == nil || !strings.Contains(err.Error(), "exceeds "+tc.limit.String())) {
				t.Fatalf("expected configured limit error, got %v", err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d, want 1", calls)
			}
		})
	}
}

func TestHTTPFailures(t *testing.T) {
	for _, status := range []int{401, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				fmt.Fprint(w, "secret response")
			}))
			defer s.Close()
			c := NewClient(s.URL, s.Client())
			err := c.Authenticate(context.Background(), "test-key")
			want := 1
			if status == 429 || status == 500 {
				want = 3
			}
			if calls != want || err == nil {
				t.Fatalf("calls=%d want=%d err=%v", calls, want, err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "test-key") {
				t.Fatal("leaked credentials/body")
			}
		})
	}
}

func TestRetryThenSuccessAndCancellation(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, "token")
	}))
	defer s.Close()
	c := NewClient(s.URL, s.Client())
	if err := c.Authenticate(context.Background(), "key"); err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Authenticate(ctx, "key"); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestTimeoutAndLongRetryAfter(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, "token")
		}))
		defer s.Close()
		h := s.Client()
		h.Timeout = 5 * time.Millisecond
		if err := NewClient(s.URL, h).Authenticate(context.Background(), "key"); err == nil {
			t.Fatal("ignored timeout")
		}
	})
	t.Run("long retry-after", func(t *testing.T) {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", "99999999999")
			w.WriteHeader(429)
		}))
		defer s.Close()
		if err := NewClient(s.URL, s.Client()).Authenticate(context.Background(), "key"); err == nil || calls != 1 {
			t.Fatalf("unbounded retry calls=%d err=%v", calls, err)
		}
	})
}

func TestDailySleepNoNightVariant(t *testing.T) {
	const day = "2026-08-08"
	const noNightFields = `"calendarDate":null,` +
		`"sleepStartTimestampGMT":null,"sleepEndTimestampGMT":null,"sleepTimeSeconds":null,` +
		`"napTimeSeconds":%d,"deepSleepSeconds":null,"lightSleepSeconds":null,"remSleepSeconds":null,` +
		`"awakeSleepSeconds":null,"unmeasurableSleepSeconds":null,"avgHeartRate":null,"avgSleepStress":null,` +
		`"averageRespirationValue":null,"averageSpO2Value":null,"sleepScores":{"overall":{"value":null}}`
	response := func(fields, sleepNeed string) string {
		return fmt.Sprintf(`{%s,"sleepNeed":%s}`, fields, sleepNeed)
	}
	metadata := fmt.Sprintf(`{"calendarDate":%q}`, day)
	fields := func(nap int32) string { return fmt.Sprintf(noNightFields, nap) }

	for _, tc := range []struct {
		name string
		nap  int32
	}{{"zero", 0}, {"positive", 123}} {
		t.Run("preserves "+tc.name+" nap", func(t *testing.T) {
			body := response(fields(tc.nap), metadata)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer server.Close()

			sleep, err := NewClient(server.URL, server.Client()).DailySleep(context.Background(), day)
			if err != nil {
				t.Fatal(err)
			}
			if sleep.Day != day || sleep.Nap == nil || *sleep.Nap != tc.nap {
				t.Fatalf("day=%q nap=%v, want day=%q nap=%d", sleep.Day, sleep.Nap, day, tc.nap)
			}
			if sleep.Start != nil || sleep.End != nil || sleep.Duration != nil || sleep.Deep != nil ||
				sleep.Light != nil || sleep.REM != nil || sleep.Awake != nil || sleep.Unmeasurable != nil ||
				sleep.HeartRate != nil || sleep.Stress != nil || sleep.Respiration != nil || sleep.SpO2 != nil ||
				sleep.Scores.Overall.Value != nil {
				t.Fatal("absent nightly measurements became populated")
			}
		})
	}

	base := fields(0)
	withoutRootDate := strings.TrimPrefix(base, `"calendarDate":null,`)
	wrongRootDate := strings.Replace(base, `"calendarDate":null`, `"calendarDate":"2026-08-07"`, 1)
	withoutNap := strings.Replace(base, `"napTimeSeconds":0`, `"napTimeSeconds":null`, 1)
	withDuration := strings.Replace(base, `"sleepTimeSeconds":null`, `"sleepTimeSeconds":1`, 1)
	withScore := strings.Replace(base, `"value":null`, `"value":75`, 1)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing root date", response(withoutRootDate, metadata)},
		{"mismatched non-null root date", response(wrongRootDate, metadata)},
		{"missing metadata date", response(base, `{}`)},
		{"mismatched metadata date", response(base, `{"calendarDate":"2026-08-07"}`)},
		{"missing nap marker", response(withoutNap, metadata)},
		{"nightly measurement with null root date", response(withDuration, metadata)},
		{"nightly score with null root date", response(withScore, metadata)},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, server.Client()).DailySleep(context.Background(), day); err == nil {
				t.Fatal("accepted invalid sleep date variant")
			}
		})
	}
}
