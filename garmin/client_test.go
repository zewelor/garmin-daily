package garmin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
