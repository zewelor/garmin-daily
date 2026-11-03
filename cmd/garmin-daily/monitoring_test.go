package main

import (
	"encoding/json"
	"testing"

	"github.com/zewelor/garmin-daily/garmin"
)

func TestDailyMonitoringRejectsInvalidValues(t *testing.T) {
	const day = "2026-11-02"
	negativeRate := int16(-1)
	negativeDuration := int64(-1)
	for _, tc := range []struct {
		name string
		set  func(*garmin.Summary)
	}{
		{"respiration", func(h *garmin.Summary) { h.Respiration.Average = &negativeRate }},
		{"stress duration", func(h *garmin.Summary) { h.Stress.HighMillis = &negativeDuration }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dayData{Health: garmin.Summary{Day: day}, Sleep: garmin.Sleep{Day: day}, HRV: garmin.HRV{Day: day}}
			tc.set(&d.Health)
			if err := d.validate(day); err == nil {
				t.Fatal("invalid monitoring measurement accepted for database write")
			}
		})
	}
}

func TestActivityRejectsNegativeZoneDuration(t *testing.T) {
	negative := json.Number("-0.125")
	a := garmin.Activity{ID: 1, StartLocal: "2026-11-02 12:00:00", StartGMT: "2026-11-02 12:00:00", Zone2Seconds: &negative}
	a.Type.Key = "running"
	if err := validateActivity(a, "2026-11-02", "2026-11-02"); err == nil {
		t.Fatal("negative time in zone accepted for database write")
	}
}
