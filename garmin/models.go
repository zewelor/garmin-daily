// Package garmin provides a client for the Garmin Connect Personal API.
package garmin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Summary contains the daily measurements returned by Garmin.
type Summary struct {
	Day      string `json:"calendarDate"`
	Movement struct {
		Steps struct {
			Value    *int32       `json:"value"`
			Distance *json.Number `json:"distanceInMeters"`
		} `json:"steps"`
	} `json:"movement"`
	Calories struct {
		Total   *int32 `json:"burnedTotal"`
		Active  *int32 `json:"burnedActive"`
		Resting *int32 `json:"burnedResting"`
	} `json:"calories"`
	HeartRate struct {
		Resting *int16 `json:"restingValue"`
		Min     *int16 `json:"minValue"`
		Max     *int16 `json:"maxValue"`
	} `json:"heartRate"`
	Intensity struct {
		Moderate *int16 `json:"moderate"`
		Vigorous *int16 `json:"vigorous"`
	} `json:"intensityMinutes"`
	Stress struct {
		Average *int16 `json:"avgLevel"`
		Max     *int16 `json:"maxLevel"`
	} `json:"stress"`
	Battery struct {
		Min     *int16 `json:"minValue"`
		Max     *int16 `json:"maxValue"`
		Charged *int16 `json:"chargedValue"`
		Drained *int16 `json:"drainedValue"`
		Latest  *int16 `json:"latestValue"`
	} `json:"bodyBattery"`
}

// EpochMillis decodes Garmin's UTC epoch milliseconds into a Go time.
// The live API uses numbers despite the Swagger string type.
type EpochMillis struct{ time.Time }

func (e *EpochMillis) UnmarshalJSON(b []byte) error {
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || n < 0 || n > 253402300799999 {
		return fmt.Errorf("invalid epoch milliseconds")
	}
	e.Time = time.UnixMilli(n).UTC()
	return nil
}

// Sleep contains the daily sleep summary; nil fields indicate missing measurements.
type Sleep struct {
	Day          string       `json:"calendarDate"`
	Start        *EpochMillis `json:"sleepStartTimestampGMT"`
	End          *EpochMillis `json:"sleepEndTimestampGMT"`
	Duration     *int32       `json:"sleepTimeSeconds"`
	Nap          *int32       `json:"napTimeSeconds"`
	Deep         *int32       `json:"deepSleepSeconds"`
	Light        *int32       `json:"lightSleepSeconds"`
	REM          *int32       `json:"remSleepSeconds"`
	Awake        *int32       `json:"awakeSleepSeconds"`
	Unmeasurable *int32       `json:"unmeasurableSleepSeconds"`
	HeartRate    *json.Number `json:"avgHeartRate"`
	Stress       *json.Number `json:"avgSleepStress"`
	Respiration  *json.Number `json:"averageRespirationValue"`
	SpO2         *json.Number `json:"averageSpO2Value"`
	Scores       struct {
		Overall struct {
			Value *int16 `json:"value"`
		} `json:"overall"`
	} `json:"sleepScores"`
}

// HRV contains the daily HRV summary.
type HRV struct {
	Day      string  `json:"calendarDate"`
	Weekly   *int32  `json:"weeklyAvg"`
	Night    *int32  `json:"lastNightAvg"`
	Status   *string `json:"status"`
	Baseline struct {
		Low      *int32 `json:"balancedLow"`
		High     *int32 `json:"balancedUpper"`
		LowUpper *int32 `json:"lowUpper"`
	} `json:"baseline"`
}
