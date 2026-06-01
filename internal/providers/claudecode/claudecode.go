package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/michaelpappas/pulse/internal/providers"
)

// NoteMode controls what the tile shows in the footer "note" line.
type NoteMode int

const (
	NoteModePrompt    NoteMode = iota // "<project> — <last prompt>" (default)
	NoteModeProject                   // "<project>" only — hides prompt text
	NoteModeOff                       // hide the note entirely
)

type Provider struct {
	// Root defaults to ~/.claude/projects.
	Root string
	// Optional USD budget overrides. Zero = no budget bar, fall back to
	// time-elapsed visualization. Anthropic doesn't publish per-window
	// dollar caps for Max plans, so we never invent these.
	SessionBudget float64
	DailyBudget   float64
	MonthBudget   float64
	// NoteMode controls footer privacy. Default shows last prompt.
	NoteMode NoteMode

	// Cached last-known-good OAuth usage. When the live fetch fails
	// transiently (429 rate-limit, network blip, brief 401 during token
	// refresh) we keep showing this so the tile doesn't flip between the
	// "real plan limits" view and the "local cost estimate" view every
	// few minutes. Cleared on account switch.
	cachedUsage  *oauthUsage
	cachedUsedAt time.Time
}

func New() *Provider { return &Provider{} }

// FromConfig constructs a Provider from a free-form config map. Unknown
// values are ignored; missing values fall back to the Provider zero value.
func FromConfig(note string, session, daily, month float64) *Provider {
	return &Provider{
		NoteMode:      parseNoteMode(note),
		SessionBudget: session,
		DailyBudget:   daily,
		MonthBudget:   month,
	}
}

func parseNoteMode(s string) NoteMode {
	switch s {
	case "prompt":
		return NoteModePrompt
	case "project":
		return NoteModeProject
	default:
		// "off", "", anything unrecognised → off (privacy-safe default)
		return NoteModeOff
	}
}

func (p *Provider) Name() string            { return "Claude Code" }
func (p *Provider) Interval() time.Duration { return 30 * time.Second }

// PreferredWidth is the minimum useful width for this tile. The App
// expands the Claude tile to the full terminal width via Hero() below
// when there's room — this value is the fallback for narrow terminals.
func (p *Provider) PreferredWidth() int { return 72 }

// Hero marks this provider as the dashboard's primary tile. The renderer
// always promotes it to the full terminal width and gives it its own
// row, so secondary tiles (system, status, github, calendar) pack
// symmetrically beneath it regardless of terminal size.
func (p *Provider) Hero() bool { return true }

func (p *Provider) root() string {
	if p.Root != "" {
		return p.Root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	now := time.Now()
	agg, err := walk(p.root(), now)
	if err != nil {
		return providers.Snapshot{Name: p.Name(), Status: providers.StatusUnknown, Err: err}
	}

	acct, _ := readAccount()
	if changed, _ := recordAccountIfChanged(acct); changed {
		oauthRateGate.clear()
		// Different account → different quotas. Drop the cache so we
		// don't briefly mis-attribute one account's usage to another.
		p.cachedUsage = nil
		p.cachedUsedAt = time.Time{}
	}

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayReset := dayStart.Add(24 * time.Hour)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	monthReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

	sessionStart := now.Add(-5 * time.Hour)
	if !agg.sessionStart.IsZero() {
		sessionStart = agg.sessionStart
	}
	sessionReset := sessionStart.Add(5 * time.Hour)

	note := ""
	switch p.NoteMode {
	case NoteModeOff:
	case NoteModeProject:
		note = agg.lastProject
	default:
		note = agg.lastPrompt
		if agg.lastProject != "" && note != "" {
			note = agg.lastProject + " — " + note
		}
	}

	name := p.Name()
	subtitle := "Estimated from local Claude logs at API rates"
	header := ""
	if acct.OrganizationName != "" {
		name = "Claude — " + acct.OrganizationName
		header = acct.PlanLabel()
		subtitle = fmt.Sprintf("Logged in as %s · %s",
			acct.EmailAddress, subtitle)
	}

	// Try to enrich with real plan limits from the OAuth usage endpoint.
	// On transient failures (429 rate-limit, brief network errors) we
	// keep using the most recent successful response so the tile holds
	// its layout instead of flipping back to the local-cost fallback.
	// The cache is dropped after 30 minutes — long enough to ride out
	// Anthropic's 5-minute rate-limit windows, short enough that genuine
	// auth/permission problems still surface.
	usage, oauthErr := p.fetchUsage(ctx, acct)
	if usage != nil {
		p.cachedUsage = usage
		p.cachedUsedAt = now
	}
	const tokenEquivNote = "Token-equivalent — Max/Enterprise plans bill flat-rate"
	const cacheMaxAge = 30 * time.Minute

	var windows []providers.Window
	switch {
	case usage != nil:
		windows = p.realQuotaWindows(usage, now)
		subtitle = tokenEquivNote
	case p.cachedUsage != nil && now.Sub(p.cachedUsedAt) < cacheMaxAge:
		windows = p.realQuotaWindows(p.cachedUsage, now)
		age := now.Sub(p.cachedUsedAt).Round(time.Second)
		subtitle = fmt.Sprintf("%s · cached %s ago (%s)", tokenEquivNote, age, oauthHint(oauthErr))
	default:
		windows = p.localCostWindows(agg, sessionStart, sessionReset, dayStart, dayReset, monthStart, monthReset)
		if oauthErr != nil {
			subtitle = tokenEquivNote + " · " + oauthHint(oauthErr)
		} else {
			subtitle = tokenEquivNote
		}
	}

	return providers.Snapshot{
		Name:      name,
		Status:    statusFromOAuthErr(oauthErr),
		Subtitle:  subtitle,
		Header:    header,
		CostUSD:   agg.costToday,
		Windows:   windows,
		Breakdown: buildBreakdown(agg),
		History:   buildHistory(agg, now),
		Stats:     buildStats(agg),
		Note:      note,
	}
}

func (p *Provider) localCostWindows(agg aggregate, sessionStart, sessionReset, dayStart, dayReset, monthStart, monthReset time.Time) []providers.Window {
	return []providers.Window{
		{Label: "5h session", Used: agg.sessionCost, Limit: p.SessionBudget, Unit: providers.UnitUSD, Start: sessionStart, ResetsAt: sessionReset},
		{Label: "Today", Used: agg.costToday, Limit: p.DailyBudget, Unit: providers.UnitUSD, Start: dayStart, ResetsAt: dayReset},
		{Label: "Month", Used: agg.costMonth, Limit: p.MonthBudget, Unit: providers.UnitUSD, Start: monthStart, ResetsAt: monthReset},
	}
}

func (p *Provider) realQuotaWindows(u *oauthUsage, now time.Time) []providers.Window {
	// Enterprise responses null-out the standard quota keys and put the
	// real company-level cap in extra_usage. Detect that case and render
	// a different window set rather than five empty bars.
	if u.isEnterpriseShape() {
		return p.enterpriseWindows(u, now)
	}
	return p.maxPlanWindows(u, now)
}

// isEnterpriseShape reports whether all of the standard Max-plan quota
// windows are null — the signal that this is an Admin-API-billed account.
func (u *oauthUsage) isEnterpriseShape() bool {
	return u.FiveHour == nil && u.SevenDay == nil &&
		u.SevenDayOpus == nil && u.SevenDaySonnet == nil
}

func (p *Provider) maxPlanWindows(u *oauthUsage, now time.Time) []providers.Window {
	weeklyReset := time.Time{}
	if u.SevenDay != nil {
		weeklyReset = u.SevenDay.Reset()
	}

	out := []providers.Window{
		mkPercent("5h session", u.FiveHour, time.Time{}, 5),
		mkPercent("Weekly", u.SevenDay, time.Time{}, 24*7),
		mkPercent("Weekly Opus", u.SevenDayOpus, weeklyReset, 24*7),
		mkPercent("Weekly Sonnet", u.SevenDaySonnet, weeklyReset, 24*7),
		mkPercent("Daily Routines", u.SevenDayRoutines, weeklyReset, 24*7),
		mkPercent("OAuth apps", u.SevenDayOAuthApps, weeklyReset, 24*7),
	}
	if u.ExtraUsage != nil && u.ExtraUsage.IsEnabled {
		out = append(out, providers.Window{
			Label:    "Extra credits",
			Used:     u.ExtraUsage.Utilization,
			Limit:    100,
			Unit:     providers.UnitPercent,
			ResetsAt: monthEnd(now),
		})
	}
	return dropEmpty(out)
}

func (p *Provider) enterpriseWindows(u *oauthUsage, now time.Time) []providers.Window {
	out := []providers.Window{}
	if u.ExtraUsage != nil && u.ExtraUsage.IsEnabled && u.ExtraUsage.MonthlyLimit > 0 {
		// extra_usage stores used_credits in cents (e.g. 693 = $6.93). The
		// utilization field already accounts for that, so it's our source
		// of truth for the bar; we just need a nice $X / $Y label.
		used := u.ExtraUsage.UsedCredits / 100.0
		limit := u.ExtraUsage.MonthlyLimit / 100.0
		out = append(out, providers.Window{
			Label:    "Monthly spend",
			Used:     used,
			Limit:    limit,
			Unit:     providers.UnitUSD,
			ResetsAt: monthEnd(now),
		})
	}
	return dropEmpty(out)
}

func mkPercent(label string, w *oauthWindow, sharedReset time.Time, hours float64) providers.Window {
	used := 0.0
	reset := sharedReset
	if w != nil {
		used = w.Utilization
		if r := w.Reset(); !r.IsZero() {
			reset = r
		}
	}
	start := time.Time{}
	if !reset.IsZero() && hours > 0 {
		start = reset.Add(-time.Duration(hours) * time.Hour)
	}
	return providers.Window{
		Label:    label,
		Used:     used,
		Limit:    100,
		Unit:     providers.UnitPercent,
		Start:    start,
		ResetsAt: reset,
	}
}

func dropEmpty(in []providers.Window) []providers.Window {
	out := in[:0]
	for _, w := range in {
		if w.ResetsAt.IsZero() && w.Used == 0 && w.Limit == 0 {
			continue
		}
		out = append(out, w)
	}
	return out
}

func monthEnd(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
}

func (p *Provider) fetchUsage(ctx context.Context, acct Account) (*oauthUsage, error) {
	blob, err := readKeychainCredential(ctx, acct.EmailAddress)
	if err != nil {
		// Retry without account scoping — login order or older builds may
		// have stored under a non-email account name.
		blob, err = readKeychainCredential(ctx, "")
		if err != nil {
			return nil, err
		}
	}
	cred, err := parseOAuthCredential(blob)
	if err != nil {
		return nil, err
	}
	if cred.Expired() {
		return nil, errOAuthUnauthorized
	}
	return fetchOAuthUsage(ctx, cred.AccessToken)
}

func oauthHint(err error) string {
	switch {
	case errors.Is(err, ErrKeychainUnavailable):
		return "no Keychain access (non-macOS?)"
	case errors.Is(err, ErrKeychainNotFound):
		return "not logged in — run `claude` to authenticate"
	case errors.Is(err, ErrKeychainDenied):
		return "Keychain prompt denied — re-run and pick Always Allow"
	case errors.Is(err, errOAuthUnauthorized):
		return "token expired — run `claude login`"
	case errors.Is(err, errOAuthRateLimited):
		return "rate-limited (429) — backing off"
	default:
		return "plan-limits API failed: " + err.Error()
	}
}

func statusFromOAuthErr(err error) providers.Status {
	switch {
	case err == nil:
		return providers.StatusOK
	case errors.Is(err, errOAuthUnauthorized), errors.Is(err, ErrKeychainDenied):
		return providers.StatusWarn
	case errors.Is(err, errOAuthRateLimited):
		return providers.StatusWarn
	default:
		return providers.StatusUnknown
	}
}

func buildStats(agg aggregate) []providers.BreakdownEntry {
	return []providers.BreakdownEntry{
		{Label: "Today (API equiv.)", Value: agg.costToday, Unit: providers.UnitUSD},
		{Label: "30d (API equiv.)", Value: agg.cost30d, Unit: providers.UnitUSD},
		{Label: "30d tokens", Value: float64(agg.tokens30d), Unit: providers.UnitTokens},
		{Label: "Today tokens", Value: float64(agg.tokensInputToday + agg.tokensOutputToday + agg.tokensCacheToday), Unit: providers.UnitTokens},
	}
}

func buildHistory(agg aggregate, now time.Time) []providers.HistoryPoint {
	points := make([]providers.HistoryPoint, 30)
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for i := 0; i < 30; i++ {
		day := startDay.AddDate(0, 0, -29+i)
		key := day.Format("2006-01-02")
		points[i] = providers.HistoryPoint{At: day, Value: agg.costByDay[key]}
	}
	return points
}

// buildBreakdown returns the top per-model and per-project contributions,
// answering "where did the cost come from?" Zero-cost entries (e.g. Claude
// Code's "<synthetic>" tool-result echoes) are dropped — they're noise.
func buildBreakdown(agg aggregate) []providers.BreakdownEntry {
	var out []providers.BreakdownEntry

	models := sortedTop(agg.costByModel, 4)
	for _, kv := range models {
		if kv.v <= 0 || kv.k == "<synthetic>" {
			continue
		}
		out = append(out, providers.BreakdownEntry{Label: kv.k, Value: kv.v, Unit: providers.UnitUSD})
	}

	projects := sortedTop(agg.costByProject, 3)
	for _, kv := range projects {
		if kv.v <= 0 {
			continue
		}
		out = append(out, providers.BreakdownEntry{Label: "↳ " + kv.k, Value: kv.v, Unit: providers.UnitUSD})
	}
	return out
}

type kv struct {
	k string
	v float64
}

func sortedTop(m map[string]float64, n int) []kv {
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return pairs
}
