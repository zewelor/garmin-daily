package main

import (
	"encoding/json"
	"testing"

	"github.com/zewelor/garmin-daily/garmin"
)

func TestValidateActivityValues(t *testing.T) {
	valid := garmin.Activity{ID: 1, StartLocal: "2026-10-26 00:15:00", StartGMT: "2026-10-25 23:15:00"}
	valid.Type.Key = "cycling"
	if err := validateActivity(valid, "2026-10-26", "2026-10-26"); err != nil {
		t.Fatal(err)
	}

	zero := json.Number("0")
	negative := json.Number("-0.01")
	valid.Distance = &zero
	if err := validateActivity(valid, "2026-10-26", "2026-10-26"); err != nil {
		t.Fatalf("zero or absent measurements rejected: %v", err)
	}
	valid.TrainingLoad = &negative
	if err := validateActivity(valid, "2026-10-26", "2026-10-26"); err == nil {
		t.Fatal("accepted negative training load")
	}
	valid.TrainingLoad = nil
	for _, change := range []int16{-101, 101} {
		valid.BodyBatteryChange = &change
		if err := validateActivity(valid, "2026-10-26", "2026-10-26"); err == nil {
			t.Fatalf("accepted Body Battery change %d", change)
		}
	}
	for _, change := range []int16{-100, 100} {
		valid.BodyBatteryChange = &change
		if err := validateActivity(valid, "2026-10-26", "2026-10-26"); err != nil {
			t.Fatalf("rejected valid Body Battery change %d: %v", change, err)
		}
	}
}
