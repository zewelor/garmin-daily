package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WeightMeasurement contains the source fields persisted by the application.
// Garmin returns weight in grams and timestamps in epoch milliseconds.
type WeightMeasurement struct {
	Day         string       `json:"calendarDate"`
	Timestamp   *EpochMillis `json:"timestampGMT"`
	Weight      *json.Number `json:"weight"`
	WeightDelta *json.Number `json:"weightDelta"`
}

// Activity is a recorded session, including child sessions of multisport events.
// Zone times and durations are seconds; intensity minutes are not doubled.
// Optional metrics retain source nulls and exact decimal values.
type Activity struct {
	ID   int64  `json:"activityId"`
	Name string `json:"activityName"`
	Type struct {
		Key string `json:"typeKey"`
	} `json:"activityType"`
	StartLocal        string              `json:"startTimeLocal"`
	StartGMT          string              `json:"startTimeGMT"`
	ParentID          *int64              `json:"parentId"`
	Duration          *json.Number        `json:"duration"`
	Distance          *json.Number        `json:"distance"`
	Calories          *json.Number        `json:"calories"`
	HeartRateAverage  *json.Number        `json:"averageHR"`
	HeartRateMax      *json.Number        `json:"maxHR"`
	TrainingLoad      *json.Number        `json:"activityTrainingLoad"`
	AerobicEffect     *json.Number        `json:"aerobicTrainingEffect"`
	AnaerobicEffect   *json.Number        `json:"anaerobicTrainingEffect"`
	Zone1Seconds      *json.Number        `json:"hrTimeInZone_1"`
	Zone2Seconds      *json.Number        `json:"hrTimeInZone_2"`
	Zone3Seconds      *json.Number        `json:"hrTimeInZone_3"`
	Zone4Seconds      *json.Number        `json:"hrTimeInZone_4"`
	Zone5Seconds      *json.Number        `json:"hrTimeInZone_5"`
	AverageSpeed      *json.Number        `json:"averageSpeed"`
	TotalSets         *int32              `json:"totalSets"`
	TotalReps         *int32              `json:"totalReps"`
	MaxDepth          *json.Number        `json:"maxDepth"`
	BodyBatteryChange *int16              `json:"differenceBodyBattery"`
	Exercises         []*ActivityExercise `json:"summarizedExerciseSets"`
}

// ActivityExercise is one source-positioned element of a workout summary.
type ActivityExercise struct {
	Category    *string      `json:"category"`
	Subcategory *string      `json:"subCategory"`
	Reps        *int32       `json:"reps"`
	Sets        *int32       `json:"sets"`
	Duration    *json.Number `json:"duration"`
	MaxWeight   *json.Number `json:"maxWeight"`
	Volume      *json.Number `json:"volume"`
}

func calendarDate(day string) bool {
	t, err := time.Parse(time.DateOnly, day)
	return err == nil && t.Year() >= 1 && t.Format(time.DateOnly) == day
}

func validRange(from, to string) bool {
	return calendarDate(from) && calendarDate(to) && from <= to
}

func nonnegativeNumbers(values ...*json.Number) bool {
	for _, value := range values {
		if value == nil {
			continue
		}
		n, ok := new(big.Rat).SetString(value.String())
		if !ok || n.Sign() < 0 {
			return false
		}
	}
	return true
}

// WeightMeasurements retrieves every measurement, including several on one day.
func (c *Client) WeightMeasurements(ctx context.Context, from, to string) ([]WeightMeasurement, error) {
	if !validRange(from, to) {
		return nil, fmt.Errorf("invalid weight date range")
	}
	var result []WeightMeasurement
	if _, err := c.get(ctx, "/weight-service/weight/all/"+from+"/"+to, &result, false); err != nil {
		return nil, err
	}
	for _, w := range result {
		if !calendarDate(w.Day) || w.Day < from || w.Day > to || w.Timestamp == nil || w.Weight == nil || !nonnegativeNumbers(w.Weight) {
			return nil, fmt.Errorf("invalid weight measurement or calendarDate")
		}
	}
	return result, nil
}

// Activities reads all pages. Repeated IDs fail instead of silently skipping
// records or looping when the API ignores the pagination offset.
func (c *Client) Activities(ctx context.Context, from, to string) ([]Activity, error) {
	if !validRange(from, to) {
		return nil, fmt.Errorf("invalid activity date range")
	}
	const pageSize = 100
	const maxPages = 1000
	result := make([]Activity, 0)
	seen := make(map[int64]struct{})
	for pageNumber, offset := 0, 0; pageNumber < maxPages; pageNumber++ {
		query := url.Values{"startDate": {from}, "endDate": {to}, "start": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(pageSize)}, "sortOrder": {"asc"}, "excludeChildren": {"false"}}
		var page []Activity
		if _, err := c.get(ctx, "/activitylist-service/activities/search/activities?"+query.Encode(), &page, false); err != nil {
			return nil, err
		}
		if len(page) > pageSize {
			return nil, fmt.Errorf("activity page exceeds requested limit")
		}
		for _, a := range page {
			local, localErr := time.Parse(time.DateTime, a.StartLocal)
			utc, utcErr := time.ParseInLocation(time.DateTime, a.StartGMT, time.UTC)
			day := local.Format(time.DateOnly)
			if a.ID <= 0 || localErr != nil || utcErr != nil || local.Format(time.DateTime) != a.StartLocal || utc.Format(time.DateTime) != a.StartGMT ||
				strings.TrimSpace(a.Type.Key) == "" || day < from || day > to || (a.ParentID != nil && *a.ParentID < 0) {
				return nil, fmt.Errorf("invalid, duplicate or out-of-range activity")
			}
			if _, ok := seen[a.ID]; ok {
				return nil, fmt.Errorf("duplicate activity ID while paginating")
			}
			if !nonnegativeNumbers(a.Duration, a.Distance, a.Calories,
				a.HeartRateAverage, a.HeartRateMax, a.TrainingLoad, a.AerobicEffect, a.AnaerobicEffect,
				a.Zone1Seconds, a.Zone2Seconds, a.Zone3Seconds, a.Zone4Seconds, a.Zone5Seconds,
				a.AverageSpeed, a.MaxDepth) ||
				(a.TotalSets != nil && *a.TotalSets < 0) || (a.TotalReps != nil && *a.TotalReps < 0) ||
				(a.BodyBatteryChange != nil && (*a.BodyBatteryChange < -100 || *a.BodyBatteryChange > 100)) {
				return nil, fmt.Errorf("invalid activity measurement")
			}
			for _, exercise := range a.Exercises {
				if exercise == nil ||
					(exercise.Sets != nil && *exercise.Sets < 0) || (exercise.Reps != nil && *exercise.Reps < 0) ||
					!nonnegativeNumbers(exercise.Duration, exercise.MaxWeight, exercise.Volume) {
					return nil, fmt.Errorf("invalid summarized exercise set")
				}
			}
			seen[a.ID] = struct{}{}
		}
		if len(page) == 0 {
			return result, nil
		}
		next := offset + len(page)
		if next <= offset {
			return nil, fmt.Errorf("activity pagination did not advance")
		}
		result = append(result, page...)
		offset = next
	}
	return nil, fmt.Errorf("activity pagination exceeded %d pages", maxPages)
}
