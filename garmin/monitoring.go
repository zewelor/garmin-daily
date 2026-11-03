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

// WeightMeasurement preserves source values. The live API returns weight in
// grams and timestamps in epoch milliseconds, despite the Swagger kg/string types.
// Composition units have not yet been verified against a populated live response.
type WeightMeasurement struct {
	Day          string       `json:"calendarDate"`
	Date         *int64       `json:"date"` // Local wall-clock milliseconds, not a UTC instant.
	Timestamp    *EpochMillis `json:"timestampGMT"`
	Weight       *json.Number `json:"weight"`
	Delta        *json.Number `json:"weightDelta"`
	BMI          *json.Number `json:"bmi"`
	BodyFat      *json.Number `json:"bodyFat"`
	BodyWater    *json.Number `json:"bodyWater"`
	BoneMass     *json.Number `json:"boneMass"`
	MuscleMass   *json.Number `json:"muscleMass"`
	Physique     *int32       `json:"physiqueRating"`
	VisceralFat  *json.Number `json:"visceralFat"`
	MetabolicAge *json.Number `json:"metabolicAge"`
}

// HeartRateZoneConfig describes current configuration, not historical time in zones.
type HeartRateZoneConfig struct {
	Sport             string `json:"sport"`
	Method            string `json:"trainingMethod"`
	RestingHeartRate  *int32 `json:"restingHeartRateUsed"`
	LactateThreshold  *int32 `json:"lactateThresholdHeartRateUsed"`
	MaxHeartRate      *int32 `json:"maxHeartRateUsed"`
	RestingAutoUpdate *bool  `json:"restingHrAutoUpdateUsed"`
	Zone1Floor        *int32 `json:"zone1Floor"`
	Zone2Floor        *int32 `json:"zone2Floor"`
	Zone3Floor        *int32 `json:"zone3Floor"`
	Zone4Floor        *int32 `json:"zone4Floor"`
	Zone5Floor        *int32 `json:"zone5Floor"`
}

// Activity is a recorded session, including child sessions of multisport events.
// Zone times and durations are seconds; intensity minutes are not doubled.
// Optional metrics retain source nulls and exact decimal values.
type Activity struct {
	ID   int64  `json:"activityId"`
	Name string `json:"activityName"`
	Type struct {
		ID  *int64 `json:"typeId"`
		Key string `json:"typeKey"`
	} `json:"activityType"`
	StartLocal        string              `json:"startTimeLocal"`
	StartGMT          string              `json:"startTimeGMT"`
	Parent            *bool               `json:"isParent"`
	ParentID          *int64              `json:"parentId"`
	Duration          *json.Number        `json:"duration"`
	MovingDuration    *json.Number        `json:"movingDuration"`
	ElapsedDuration   *json.Number        `json:"elapsedDuration"`
	Distance          *json.Number        `json:"distance"`
	Calories          *json.Number        `json:"calories"`
	BMRCalories       *json.Number        `json:"bmrCalories"`
	HeartRateAverage  *json.Number        `json:"averageHR"`
	HeartRateMax      *json.Number        `json:"maxHR"`
	ModerateMinutes   *int32              `json:"moderateIntensityMinutes"`
	VigorousMinutes   *int32              `json:"vigorousIntensityMinutes"`
	TrainingLoad      *json.Number        `json:"activityTrainingLoad"`
	AerobicEffect     *json.Number        `json:"aerobicTrainingEffect"`
	AnaerobicEffect   *json.Number        `json:"anaerobicTrainingEffect"`
	EffectLabel       *string             `json:"trainingEffectLabel"`
	TrainingStress    *json.Number        `json:"trainingStressScore"`
	VO2Max            *json.Number        `json:"vO2MaxValue"`
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
		if !calendarDate(w.Day) || w.Day < from || w.Day > to || w.Timestamp == nil || w.Weight == nil ||
			!nonnegativeNumbers(w.Weight, w.BMI, w.BodyFat, w.BodyWater, w.BoneMass, w.MuscleMass, w.VisceralFat, w.MetabolicAge) {
			return nil, fmt.Errorf("invalid weight measurement or calendarDate")
		}
	}
	return result, nil
}

// HeartRateZones reads existing configuration without generating defaults.
func (c *Client) HeartRateZones(ctx context.Context) ([]HeartRateZoneConfig, error) {
	var result []HeartRateZoneConfig
	if _, err := c.get(ctx, "/biometric-service/heartRateZones/v2?generateDefaults=false", &result, false); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, z := range result {
		if z.Sport == "" || seen[z.Sport] {
			return nil, fmt.Errorf("missing or duplicate heart rate zone sport")
		}
		seen[z.Sport] = true
		var previous int32
		for _, floor := range []*int32{z.Zone1Floor, z.Zone2Floor, z.Zone3Floor, z.Zone4Floor, z.Zone5Floor} {
			if floor != nil {
				if *floor <= previous {
					return nil, fmt.Errorf("invalid heart rate zone boundaries")
				}
				previous = *floor
			}
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
			if !nonnegativeNumbers(a.Duration, a.MovingDuration, a.ElapsedDuration, a.Distance, a.Calories, a.BMRCalories,
				a.HeartRateAverage, a.HeartRateMax, a.TrainingLoad, a.AerobicEffect, a.AnaerobicEffect,
				a.TrainingStress, a.VO2Max, a.Zone1Seconds, a.Zone2Seconds, a.Zone3Seconds, a.Zone4Seconds, a.Zone5Seconds,
				a.AverageSpeed, a.MaxDepth) ||
				(a.ModerateMinutes != nil && *a.ModerateMinutes < 0) || (a.VigorousMinutes != nil && *a.VigorousMinutes < 0) ||
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
