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
			fmt.Fprint(w, `{"data":[{"calendarDate":"2026-10-25","movement":{"steps":{"value":0,"distanceInMeters":1234.567890123456789}},"calories":{"burnedTotal":0,"burnedActive":null,"burnedResting":2100},"heartRate":{"restingValue":0,"minValue":null,"maxValue":180},"intensityMinutes":{"moderate":0,"vigorous":15},"stress":{"avgLevel":0,"maxLevel":70,"totalDurationInMillis":{"unexpected":true},"restDurationInMillis":0,"activityDurationInMillis":1000,"uncategorizedDurationInMillis":null,"lowStressDurationInMillis":2000,"mediumStressDurationInMillis":3000,"highStressDurationInMillis":4000,"qualifier":[]},"respiration":{"avgValue":0,"minValue":null,"maxValue":20,"latestValue":0},"pulseOx":{"avgValue":[]},"bodyBattery":{"minValue":0,"maxValue":100,"chargedValue":0,"drainedValue":null,"latestValue":0}}]}`)
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
	if summary.Movement.Steps.Value == nil || *summary.Movement.Steps.Value != 0 || summary.Movement.Steps.Distance.String() != "1234.567890123456789" {
		t.Fatal("summary movement zero or decimal precision lost")
	}
	if summary.Calories.Total == nil || *summary.Calories.Total != 0 || summary.Calories.Active != nil || summary.Calories.Resting == nil || *summary.Calories.Resting != 2100 {
		t.Fatal("summary calories zero or null changed")
	}
	if summary.HeartRate.Resting == nil || *summary.HeartRate.Resting != 0 || summary.HeartRate.Min != nil || summary.HeartRate.Max == nil || *summary.HeartRate.Max != 180 {
		t.Fatal("summary heart rate zero or null changed")
	}
	if summary.Stress.RestMillis == nil || *summary.Stress.RestMillis != 0 || summary.Stress.UncategorizedMillis != nil || summary.Stress.HighMillis == nil || *summary.Stress.HighMillis != 4000 {
		t.Fatal("summary stress duration zero or null changed")
	}
	if summary.Respiration.Average == nil || *summary.Respiration.Average != 0 || summary.Respiration.Min != nil || summary.Respiration.Latest == nil || *summary.Respiration.Latest != 0 {
		t.Fatal("summary respiration zero or null changed")
	}
	if summary.Battery.Min == nil || *summary.Battery.Min != 0 || summary.Battery.Drained != nil || summary.Battery.Latest == nil || *summary.Battery.Latest != 0 {
		t.Fatal("summary body battery zero or null changed")
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
