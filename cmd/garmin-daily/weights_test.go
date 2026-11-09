package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zewelor/garmin-daily/garmin"
)

func TestWeightPreviewHandlesDuplicateSourceMeasurements(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"matching persisted fields", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":900},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500.0,"weightDelta":0}]`, `108.5`, false},
		{"zero then nonzero", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500}]`, `108.5`, false},
		{"nonzero then zero", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0}]`, `108.5`, false},
		{"single zero", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0}]`, `0.000`, false},
		{"two zeros", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0}]`, `0.000`, false},
		{"different weights", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":109500}]`, "", true},
		{"zero cannot hide conflict", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":109500}]`, "", true},
		{"precision matters", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500.00000000001},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500.00000000002}]`, "", true},
		{"different dates", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500},{"calendarDate":"2026-11-08","timestampGMT":1794213973000,"weight":108500}]`, "", true},
		{"missing weight", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500},{"calendarDate":"2026-11-09","timestampGMT":1794213973000}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := tc.body
				if r.Method == http.MethodPost {
					body = "test-token"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			client := garmin.NewClient("https://garmin.example", httpClient)
			if err := client.Authenticate(context.Background(), "test-key"); err != nil {
				t.Fatal(err)
			}
			from, _ := time.Parse(time.DateOnly, "2026-11-08")
			to, _ := time.Parse(time.DateOnly, "2026-11-09")
			var out bytes.Buffer
			err := syncWeightsRange(context.Background(), client, nil, from, to, true, &out)
			if tc.wantErr {
				if err == nil || out.Len() != 0 {
					t.Fatalf("conflict: output=%s err=%v", out.String(), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rows := decodePreview(t, out.String())
			if len(rows) != 1 || rows[0].Table != "weight_measurements" || string(rows[0].Values["weight_kg"]) != tc.want {
				t.Fatalf("unexpected preview: %s", strings.TrimSpace(out.String()))
			}
		})
	}
}

func TestWeightDeltaPreview(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		wantErr          bool
	}{
		{"observed duplicate", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":900.0000000000057},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":0}]`, `900.0000000000057`, false},
		{"negative value after zero", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0,"weightDelta":0},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":-1200.125}]`, `-1200.125`, false},
		{"independent fields", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":0},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":0,"weightDelta":-1200}]`, `-1200`, false},
		{"null and value", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":null},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":-12}]`, `-12`, false},
		{"value and null", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":-12},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":null}]`, `-12`, false},
		{"null and zero", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":null},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":0}]`, `0`, false},
		{"equal signed deltas", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":-12},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":-12.0}]`, `-12`, false},
		{"absent delta", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500}]`, `null`, false},
		{"conflicting nonzero delta", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":900},{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":-900}]`, "", true},
		{"invalid delta", `[{"calendarDate":"2026-11-09","timestampGMT":1794213973000,"weight":108500,"weightDelta":"bad"}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := tc.body
				if r.Method == http.MethodPost {
					body = "test-token"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			client := garmin.NewClient("https://garmin.example", httpClient)
			if err := client.Authenticate(context.Background(), "test-key"); err != nil {
				t.Fatal(err)
			}
			day, _ := time.Parse(time.DateOnly, "2026-11-09")
			var out bytes.Buffer
			err := syncWeightsRange(context.Background(), client, nil, day, day, true, &out)
			if tc.wantErr {
				if err == nil || out.Len() != 0 {
					t.Fatalf("expected error with no output: out=%s err=%v", out.String(), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rows := decodePreview(t, out.String())
			if len(rows) != 1 || string(rows[0].Values["weight_delta"]) != tc.want || string(rows[0].Values["weight_kg"]) != "108.5" {
				t.Fatalf("unexpected preview: %s", out.String())
			}
		})
	}
}
