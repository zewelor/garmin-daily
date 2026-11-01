package main

import (
	"encoding/json"
	"fmt"
	"time"

	"garmin-daily/garmin"
)

type dayData struct {
	Health garmin.Summary
	Sleep  garmin.Sleep
	HRV    garmin.HRV
}

func validDay(s string) bool {
	d, err := time.Parse(time.DateOnly, s)
	return err == nil && d.Year() >= 1 && d.Format(time.DateOnly) == s
}

func nonnegative[T ~int16 | ~int32](values ...*T) bool {
	for _, v := range values {
		if v != nil && *v < 0 {
			return false
		}
	}
	return true
}

func percent(values ...*int16) bool {
	for _, v := range values {
		if v != nil && (*v < 0 || *v > 100) {
			return false
		}
	}
	return true
}

func (d dayData) validate(day string) error {
	h, s, r := d.Health, d.Sleep, d.HRV
	if !validDay(day) || h.Day != day || s.Day != day || r.Day != day {
		return fmt.Errorf("missing or mismatched calendarDate")
	}
	if !nonnegative(h.Movement.Steps.Value, h.Calories.Total, h.Calories.Active, h.Calories.Resting,
		s.Duration, s.Nap, s.Deep, s.Light, s.REM, s.Awake, s.Unmeasurable, r.Weekly, r.Night, r.Baseline.Low, r.Baseline.High, r.Baseline.LowUpper) ||
		!nonnegative(h.HeartRate.Resting, h.HeartRate.Min, h.HeartRate.Max, h.Intensity.Moderate, h.Intensity.Vigorous, h.Battery.Charged, h.Battery.Drained) ||
		!percent(h.Stress.Average, h.Stress.Max, h.Battery.Min, h.Battery.Max, h.Battery.Latest, s.Scores.Overall.Value) {
		return fmt.Errorf("measurement outside supported range")
	}
	for _, v := range []*json.Number{h.Movement.Steps.Distance, s.HeartRate, s.Stress, s.Respiration, s.SpO2} {
		if _, err := (databaseNumber{v}).NumericValue(); err != nil {
			return err
		}
	}
	if s.Start != nil && s.End != nil && s.End.UnixMilli() < s.Start.UnixMilli() {
		return fmt.Errorf("sleep ends before it starts")
	}
	return nil
}
