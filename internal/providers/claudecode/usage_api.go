package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
//
// Schema varies by plan tier:
//   - Max plans → five_hour / seven_day / seven_day_opus / seven_day_sonnet
//     are populated; extra_usage may be present for top-up credits.
//   - Enterprise → standard window keys are null; the monthly company cap
//     lives in extra_usage (monthly_limit + used_credits). Anthropic also
//     occasionally returns promo windows under codenames like
//     omelette_promotional / iguana_necktie / tangelo — pulse renders any
//     with a non-zero utilization.
type oauthUsage struct {
	FiveHour            *oauthWindow     `json:"five_hour"`
	SevenDay            *oauthWindow     `json:"seven_day"`
	SevenDayOpus        *oauthWindow     `json:"seven_day_opus"`
	SevenDaySonnet      *oauthWindow     `json:"seven_day_sonnet"`
	SevenDayRoutines    *oauthWindow     `json:"seven_day_routines"`
	SevenDayOAuthApps   *oauthWindow     `json:"seven_day_oauth_apps"`
	SevenDayOmelette    *oauthWindow     `json:"seven_day_omelette"`
	SevenDayCowork      *oauthWindow     `json:"seven_day_cowork"`
	OmelettePromotional *oauthWindow     `json:"omelette_promotional"`
	IguanaNecktie       *oauthWindow     `json:"iguana_necktie"`
	Tangelo             *oauthWindow     `json:"tangelo"`
	ExtraUsage          *oauthExtraUsage `json:"extra_usage"`
}

// errOAuthUnauthorized means the token is rejected — almost always expired.
// The caller should signal "re-run `claude login`" in the UI.
var errOAuthUnauthorized = errors.New("OAuth token rejected (401) — run `claude login`")

// errOAuthRateLimited means Anthropic returned 429. Backoff is left to the
// caller's refresh cadence.
var errOAuthRateLimited = errors.New("OAuth usage endpoint rate-limited (429)")

// FetchUsageRaw reads the Keychain credential and returns the raw response
// body from /api/oauth/usage — useful for diagnostics. Bypasses the
// rate-limit gate so users running `pulse usage` get an immediate answer.
func FetchUsageRaw(ctx context.Context) ([]byte, error) {
	acct, _ := readAccount()
	blob, err := readKeychainCredential(ctx, acct.EmailAddress)
	if err != nil {
		blob, err = readKeychainCredential(ctx, "")
		if err != nil {
			return nil, err
		}
	}
	cred, err := parseOAuthCredential(blob)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", oauthBetaHeader)
	req.Header.Set("User-Agent", oauthDefaultUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func fetchOAuthUsage(ctx context.Context, accessToken string) (*oauthUsage, error) {
	if until, blocked := oauthRateGate.blockedUntil(); blocked {
		return nil, fmt.Errorf("%w (retry after %s)", errOAuthRateLimited, time.Until(until).Round(time.Second))
	}

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
		oauthRateGate.clear()
		return &out, nil
	case http.StatusUnauthorized:
		return nil, errOAuthUnauthorized
	case http.StatusTooManyRequests:
		oauthRateGate.trip(parseRetryAfter(resp.Header.Get("Retry-After")))
		return nil, errOAuthRateLimited
	case http.StatusForbidden:
		// Most likely the endpoint is not exposed for this plan tier
		// (e.g. Enterprise uses the Admin API instead). Treat as a longer
		// backoff so we don't poll uselessly.
		oauthRateGate.trip(15 * time.Minute)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("oauth usage HTTP 403 (likely not exposed for this plan): %s", string(body))
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("oauth usage HTTP %d: %s", resp.StatusCode, string(body))
	}
}

func parseRetryAfter(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
