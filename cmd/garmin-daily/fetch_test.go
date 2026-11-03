package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchCLIHasNoDatabaseRequirement(t *testing.T) {
	err := run(context.Background(), []string{"fetch", "--from", "2026-11-02", "--to", "2026-11-02", "--output", filepath.Join(t.TempDir(), "download")}, func(key string) string {
		if key == "DATABASE_URL" {
			t.Fatal("fetch accessed database configuration")
		}
		return ""
	}, io.Discard)
	if err == nil || err.Error() != "GARMIN_API_KEY is required" {
		t.Fatalf("unexpected fetch error: %v", err)
	}
}

// Simulates Garmin's real numeric timestamps, missing optional metrics, two
// weigh-ins on one day, and a raw field outside our selected monitoring scope.
func TestFetchArtifactsEndToEnd(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "personal-api-key/token") {
			if r.Header.Get("Authorization") != "ApiKey test-secret" {
				t.Error("missing API key")
			}
			fmt.Fprint(w, "test-bearer-secret")
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-bearer-secret" {
			t.Error("missing bearer token")
		}
		switch r.URL.Path {
		case "/usersummary-service/v2/dailysummaries":
			fmt.Fprint(w, `{"data":[{"calendarDate":"2026-11-02","calories":{"burnedActive":500},"intensityMinutes":{"moderate":30,"vigorous":15},"respiration":{"avgValue":15,"minValue":10,"maxValue":18,"latestValue":16},"pulseOx":{"minValue":94,"latestValue":97},"stress":{"highStressDurationInMillis":60000},"unmodeled":{"keptInRaw":true}}]}`)
		case "/sleep-service/sleep/dailySleep", "/hrv-service/hrv/2026-11-02":
			w.WriteHeader(204)
		case "/weight-service/weight/all/2026-11-02/2026-11-02":
			fmt.Fprint(w, `[{"calendarDate":"2026-11-02","date":1793613600000,"timestampGMT":1793613600000,"weight":82345.67890123456789,"weightDelta":-500.125},{"calendarDate":"2026-11-02","timestampGMT":1793617200000,"weight":82400}]`)
		case "/biometric-service/heartRateZones/v2":
			fmt.Fprint(w, `[]`)
		case "/activitylist-service/activities/search/activities":
			if r.URL.Query().Get("start") == "1" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, `[{"activityId":123,"activityType":{"typeKey":"running"},"startTimeLocal":"2026-11-02 12:00:00","startTimeGMT":"2026-11-02 12:00:00","duration":1234.56789,"hrTimeInZone_2":1200.125,"activityTrainingLoad":40,"vO2MaxValue":null}]`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	// Keep the production host in the application; reroute only this test's HTTP traffic.
	h := s.Client()
	base := h.Transport
	h.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(s.URL, "http://")
		return base.RoundTrip(r)
	})
	dir := filepath.Join(t.TempDir(), "download")
	day, _ := time.Parse(time.DateOnly, "2026-11-02")
	var out bytes.Buffer
	if err := fetchFiles(context.Background(), h, "test-secret", day, day, dir, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "days=1 weights=2 activities=1 zone_configs=0") {
		t.Fatalf("unexpected summary: %s", &out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("82345.67890123456789")) || !bytes.Contains(data, []byte("-500.125")) {
		t.Fatal("weight decimal precision or signed delta lost")
	}
	var got fetchData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Weights) != 2 || len(got.Activities) != 1 || got.Days[0].Health.Respiration.Average == nil || got.Days[0].Health.PulseOx.Average != nil || got.Days[0].Sleep.Duration != nil {
		t.Fatal("download lost measurements or fabricated absent data")
	}
	health := got.Days[0].Health
	if *health.Calories.Active != 500 || *health.Intensity.Moderate != 30 || *health.Intensity.Vigorous != 15 || *health.PulseOx.Min != 94 || *health.PulseOx.Latest != 97 || *health.Stress.HighMillis != 60000 {
		t.Fatal("active kcal, raw intensity minutes, SpO2 or stress duration changed")
	}
	if bytes.Contains(data, []byte("keptInRaw")) {
		t.Fatal("selected export unexpectedly contains an unmodeled field")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 9 {
		t.Fatalf("files=%d, want 7 raw responses + data + manifest", len(files))
	}
	foundRaw := false
	for _, f := range files {
		info, _ := f.Info()
		if info.Mode().Perm() != 0600 {
			t.Errorf("unsafe file mode %s: %o", f.Name(), info.Mode().Perm())
		}
		content, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte("test-secret")) || bytes.Contains(content, []byte("test-bearer-secret")) || bytes.Contains(content, []byte("Authorization")) {
			t.Fatal("credentials written to artifact")
		}
		if bytes.Contains(content, []byte("keptInRaw")) {
			foundRaw = true
		}
	}
	if !foundRaw {
		t.Fatal("original response was not preserved")
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("unsafe output directory permissions")
	}
	if err := fetchFiles(context.Background(), h, "test-secret", day, day, dir, io.Discard); err == nil {
		t.Fatal("overwrote existing output directory")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchFailuresLeaveNoSuccessManifest(t *testing.T) {
	for _, tc := range []struct {
		name, summary string
		sleepStatus   int
	}{
		{"invalid oxygen", `{"pulseOx":{"avgValue":101}}`, 204},
		{"negative respiration", `{"respiration":{"minValue":-1}}`, 204},
		{"negative stress duration", `{"stress":{"highStressDurationInMillis":-1}}`, 204},
		{"authentication failure midway", `{}`, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, status := "", 204
				switch r.URL.Path {
				case "/personalapitoken-service/personal-api/personal-api-key/token":
					body, status = "test-bearer-secret", 200
				case "/usersummary-service/v2/dailysummaries":
					body, status = `{"data":[{"calendarDate":"2026-11-02",`+strings.TrimPrefix(tc.summary, "{")+`]}`, 200
					if tc.summary == "{}" {
						body = `{"data":[{"calendarDate":"2026-11-02"}]}`
					}
				case "/sleep-service/sleep/dailySleep":
					status = tc.sleepStatus
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			dir := filepath.Join(t.TempDir(), "download")
			day, _ := time.Parse(time.DateOnly, "2026-11-02")
			want := "monitoring measurement outside supported range"
			if tc.sleepStatus == 401 {
				want = "sleep: Garmin HTTP 401"
			}
			if err := fetchFiles(context.Background(), h, "test-secret", day, day, dir, io.Discard); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("fetch failed for the wrong reason: got %v, want %q", err, want)
			}
			for _, name := range []string{"data.json", "manifest.json"} {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("failed fetch produced %s", name)
				}
			}
			raw, err := filepath.Glob(filepath.Join(dir, "response-*.json"))
			if err != nil || len(raw) == 0 {
				t.Fatal("failure discarded raw diagnostic evidence")
			}
		})
	}
}
