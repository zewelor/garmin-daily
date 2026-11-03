package garmin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func activityJSON(id int, local, gmt string, extra string) string {
	return fmt.Sprintf(`{"activityId":%d,"activityName":"ride","startTimeLocal":%q,"startTimeGMT":%q,"activityType":{"typeKey":"cycling"}%s}`, id, local, gmt, extra)
}

func activityPage(count int, local, gmt string) string {
	items := make([]string, count)
	for i := range items {
		items[i] = activityJSON(i+1, local, gmt, "")
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestActivitiesPaginateAndPreserveSourceLocalDate(t *testing.T) {
	const from, to = "2026-03-29", "2026-03-29"
	var starts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		starts = append(starts, q.Get("start"))
		if q.Get("limit") != "99" || q.Get("startDate") != from || q.Get("endDate") != to || q.Get("sortBy") != "startLocal" || q.Get("sortOrder") != "desc" || q.Get("excludeChildren") != "true" {
			t.Errorf("unexpected activity query: %v", q)
		}
		if q.Get("start") == "0" {
			items := make([]string, 99)
			for i := range items {
				items[i] = activityJSON(i+1, "2026-03-29 02:30:00", "2026-03-29 01:30:00", "")
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
			return
		}
		if q.Get("start") == "99" {
			fmt.Fprint(w, "[", activityJSON(100, "2026-03-29 02:30:00", "2026-03-29 01:30:00", ""), "]")
			return
		}
		t.Errorf("unexpected offset %q", q.Get("start"))
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client := NewClient(server.URL, server.Client())
	client.token = "test-token"

	activities, err := client.Activities(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 100 || strings.Join(starts, ",") != "0,99" {
		t.Fatalf("activities=%d offsets=%v", len(activities), starts)
	}
	if activities[0].Day != from || activities[0].StartedAt.Format("2006-01-02 15:04:05 MST") != "2026-03-29 01:30:00 UTC" {
		t.Fatalf("local day=%q started_at=%s", activities[0].Day, activities[0].StartedAt)
	}
}

func TestActivitiesRejectInvalidPages(t *testing.T) {
	valid := activityJSON(1, "2026-10-26 00:15:00", "2026-10-25 23:15:00", "")
	for _, tc := range []struct {
		name   string
		first  string
		second string
		status int
	}{
		{"duplicate across pages", activityPage(99, "2026-10-26 00:15:00", "2026-10-25 23:15:00"), "[" + valid + "]", 200},
		{"missing id", `[{"startTimeLocal":"2026-10-26 00:15:00","startTimeGMT":"2026-10-25 23:15:00","activityType":{"typeKey":"cycling"}}]`, "", 200},
		{"missing timestamp", `[{"activityId":1,"startTimeLocal":"2026-10-26 00:15:00","activityType":{"typeKey":"cycling"}}]`, "", 200},
		{"missing sport", `[{"activityId":1,"startTimeLocal":"2026-10-26 00:15:00","startTimeGMT":"2026-10-25 23:15:00"}]`, "", 200},
		{"local date outside range", "[" + activityJSON(1, "2026-10-27 00:15:00", "2026-10-26 23:15:00", "") + "]", "", 200},
		{"null response", "null", "", 200},
		{"object response", `{"activities":[]}`, "", 200},
		{"second page error", activityPage(99, "2026-10-26 00:15:00", "2026-10-25 23:15:00"), "secret upstream response", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("start") == "0" {
					fmt.Fprint(w, tc.first)
					return
				}
				if tc.status != 200 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.second)
					return
				}
				fmt.Fprint(w, tc.second)
			}))
			defer server.Close()
			client := NewClient(server.URL, server.Client())
			client.token = "test-token"
			got, err := client.Activities(context.Background(), "2026-10-26", "2026-10-26")
			if err == nil || got != nil {
				t.Fatalf("activities=%v err=%v, want nil activities and error", got, err)
			}
			if strings.Contains(err.Error(), "secret upstream response") {
				t.Fatalf("response body leaked: %v", err)
			}
		})
	}
}

func TestActivitiesKeepOptionalZeroAndExactNumbers(t *testing.T) {
	body := `[{"activityId":17,"activityName":"strength","startTimeLocal":"2026-10-26 00:15:00","startTimeGMT":"2026-10-25 23:15:00","activityType":{"typeKey":"strength_training"},"duration":0,"distance":0,"averageSpeed":0,"calories":0,"averageHR":0,"maxHR":0,"aerobicTrainingEffect":0,"anaerobicTrainingEffect":null,"activityTrainingLoad":1.234567890123456789,"totalSets":0,"totalReps":0,"maxDepth":0,"differenceBodyBattery":-100,"summarizedExerciseSets":[{"category":"strength_training","subCategory":"bench_press","reps":0,"sets":0,"duration":0,"maxWeight":0,"volume":0}]}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer server.Close()
	client := NewClient(server.URL, server.Client())
	client.token = "token"
	activities, err := client.Activities(context.Background(), "2026-10-26", "2026-10-26")
	if err != nil {
		t.Fatal(err)
	}
	a := activities[0]
	if a.Day != "2026-10-26" || a.StartedAt != (time.Date(2026, 10, 25, 23, 15, 0, 0, time.UTC)) || a.BodyBatteryChange == nil || *a.BodyBatteryChange != -100 {
		t.Fatalf("date/timestamp/BB mapping: %+v", a)
	}
	if a.DurationS == nil || a.DurationS.String() != "0" || a.DistanceM == nil || a.DistanceM.String() != "0" || a.TrainingEffectAnaerobic != nil {
		t.Fatalf("missing or zero decimal values not preserved: %+v", a)
	}
	if a.TrainingLoad == nil || a.TrainingLoad.String() != "1.234567890123456789" || len(a.Exercises) != 1 || a.Exercises[0].VolumeG == nil || a.Exercises[0].VolumeG.String() != "0" {
		t.Fatalf("exact summary or exercise values not preserved: %+v", a)
	}
}

func TestActivitiesValidateRangeAndDates(t *testing.T) {
	for _, dates := range [][2]string{{"2026-02-30", "2026-03-01"}, {"2026-10-27", "2026-10-26"}} {
		client := NewClient("http://unused", http.DefaultClient)
		if _, err := client.Activities(context.Background(), dates[0], dates[1]); err == nil {
			t.Fatalf("accepted range %v", dates)
		}
	}
}

func TestActivitiesIncludeSourceLocalDateBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("startDate") != "2026-10-25" || r.URL.Query().Get("endDate") != "2026-10-27" {
			t.Errorf("unexpected range: %v", r.URL.Query())
		}
		fmt.Fprint(w, "[",
			activityJSON(1, "2026-10-25 00:00:00", "2026-10-24 23:00:00", ""), ",",
			activityJSON(2, "2026-10-27 23:59:59", "2026-10-27 22:59:59", ""), "]")
	}))
	defer server.Close()
	client := NewClient(server.URL, server.Client())
	client.token = "test-token"
	activities, err := client.Activities(context.Background(), "2026-10-25", "2026-10-27")
	if err != nil || len(activities) != 2 || activities[0].Day != "2026-10-25" || activities[1].Day != "2026-10-27" {
		t.Fatalf("activities=%+v err=%v", activities, err)
	}
}
