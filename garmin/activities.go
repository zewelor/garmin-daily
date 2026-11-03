package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	activitySearchPath = "/activitylist-service/activities/search/activities"
	activityPageSize   = 99
	maxActivityPages   = 1000
)

const activityTimestampLayout = "2006-01-02 15:04:05"

// Activity contains the summary fields persisted by GarminDaily. Day is derived
// from Garmin's source-local start time; StartedAt is parsed from its GMT value.
type Activity struct {
	ID                      int64
	Day                     string
	StartedAt               time.Time
	Name                    *string
	Sport                   string
	DurationS               *json.Number
	DistanceM               *json.Number
	AverageSpeedMPS         *json.Number
	Calories                *json.Number
	HeartRateAvg            *json.Number
	HeartRateMax            *json.Number
	TrainingEffectAerobic   *json.Number
	TrainingEffectAnaerobic *json.Number
	TrainingLoad            *json.Number
	Sets                    *int32
	Reps                    *int32
	DepthMaxCM              *json.Number
	BodyBatteryChange       *int16
	Exercises               []ActivityExercise
}

// ActivityExercise is one element of Garmin's summarizedExerciseSets array.
type ActivityExercise struct {
	Category    *string
	Subcategory *string
	Sets        *int32
	Reps        *int32
	DurationMS  *json.Number
	MaxWeightG  *json.Number
	VolumeG     *json.Number
}

type activityDTO struct {
	ActivityID              json.Number            `json:"activityId"`
	ActivityName            *string                `json:"activityName"`
	StartTimeLocal          string                 `json:"startTimeLocal"`
	StartTimeGMT            string                 `json:"startTimeGMT"`
	ActivityType            *activityTypeDTO       `json:"activityType"`
	Duration                *json.Number           `json:"duration"`
	Distance                *json.Number           `json:"distance"`
	AverageSpeed            *json.Number           `json:"averageSpeed"`
	Calories                *json.Number           `json:"calories"`
	AverageHR               *json.Number           `json:"averageHR"`
	MaxHR                   *json.Number           `json:"maxHR"`
	AerobicTrainingEffect   *json.Number           `json:"aerobicTrainingEffect"`
	AnaerobicTrainingEffect *json.Number           `json:"anaerobicTrainingEffect"`
	ActivityTrainingLoad    *json.Number           `json:"activityTrainingLoad"`
	TotalSets               *int32                 `json:"totalSets"`
	TotalReps               *int32                 `json:"totalReps"`
	MaxDepth                *json.Number           `json:"maxDepth"`
	DifferenceBodyBattery   *int16                 `json:"differenceBodyBattery"`
	Exercises               []*activityExerciseDTO `json:"summarizedExerciseSets"`
}

type activityTypeDTO struct {
	TypeKey *string `json:"typeKey"`
}

type activityExerciseDTO struct {
	Category    *string      `json:"category"`
	Subcategory *string      `json:"subCategory"`
	Reps        *int32       `json:"reps"`
	Sets        *int32       `json:"sets"`
	Duration    *json.Number `json:"duration"`
	MaxWeight   *json.Number `json:"maxWeight"`
	Volume      *json.Number `json:"volume"`
}

func validActivityDate(value string) bool {
	day, err := time.Parse(time.DateOnly, value)
	return err == nil && day.Format(time.DateOnly) == value
}

func decodeActivity(dto activityDTO, from, to string) (Activity, error) {
	var result Activity
	id, err := strconv.ParseInt(dto.ActivityID.String(), 10, 64)
	if err != nil || id <= 0 {
		return result, fmt.Errorf("missing or invalid activity ID")
	}
	localTime, err := time.Parse(activityTimestampLayout, dto.StartTimeLocal)
	if err != nil || localTime.Format(activityTimestampLayout) != dto.StartTimeLocal {
		return result, fmt.Errorf("missing or invalid local start time")
	}
	startedAt, err := time.ParseInLocation(activityTimestampLayout, dto.StartTimeGMT, time.UTC)
	if err != nil || startedAt.Format(activityTimestampLayout) != dto.StartTimeGMT {
		return result, fmt.Errorf("missing or invalid GMT start time")
	}
	day := localTime.Format(time.DateOnly)
	if day < from || day > to {
		return result, fmt.Errorf("activity local start date is outside requested range")
	}
	if dto.ActivityType == nil || dto.ActivityType.TypeKey == nil || strings.TrimSpace(*dto.ActivityType.TypeKey) == "" {
		return result, fmt.Errorf("missing activity sport")
	}
	result = Activity{
		ID:                      id,
		Day:                     day,
		StartedAt:               startedAt,
		Name:                    dto.ActivityName,
		Sport:                   *dto.ActivityType.TypeKey,
		DurationS:               dto.Duration,
		DistanceM:               dto.Distance,
		AverageSpeedMPS:         dto.AverageSpeed,
		Calories:                dto.Calories,
		HeartRateAvg:            dto.AverageHR,
		HeartRateMax:            dto.MaxHR,
		TrainingEffectAerobic:   dto.AerobicTrainingEffect,
		TrainingEffectAnaerobic: dto.AnaerobicTrainingEffect,
		TrainingLoad:            dto.ActivityTrainingLoad,
		Sets:                    dto.TotalSets,
		Reps:                    dto.TotalReps,
		DepthMaxCM:              dto.MaxDepth,
		BodyBatteryChange:       dto.DifferenceBodyBattery,
		Exercises:               make([]ActivityExercise, 0, len(dto.Exercises)),
	}
	for _, exercise := range dto.Exercises {
		if exercise == nil {
			return Activity{}, fmt.Errorf("invalid summarized exercise set")
		}
		result.Exercises = append(result.Exercises, ActivityExercise{
			Category:    exercise.Category,
			Subcategory: exercise.Subcategory,
			Sets:        exercise.Sets,
			Reps:        exercise.Reps,
			DurationMS:  exercise.Duration,
			MaxWeightG:  exercise.MaxWeight,
			VolumeG:     exercise.Volume,
		})
	}
	return result, nil
}

// Activities returns every activity whose source-local start date is within
// the inclusive range. All pages are fetched and validated before returning.
func (c *Client) Activities(ctx context.Context, from, to string) ([]Activity, error) {
	if !validActivityDate(from) || !validActivityDate(to) || from > to {
		return nil, fmt.Errorf("invalid Garmin activity date range")
	}
	activities := make([]Activity, 0)
	seen := make(map[int64]struct{})
	start := 0
	for pageNumber := 0; pageNumber < maxActivityPages; pageNumber++ {
		query := url.Values{
			"start":           {strconv.Itoa(start)},
			"limit":           {strconv.Itoa(activityPageSize)},
			"startDate":       {from},
			"endDate":         {to},
			"sortBy":          {"startLocal"},
			"sortOrder":       {"desc"},
			"excludeChildren": {"true"},
		}
		body, _, err := c.request(ctx, "GET", activitySearchPath+"?"+query.Encode(), "Bearer "+c.token)
		if err != nil {
			return nil, err
		}
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return nil, fmt.Errorf("invalid Garmin activity page")
		}
		var page []activityDTO
		if err := json.Unmarshal(trimmed, &page); err != nil || page == nil || len(page) > activityPageSize {
			return nil, fmt.Errorf("invalid Garmin activity page")
		}
		for _, dto := range page {
			activity, err := decodeActivity(dto, from, to)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[activity.ID]; ok {
				return nil, fmt.Errorf("duplicate activity ID while paginating")
			}
			seen[activity.ID] = struct{}{}
			activities = append(activities, activity)
		}
		if len(page) < activityPageSize {
			return activities, nil
		}
		next := start + len(page)
		if next <= start {
			return nil, fmt.Errorf("activity pagination did not advance")
		}
		start = next
	}
	return nil, fmt.Errorf("activity pagination exceeded %d pages", maxActivityPages)
}
