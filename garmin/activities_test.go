package garmin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func activityJSON(id int, local, gmt, extra string) string {
	return fmt.Sprintf(`{"activityId":%d,"activityName":"ride","startTimeLocal":%q,"startTimeGMT":%q,"activityType":{"typeKey":"cycling"}%s}`, id, local, gmt, extra)
}

func activityPage(count int, local, gmt string) string {
	items := make([]string, count)
	for i := range items {
		items[i] = activityJSON(i+1, local, gmt, "")
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestActivitiesPaginateAndPreserveMonitoringWireModel(t *testing.T) {
	const from, to = "2026-03-29", "2026-03-29"
	var starts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		starts = append(starts, q.Get("start"))
		if q.Get("limit") != "100" || q.Get("startDate") != from || q.Get("endDate") != to || q.Get("sortOrder") != "asc" || q.Get("excludeChildren") != "false" {
			t.Errorf("monitoring activity query changed: %v", q)
		}
		switch q.Get("start") {
		case "0":
			items := make([]string, 100)
			for i := range items {
				extra := ""
				if i == 0 {
					extra = `,"duration":0,"distance":0,"averageSpeed":1e-10,"calories":0,"averageHR":0,"aerobicTrainingEffect":0,"activityTrainingLoad":1.234567890123456789,"totalSets":0,"totalReps":0,"maxDepth":0,"differenceBodyBattery":-100,"summarizedExerciseSets":[{"category":"strength_training","subCategory":"bench_press","reps":0,"sets":0,"duration":0,"maxWeight":0,"volume":0}]`
				}
				items[i] = activityJSON(i+1, "2026-03-29 02:30:00", "2026-03-29 01:30:00", extra)
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		case "100":
			fmt.Fprint(w, "[", activityJSON(101, "2026-03-29 02:30:00", "2026-03-29 01:30:00", ""), "]")
		case "101":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected offset %q", q.Get("start"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, server.Client())
	activities, err := client.Activities(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 101 || strings.Join(starts, ",") != "0,100,101" {
		t.Fatalf("activities=%d offsets=%v", len(activities), starts)
	}
	a := activities[0]
	if a.ID != 1 || a.Name != "ride" || a.StartLocal != "2026-03-29 02:30:00" || a.StartGMT != "2026-03-29 01:30:00" || a.Type.Key != "cycling" {
		t.Fatalf("monitoring wire fields changed: %+v", a)
	}
	if a.Duration == nil || a.Duration.String() != "0" || a.Distance == nil || a.Distance.String() != "0" || a.AverageSpeed == nil || a.AverageSpeed.String() != "1e-10" || a.TrainingLoad == nil || a.TrainingLoad.String() != "1.234567890123456789" {
		t.Fatalf("optional zero or exact activity values lost: %+v", a)
	}
	if a.BodyBatteryChange == nil || *a.BodyBatteryChange != -100 || a.TotalSets == nil || *a.TotalSets != 0 || len(a.Exercises) != 1 || a.Exercises[0].Volume == nil || a.Exercises[0].Volume.String() != "0" {
		t.Fatalf("extended activity fields lost: %+v", a)
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
		{"duplicate across pages", activityPage(100, "2026-10-26 00:15:00", "2026-10-25 23:15:00"), "[" + valid + "]", 200},
		{"missing ID", `[{"startTimeLocal":"2026-10-26 00:15:00","startTimeGMT":"2026-10-25 23:15:00","activityType":{"typeKey":"cycling"}}]`, "", 200},
		{"invalid ID", strings.Replace(valid, `"activityId":1`, `"activityId":1.5`, 1), "", 200},
		{"missing timestamp", `[{"activityId":1,"startTimeLocal":"2026-10-26 00:15:00","activityType":{"typeKey":"cycling"}}]`, "", 200},
		{"missing sport", `[{"activityId":1,"startTimeLocal":"2026-10-26 00:15:00","startTimeGMT":"2026-10-25 23:15:00"}]`, "", 200},
		{"local date outside range", "[" + activityJSON(1, "2026-10-27 00:15:00", "2026-10-26 23:15:00", "") + "]", "", 200},
		{"negative average speed", "[" + activityJSON(1, "2026-10-26 00:15:00", "2026-10-25 23:15:00", `,"averageSpeed":-1`) + "]", "", 200},
		{"invalid Body Battery change", "[" + activityJSON(1, "2026-10-26 00:15:00", "2026-10-25 23:15:00", `,"differenceBodyBattery":101`) + "]", "", 200},
		{"null response", "null", "", 200},
		{"object response", `{"activities":[]}`, "", 200},
		{"second page error", activityPage(100, "2026-10-26 00:15:00", "2026-10-25 23:15:00"), "secret upstream response", 503},
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

func TestActivitiesValidateRangeBeforeRequest(t *testing.T) {
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
		if r.URL.Query().Get("start") == "2" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, "[",
			activityJSON(1, "2026-10-25 00:00:00", "2026-10-24 23:00:00", ""), ",",
			activityJSON(2, "2026-10-27 23:59:59", "2026-10-27 22:59:59", ""), "]")
	}))
	defer server.Close()
	client := NewClient(server.URL, server.Client())
	activities, err := client.Activities(context.Background(), "2026-10-25", "2026-10-27")
	if err != nil || len(activities) != 2 || activities[0].StartLocal[:10] != "2026-10-25" || activities[1].StartLocal[:10] != "2026-10-27" {
		t.Fatalf("activities=%+v err=%v", activities, err)
	}
}
