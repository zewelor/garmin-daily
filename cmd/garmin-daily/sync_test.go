package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zewelor/garmin-daily/garmin"
)

const healthFixture = `{"data":[{"calendarDate":"2026-10-25","movement":{"steps":{"value":12345,"distanceInMeters":9876.54}},"calories":{"burnedTotal":2456},"heartRate":{"restingValue":51},"bodyBattery":{"maxValue":95}}]}`
const sleepFixture = `{"calendarDate":"2026-10-25","sleepStartTimestampGMT":1792879200000,"sleepEndTimestampGMT":1792915200000,"sleepTimeSeconds":36000,"deepSleepSeconds":3600,"avgHeartRate":51.25,"sleepScores":{"overall":{"value":88}}}`
const hrvFixture = `{"hrvSummary":{"calendarDate":"2026-10-25","weeklyAvg":48,"lastNightAvg":52,"status":"BALANCED","baseline":{"balancedLow":40,"balancedUpper":65}}}`

func fixtureServer(t *testing.T, health, sleep, hrv string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/personalapitoken-service/personal-api/personal-api-key/token" {
			if r.Method != "POST" || r.Header.Get("Authorization") != "ApiKey test-key" || r.Header.Get("Accept") != "*/*" {
				t.Error("incorrect token request")
			}
			fmt.Fprint(w, "test.bearer.token")
			return
		}
		if r.Header.Get("Authorization") != "Bearer test.bearer.token" {
			t.Error("incorrect bearer header")
		}
		switch r.URL.Path {
		case "/usersummary-service/v2/dailysummaries":
			if r.URL.Query().Get("startDate") != "2026-10-25" || r.URL.Query().Get("endDate") != "2026-10-25" {
				t.Error("incorrect range")
			}
			fmt.Fprint(w, health)
		case "/sleep-service/sleep/dailySleep":
			if sleep == "" {
				w.WriteHeader(204)
				return
			}
			fmt.Fprint(w, sleep)
		case "/hrv-service/hrv/2026-10-25":
			if hrv == "" {
				w.WriteHeader(204)
				return
			}
			fmt.Fprint(w, hrv)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
}

func TestFetchDay(t *testing.T) {
	s := fixtureServer(t, healthFixture, sleepFixture, hrvFixture)
	defer s.Close()
	c := garmin.NewClient(s.URL, s.Client())
	ctx := context.Background()
	if err := c.Authenticate(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	d, err := fetchDay(ctx, c, "2026-10-25")
	if err != nil {
		t.Fatal(err)
	}
	distance := d.Health.Movement.Steps.Distance.String()
	if *d.Health.Movement.Steps.Value != 12345 || distance != "9876.54" {
		t.Fatal("lost movement values")
	}
	if d.Health.HeartRate.Max != nil {
		t.Fatal("missing value became zero")
	}
	if d.Sleep.Start.UnixMilli() != 1792879200000 || d.Sleep.End.Sub(d.Sleep.Start.Time) != 10*time.Hour {
		t.Fatal("sleep timestamps or DST duration changed")
	}
	if *d.HRV.Night != 52 {
		t.Fatal("lost HRV")
	}
}

func TestAbsentMeasurements(t *testing.T) {
	for _, response := range []string{"", `{"calendarDate":"2026-10-25","sleepTimeSeconds":null,"sleepStartTimestampGMT":null,"sleepEndTimestampGMT":null}`} {
		s := fixtureServer(t, healthFixture, response, "")
		c := garmin.NewClient(s.URL, s.Client())
		if err := c.Authenticate(context.Background(), "test-key"); err != nil {
			t.Fatal(err)
		}
		d, err := fetchDay(context.Background(), c, "2026-10-25")
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if d.Sleep.Duration != nil || d.Sleep.Start != nil || d.HRV.Night != nil {
			t.Fatal("missing measurements became zero")
		}
	}
}

func TestRejectInvalidResponse(t *testing.T) {
	for _, tc := range []struct{ name, health, sleep, hrv string }{
		{"wrong day", strings.ReplaceAll(healthFixture, "2026-10-25", "2026-10-24"), sleepFixture, hrvFixture},
		{"missing summary", `{"data":[]}`, sleepFixture, hrvFixture},
		{"duplicate summary", `{"data":[{"calendarDate":"2026-10-25"},{"calendarDate":"2026-10-25"}]}`, sleepFixture, hrvFixture},
		{"missing sleep day", healthFixture, `{"sleepTimeSeconds":12}`, hrvFixture},
		{"bad JSON", healthFixture, `{`, hrvFixture},
		{"negative steps", strings.ReplaceAll(healthFixture, "12345", "-1"), sleepFixture, hrvFixture},
		{"invalid BB", strings.ReplaceAll(healthFixture, "95", "101"), sleepFixture, hrvFixture},
		{"reversed sleep", healthFixture, strings.ReplaceAll(sleepFixture, "1792915200000", "1792800000000"), hrvFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureServer(t, tc.health, tc.sleep, tc.hrv)
			defer s.Close()
			c := garmin.NewClient(s.URL, s.Client())
			if err := c.Authenticate(context.Background(), "test-key"); err != nil {
				t.Fatal(err)
			}
			if _, err := fetchDay(context.Background(), c, "2026-10-25"); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}
