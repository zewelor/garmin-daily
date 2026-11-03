package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type previewRow struct {
	Table  string                     `json:"table"`
	Values map[string]json.RawMessage `json:"values"`
	Delete map[string]json.RawMessage `json:"delete"`
}

func installPreviewHTTP(t *testing.T) *[]string {
	t.Helper()
	var requests []string
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path)
		body, status := "", 200
		switch r.URL.Path {
		case "/personalapitoken-service/personal-api/personal-api-key/token":
			body = "synthetic-token"
		case "/usersummary-service/v2/dailysummaries":
			body = `{"data":[{"calendarDate":"2026-11-02","calories":{"burnedActive":500},"intensityMinutes":{"moderate":30,"vigorous":15},"respiration":{"avgValue":15},"pulseOx":{"avgValue":"ignored"},"stress":{"highStressDurationInMillis":60000}}]}`
		case "/sleep-service/sleep/dailySleep", "/hrv-service/hrv/2026-11-02":
			status = 204
		case "/weight-service/weight/all/2026-11-02/2026-11-02":
			body = `[{"calendarDate":"2026-11-02","timestampGMT":1793613600000,"weight":82345.67890123456789,"bmi":"ignored"},{"calendarDate":"2026-11-02","timestampGMT":1793617200000,"weight":82400}]`
		case "/activitylist-service/activities/search/activities":
			if r.URL.Query().Get("start") == "3" {
				body = `[]`
				break
			}
			body = `[
    {"activityId":123,"activityType":{"typeKey":"strength_training"},"startTimeLocal":"2026-11-02 12:00:00","startTimeGMT":"2026-11-02 12:00:00","hrTimeInZone_2":1200.125,"summarizedExerciseSets":[{"sets":2,"reps":10,"maxWeight":12000}]},
    {"activityId":124,"parentId":123,"activityType":{"typeKey":"running"},"startTimeLocal":"2026-11-02 12:00:00","startTimeGMT":"2026-11-02 12:00:00"},
    {"activityId":125,"activityType":{"typeKey":"running"},"startTimeLocal":"2026-11-02 13:00:00","startTimeGMT":"2026-11-02 13:00:00"}
   ]`
		case "/biometric-service/heartRateZones/v2":
			status = 403
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			status = 404
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	return &requests
}

func decodePreview(t *testing.T, data string) []previewRow {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	var rows []previewRow
	for {
		var r previewRow
		err := decoder.Decode(&r)
		if errors.Is(err, io.EOF) {
			return rows
		}
		if err != nil {
			t.Fatalf("invalid preview JSON: %v", err)
		}
		rows = append(rows, r)
	}
}

func TestDryRunPrintsIncomingDatabaseRows(t *testing.T) {
	requests := installPreviewHTTP(t)
	var out bytes.Buffer
	err := run(context.Background(), []string{"sync", "--dry-run", "--from", "2026-11-02", "--to", "2026-11-02"}, func(name string) string {
		if name == "GARMIN_API_KEY" {
			return "synthetic-key"
		}
		t.Fatalf("dry run read database configuration %s", name)
		return ""
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	rows := decodePreview(t, out.String())
	counts := map[string]int{}
	prune := map[string]string{}
	var weights []string
	for _, r := range rows {
		if r.Delete != nil {
			prune[string(r.Delete["activity_id"])] = string(r.Delete["exercise_index_gte"])
			continue
		}
		counts[r.Table]++
		switch r.Table {
		case "daily_health":
			if string(r.Values["day"]) != `"2026-11-02"` || string(r.Values["calories_active"]) != "500" || string(r.Values["vigorous_minutes"]) != "15" || string(r.Values["respiration_avg"]) != "15" || string(r.Values["stress_high_ms"]) != "60000" {
				t.Fatalf("wrong database values: %s", out.String())
			}
		case "activities":
			if string(r.Values["activity_id"]) == "124" {
				t.Fatal("child session included in write preview")
			}
		case "weight_measurements":
			weights = append(weights, string(r.Values["weight_kg"]))
		}
	}
	if len(rows) != 10 || counts["daily_health"] != 1 || counts["daily_sleep"] != 1 || counts["daily_hrv"] != 1 || counts["activities"] != 2 || counts["activity_exercises"] != 1 || counts["weight_measurements"] != 2 {
		t.Fatalf("unexpected row counts: %+v, total=%d", counts, len(rows))
	}
	if len(weights) != 2 || weights[0] != "82.34567890123456789" || weights[1] != "82.4" {
		t.Fatalf("wrong kg values: %v", weights)
	}
	if prune["123"] != "1" || prune["125"] != "0" {
		t.Fatalf("missing exercise snapshot pruning: %v", prune)
	}
	for _, path := range *requests {
		if strings.Contains(path, "heartRateZones") {
			t.Fatal("dry run fetched unused zone configuration")
		}
	}
	for _, unused := range []string{"bmi", "pulseOx", "synthetic-key", "synthetic-token"} {
		if strings.Contains(out.String(), unused) {
			t.Fatalf("preview contains unused field or credential %q", unused)
		}
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestDryRunOutputErrorRemainsAnOutputError(t *testing.T) {
	installPreviewHTTP(t)
	failure := errors.New("disk write failed")
	err := run(context.Background(), []string{"sync", "--dry-run", "--from", "2026-11-02", "--to", "2026-11-02"}, func(string) string { return "synthetic-key" }, failingWriter{failure})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "write dry-run output") || strings.Contains(err.Error(), "network") {
		t.Fatalf("output error lost or misclassified: %v", err)
	}
}

func TestActivityOnlyDryRunHasTheSameScope(t *testing.T) {
	requests := installPreviewHTTP(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"sync-activities", "--dry-run", "--from", "2026-11-02", "--to", "2026-11-02"}, func(name string) string {
		if name != "GARMIN_API_KEY" {
			t.Fatalf("unexpected configuration %s", name)
		}
		return "synthetic-key"
	}, &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range decodePreview(t, out.String()) {
		if r.Table != "activities" && r.Table != "activity_exercises" {
			t.Fatalf("unexpected table %s", r.Table)
		}
	}
	for _, path := range *requests {
		if strings.Contains(path, "weight/") || strings.Contains(path, "dailysummaries") {
			t.Fatalf("unexpected request %s", path)
		}
	}
}

func TestDryRunFlags(t *testing.T) {
	for _, args := range [][]string{
		{"fetch"}, {"migrate-activities"}, {"migrate-monitoring"},
		{"sync", "--dry-run", "--from", "2026-11-02", "--to", "2026-11-02", "--output", "unused"},
	} {
		if err := run(context.Background(), args, func(string) string { return "" }, io.Discard); err == nil {
			t.Fatalf("obsolete command or option accepted: %v", args)
		}
	}
}
