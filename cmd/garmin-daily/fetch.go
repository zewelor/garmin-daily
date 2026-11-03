package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zewelor/garmin-daily/garmin"
)

type fetchData struct {
	From       string                       `json:"from"`
	To         string                       `json:"to"`
	Days       []dayData                    `json:"days"`
	Weights    []garmin.WeightMeasurement   `json:"weights"`
	Zones      []garmin.HeartRateZoneConfig `json:"heart_rate_zones"`
	Activities []garmin.Activity            `json:"activities"`
}

type capturedResponse struct {
	Path   string `json:"path"`
	Status int    `json:"status"`
	File   string `json:"file"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Requests are sequential. Never record authentication responses or headers.
type recordingTransport struct {
	base      http.RoundTripper
	directory string
	responses []capturedResponse
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || req.Method != http.MethodGet {
		return resp, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	resp.Body.Close()
	if err != nil || len(body) > 8*1024*1024 {
		return nil, fmt.Errorf("read fetch response failed or exceeds 8 MiB")
	}
	name := fmt.Sprintf("response-%03d.json", len(t.responses)+1)
	if err := writePrivate(filepath.Join(t.directory, name), body); err != nil {
		return nil, err
	}
	t.responses = append(t.responses, capturedResponse{
		Path: req.URL.RequestURI(), Status: resp.StatusCode, File: name,
		Bytes: len(body), SHA256: fmt.Sprintf("%x", sha256.Sum256(body)),
	})
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func writePrivate(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create fetch artifact failed")
	}
	_, writeErr := f.Write(body)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("write fetch artifact failed")
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fetch artifact failed")
	}
	return writePrivate(path, append(body, '\n'))
}

func validateMonitoringSummary(h garmin.Summary) error {
	if !nonnegative(h.Respiration.Average, h.Respiration.Min, h.Respiration.Max, h.Respiration.Latest) ||
		!percent(h.PulseOx.Average, h.PulseOx.Min, h.PulseOx.Latest) ||
		!nonnegative(h.Stress.TotalMillis, h.Stress.RestMillis, h.Stress.ActivityMillis, h.Stress.UncategorizedMillis,
			h.Stress.LowMillis, h.Stress.MediumMillis, h.Stress.HighMillis) {
		return fmt.Errorf("monitoring measurement outside supported range")
	}
	return nil
}

// fetchFiles deliberately has no database dependency. A manifest is written
// last, only after every request, validation and artifact write succeeds.
func fetchFiles(ctx context.Context, httpClient *http.Client, key string, from, to time.Time, directory string, out io.Writer) error {
	if directory == "" {
		return fmt.Errorf("--output is required for fetch")
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return fmt.Errorf("create output directory failed; use a new directory with an existing parent")
	}
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	recorder := &recordingTransport{base: base, directory: directory}
	h := *httpClient
	h.Transport = recorder
	c := garmin.NewClient(garmin.BaseURL, &h)
	if err := c.Authenticate(ctx, key); err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	d := fetchData{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Days: make([]dayData, 0)}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		date := day.Format(time.DateOnly)
		values, err := fetchDay(ctx, c, date)
		if err != nil {
			return fmt.Errorf("%s: %w", date, err)
		}
		if err := validateMonitoringSummary(values.Health); err != nil {
			return fmt.Errorf("%s: %w", date, err)
		}
		d.Days = append(d.Days, values)
		fmt.Fprintf(out, "fetched_day=%s\n", date)
	}
	var err error
	if d.Weights, err = c.WeightMeasurements(ctx, d.From, d.To); err != nil {
		return fmt.Errorf("weights: %w", err)
	}
	if d.Zones, err = c.HeartRateZones(ctx); err != nil {
		return fmt.Errorf("heart rate zones: %w", err)
	}
	if d.Activities, err = c.Activities(ctx, d.From, d.To); err != nil {
		return fmt.Errorf("activities: %w", err)
	}
	if err := writePrivateJSON(filepath.Join(directory, "data.json"), d); err != nil {
		return err
	}
	manifest := struct {
		From      string             `json:"from"`
		To        string             `json:"to"`
		FetchedAt time.Time          `json:"fetched_at"`
		Responses []capturedResponse `json:"responses"`
	}{d.From, d.To, time.Now().UTC(), recorder.responses}
	if err := writePrivateJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return err
	}
	fmt.Fprintf(out, "fetched days=%d weights=%d activities=%d zone_configs=%d output=%s\n", len(d.Days), len(d.Weights), len(d.Activities), len(d.Zones), directory)
	return nil
}
