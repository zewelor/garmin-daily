package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BaseURL is the official Garmin Connect Personal API host.
const BaseURL = "https://personal.connectapi.garmin.com"
const (
	tokenPath   = "/personalapitoken-service/personal-api/personal-api-key/token"
	summaryPath = "/usersummary-service/v2/dailysummaries"
	sleepPath   = "/sleep-service/sleep/dailySleep"
	hrvPath     = "/hrv-service/hrv/"
)

// Client holds one in-memory bearer token. Authenticate before reading data.
// Use it sequentially; the caller owns HTTP timeouts and redirect policy.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

// NewClient uses the supplied API host and non-nil HTTP client.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, http: httpClient}
}

func (c *Client) request(ctx context.Context, method, path, auth string) ([]byte, int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("create Garmin request")
		}
		req.Header.Set("Authorization", auth)
		req.Header.Set("Accept", "application/json")
		if method == http.MethodPost {
			req.Header.Set("Accept", "*/*")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("Garmin request failed (network, timeout or cancellation)")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, fmt.Errorf("read Garmin response")
		}
		if len(body) > 8*1024*1024 {
			return nil, resp.StatusCode, fmt.Errorf("Garmin response exceeds 8 MiB")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, resp.StatusCode, nil
		}
		if attempt == 2 || (resp.StatusCode != 429 && resp.StatusCode < 500) {
			return nil, resp.StatusCode, fmt.Errorf("Garmin HTTP %d", resp.StatusCode)
		}
		delay := time.Duration(attempt+1) * time.Second
		if h := resp.Header.Get("Retry-After"); h != "" {
			if seconds, err := strconv.Atoi(h); err == nil && seconds >= 0 {
				if seconds > 30 {
					return nil, resp.StatusCode, fmt.Errorf("Garmin Retry-After exceeds 30s; rerun later")
				}
				delay = time.Duration(seconds) * time.Second
			} else if when, err := http.ParseTime(h); err == nil {
				delay = time.Until(when)
				if delay < 0 {
					delay = 0
				}
			} else {
				return nil, resp.StatusCode, fmt.Errorf("invalid Garmin Retry-After")
			}
		}
		if delay > 30*time.Second {
			return nil, resp.StatusCode, fmt.Errorf("Garmin HTTP %d: Retry-After exceeds 30s; rerun later", resp.StatusCode)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, 0, ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}

// Authenticate exchanges a Personal API key for a token without persisting credentials.
func (c *Client) Authenticate(ctx context.Context, key string) error {
	body, status, err := c.request(ctx, http.MethodPost, tokenPath, "ApiKey "+key)
	if err != nil {
		return err
	}
	c.token = strings.TrimSpace(string(body))
	if status != 200 || c.token == "" || strings.ContainsAny(c.token, "\r\n\t ") {
		return fmt.Errorf("invalid Garmin token response")
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, dst any, optional bool) (bool, error) {
	body, status, err := c.request(ctx, http.MethodGet, path, "Bearer "+c.token)
	if err != nil {
		return false, err
	}
	if optional && status == 204 {
		return false, nil
	}
	if status != 200 || len(bytes.TrimSpace(body)) == 0 || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return false, fmt.Errorf("empty or unexpected Garmin response")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return false, fmt.Errorf("invalid Garmin JSON response")
	}
	return true, nil
}

// DailySummary returns one summary for a Garmin calendar date (YYYY-MM-DD).
func (c *Client) DailySummary(ctx context.Context, day string) (Summary, error) {
	var result struct {
		Data []Summary `json:"data"`
	}
	query := url.Values{"startDate": {day}, "endDate": {day}}
	if _, err := c.get(ctx, summaryPath+"?"+query.Encode(), &result, false); err != nil {
		return Summary{}, err
	}
	if len(result.Data) != 1 {
		return Summary{}, fmt.Errorf("expected exactly one daily summary")
	}
	if result.Data[0].Day != day {
		return Summary{}, fmt.Errorf("missing or mismatched calendarDate")
	}
	return result.Data[0], nil
}

// DailySleep returns nil for HTTP 204 (no measurements).
func (c *Client) DailySleep(ctx context.Context, day string) (*Sleep, error) {
	var result Sleep
	query := url.Values{"date": {day}}
	ok, err := c.get(ctx, sleepPath+"?"+query.Encode(), &result, true)
	if err != nil || !ok {
		return nil, err
	}
	if result.Day != day {
		return nil, fmt.Errorf("missing or mismatched calendarDate")
	}
	return &result, nil
}

// DailyHRV returns nil for HTTP 204 (no measurements).
func (c *Client) DailyHRV(ctx context.Context, day string) (*HRV, error) {
	var result struct {
		Summary *HRV `json:"hrvSummary"`
	}
	ok, err := c.get(ctx, hrvPath+url.PathEscape(day), &result, true)
	if err != nil || !ok {
		return nil, err
	}
	if result.Summary == nil {
		return nil, fmt.Errorf("missing hrvSummary")
	}
	if result.Summary.Day != day {
		return nil, fmt.Errorf("missing or mismatched calendarDate")
	}
	return result.Summary, nil
}
