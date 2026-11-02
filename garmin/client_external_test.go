package garmin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zewelor/garmin-daily/garmin"
)

func TestClientEndpoints(t *testing.T) {
	const day = "2026-10-25"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/personalapitoken-service/personal-api/personal-api-key/token" {
			if r.Method != "POST" || r.Header.Get("Authorization") != "ApiKey test-key" || r.Header.Get("Accept") != "*/*" {
				t.Error("wrong token request")
			}
			fmt.Fprint(w, "test-token")
			return
		}
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" {
			t.Error("wrong data authorization")
		}
		switch r.URL.Path {
		case "/usersummary-service/v2/dailysummaries":
			if r.URL.Query().Get("startDate") != day || r.URL.Query().Get("endDate") != day {
				t.Error("wrong summary dates")
			}
			fmt.Fprint(w, `{"data":[{"calendarDate":"2026-10-25","movement":{"steps":{"distanceInMeters":1234.567890123456789}}}]}`)
		case "/sleep-service/sleep/dailySleep":
			if r.URL.Query().Get("date") != day {
				t.Error("wrong sleep date")
			}
			fmt.Fprint(w, `{"calendarDate":"2026-10-25","sleepStartTimestampGMT":1792879200000,"avgHeartRate":5.125e1}`)
		case "/hrv-service/hrv/2026-10-25":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := garmin.NewClient(server.URL, server.Client())
	ctx := context.Background()
	if err := client.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	summary, err := client.DailySummary(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Movement.Steps.Distance.String() != "1234.567890123456789" {
		t.Fatal("decimal precision lost")
	}
	sleep, err := client.DailySleep(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if sleep == nil || sleep.Start.UnixMilli() != 1792879200000 || *sleep.HeartRate != json.Number("5.125e1") {
		t.Fatal("sleep values changed")
	}
	hrv, err := client.DailyHRV(ctx, day)
	if err != nil || hrv != nil {
		t.Fatalf("no-data response: %v %v", hrv, err)
	}
}
