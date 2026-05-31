package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	oauthUsageURL         = "https://api.anthropic.com/api/oauth/usage"
	oauthBetaHeader       = "oauth-2025-04-20"
	oauthDefaultUserAgent = "claude-code/2.1.0"
)

// oauthWindow is one quota slice returned by /api/oauth/usage. Utilization
// is 0.0–1.0; ResetsAt is ISO8601.
type oauthWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

func (w oauthWindow) Reset() time.Time {
	if w.ResetsAt == "" {
		return time.Time{}
	}
	for _, fmt := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(fmt, w.ResetsAt); err == nil {
			return t
		}
	}
	return time.Time{}
}

// oauthExtraUsage describes top-up credit usage on Max plans.
type oauthExtraUsage struct {
	IsEnabled    bool    `json:"is_enabled"`
	MonthlyLimit float64 `json:"monthly_limit"`
	UsedCredits  float64 `json:"used_credits"`
	Utilization  float64 `json:"utilization"`
	Currency     string  `json:"currency"`
}

// oauthUsage is the full response from /api/oauth/usage.
type oauthUsage struct {
	FiveHour          *oauthWindow     `json:"five_hour"`
	SevenDay          *oauthWindow     `json:"seven_day"`
	SevenDayOpus      *oauthWindow     `json:"seven_day_opus"`
	SevenDaySonnet    *oauthWindow     `json:"seven_day_sonnet"`
	SevenDayRoutines  *oauthWindow     `json:"seven_day_routines"`
	SevenDayOAuthApps *oauthWindow     `json:"seven_day_oauth_apps"`
	ExtraUsage        *oauthExtraUsage `json:"extra_usage"`
}

// errOAuthUnauthorized means the token is rejected — almost always expired.
// The caller should signal "re-run `claude login`" in the UI.
var errOAuthUnauthorized = errors.New("OAuth token rejected (401) — run `claude login`")

// errOAuthRateLimited means Anthropic returned 429. Backoff is left to the
// caller's refresh cadence.
var errOAuthRateLimited = errors.New("OAuth usage endpoint rate-limited (429)")

func fetchOAuthUsage(ctx context.Context, accessToken string) (*oauthUsage, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, oauthUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", oauthBetaHeader)
	req.Header.Set("User-Agent", oauthDefaultUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		var out oauthUsage
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode oauth usage: %w", err)
		}
		return &out, nil
	case http.StatusUnauthorized:
		return nil, errOAuthUnauthorized
	case http.StatusTooManyRequests:
		return nil, errOAuthRateLimited
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("oauth usage HTTP %d: %s", resp.StatusCode, string(body))
	}
}
