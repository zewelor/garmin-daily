package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMonitoringEndpoints(t *testing.T) {
	var activityPages int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/weight-service/weight/all/2026-10-28/2026-11-03":
			fmt.Fprint(w, `[{"calendarDate":"2026-11-02","date":{"unexpected":"ignored"},"timestampGMT":1793613600000,"weight":82345.67890123456789,"weightDelta":"bad","bodyFat":null},{"calendarDate":"2026-11-02","timestampGMT":1793617200000,"weight":0,"bmi":[],"bodyFat":{"unexpected":"ignored"},"bodyWater":false,"muscleMass":"bad","boneMass":-1,"metabolicAge":null}]`)
		case "/activitylist-service/activities/search/activities":
			q := r.URL.Query()
			if q.Get("startDate") != "2026-10-28" || q.Get("endDate") != "2026-11-03" || q.Get("excludeChildren") != "false" || q.Get("limit") != "100" || q.Get("sortOrder") != "asc" {
				t.Error("wrong activity range or pagination filters")
			}
			activityPages++
			if q.Get("start") == "101" {
				fmt.Fprint(w, `[]`)
				return
			}
			var rows []map[string]any
			first, last := 1, 100
			if q.Get("start") == "100" {
				first, last = 101, 101
			} else if q.Get("start") != "0" {
				t.Errorf("unexpected pagination offset %q", q.Get("start"))
			}
			for id := first; id <= last; id++ {
				row := map[string]any{"activityId": id, "startTimeLocal": "2026-11-02 12:00:00", "startTimeGMT": "2026-11-02 12:00:00", "activityType": map[string]any{"typeKey": "running", "typeId": map[string]any{"unexpected": "ignored"}}, "duration": json.Number("1800.123456789"), "calories": 250, "hrTimeInZone_2": json.Number("1200.125"), "aerobicTrainingEffect": 2.5, "activityTrainingLoad": 45, "vO2MaxValue": map[string]any{"unexpected": "ignored"}, "isParent": map[string]any{"unexpected": "ignored"}, "movingDuration": "bad", "trainingStressScore": -1}
				if id == 2 {
					row["anaerobicTrainingEffect"] = json.Number("0.5")
					row["parentId"] = 1
				}
				rows = append(rows, row)
			}
			json.NewEncoder(w).Encode(rows)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c := NewClient(s.URL, s.Client())
	ctx := context.Background()
	w, err := c.WeightMeasurements(ctx, "2026-10-28", "2026-11-03")
	if err != nil || len(w) != 2 {
		t.Fatalf("weights count=%d err=%v", len(w), err)
	}
	if w[0].Weight.String() != "82345.67890123456789" || w[0].Timestamp.UnixMilli() != 1793613600000 || w[0].Timestamp.Location() != time.UTC {
		t.Fatal("weight precision or timestamp changed")
	}
	if w[1].Weight == nil || w[1].Weight.String() != "0" || w[1].Timestamp.UnixMilli() != 1793617200000 {
		t.Fatal("second weight measurement changed")
	}
	a, err := c.Activities(ctx, "2026-10-28", "2026-11-03")
	if err != nil || len(a) != 101 || activityPages != 3 {
		t.Fatalf("activities=%d pages=%d err=%v", len(a), activityPages, err)
	}
	if a[100].ID != 101 || a[0].Duration.String() != "1800.123456789" || a[0].Zone2Seconds.String() != "1200.125" {
		t.Fatal("activity measurements or missing values changed")
	}
	if a[1].AnaerobicEffect.String() != "0.5" || a[1].ParentID == nil || *a[1].ParentID != 1 {
		t.Fatal("training effect or child-session link changed")
	}
}

func TestActivitiesFollowServerLimitedPages(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("start") {
		case "0":
			fmt.Fprint(w, `[{"activityId":1,"startTimeLocal":"2026-11-02 12:00:00","startTimeGMT":"2026-11-02 12:00:00","activityType":{"typeKey":"running"}}]`)
		case "1":
			fmt.Fprint(w, `[{"activityId":2,"startTimeLocal":"2026-11-02 13:00:00","startTimeGMT":"2026-11-02 13:00:00","activityType":{"typeKey":"running"}}]`)
		case "2":
			fmt.Fprint(w, `[]`)
		default:
			t.Error("skipped a server-limited page")
			w.WriteHeader(400)
		}
	}))
	defer s.Close()
	a, err := NewClient(s.URL, s.Client()).Activities(context.Background(), "2026-11-02", "2026-11-02")
	if err != nil || len(a) != 2 || calls != 3 {
		t.Fatalf("server-limited pagination lost records: count=%d calls=%d err=%v", len(a), calls, err)
	}
}

func TestMonitoringRejectsBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		fetch      func(*Client) error
	}{
		{"null weights", `null`, func(c *Client) error {
			_, e := c.WeightMeasurements(context.Background(), "2026-11-02", "2026-11-02")
			return e
		}},
		{"weight outside range", `[{"calendarDate":"2026-11-01","timestampGMT":1793613600000,"weight":82000}]`, func(c *Client) error {
			_, e := c.WeightMeasurements(context.Background(), "2026-11-02", "2026-11-02")
			return e
		}},
		{"missing weight", `[{"calendarDate":"2026-11-02","timestampGMT":1793613600000}]`, func(c *Client) error {
			_, e := c.WeightMeasurements(context.Background(), "2026-11-02", "2026-11-02")
			return e
		}},
		{"negative weight", `[{"calendarDate":"2026-11-02","timestampGMT":1793613600000,"weight":-1}]`, func(c *Client) error {
			_, e := c.WeightMeasurements(context.Background(), "2026-11-02", "2026-11-02")
			return e
		}},
		{"missing activity id", `[{"startTimeLocal":"2026-11-02 12:00:00"}]`, func(c *Client) error {
			_, e := c.Activities(context.Background(), "2026-11-02", "2026-11-02")
			return e
		}},
		{"negative time in zone", `[{"activityId":1,"startTimeLocal":"2026-11-02 12:00:00","startTimeGMT":"2026-11-02 12:00:00","activityType":{"typeKey":"running"},"hrTimeInZone_1":-1}]`, func(c *Client) error {
			_, e := c.Activities(context.Background(), "2026-11-02", "2026-11-02")
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.body) }))
			defer s.Close()
			if err := tc.fetch(NewClient(s.URL, s.Client())); err == nil || strings.Contains(err.Error(), tc.body) {
				t.Fatalf("bad response was accepted or exposed: %v", err)
			}
		})
	}
}

func TestActivityPaginationRejectsRepeatedPage(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, "[")
		for i := 1; i <= 100; i++ {
			if i > 1 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"activityId":%d,"startTimeLocal":"2026-11-02 12:00:00","startTimeGMT":"2026-11-02 12:00:00","activityType":{"typeKey":"running"}}`, i)
		}
		fmt.Fprint(w, "]")
	}))
	defer s.Close()
	if _, err := NewClient(s.URL, s.Client()).Activities(context.Background(), "2026-11-02", "2026-11-02"); err == nil || calls != 2 {
		t.Fatalf("repeated page: calls=%d err=%v", calls, err)
	}
}

func TestMonitoringEmptyAndInvalidRange(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; fmt.Fprint(w, `[]`) }))
	defer s.Close()
	c := NewClient(s.URL, s.Client())
	ctx := context.Background()
	if _, err := c.WeightMeasurements(ctx, "2026-11-03", "2026-11-02"); err == nil {
		t.Fatal("reversed weight range accepted")
	}
	if _, err := c.Activities(ctx, "2026-02-30", "2026-03-01"); err == nil {
		t.Fatal("invalid activity date accepted")
	}
	if calls != 0 {
		t.Fatal("invalid dates made HTTP requests")
	}
	if w, err := c.WeightMeasurements(ctx, "2026-11-02", "2026-11-02"); err != nil || len(w) != 0 {
		t.Fatalf("empty weights: %v", err)
	}
	if a, err := c.Activities(ctx, "2026-11-02", "2026-11-02"); err != nil || len(a) != 0 {
		t.Fatalf("empty activities: %v", err)
	}
}
