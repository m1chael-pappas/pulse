// Package maccal reads upcoming events from macOS Calendar.app via
// AppleScript. No OAuth, no Google Cloud Console, no iCal subscription
// URLs — works with any account Calendar.app is already syncing
// (Google, iCloud, Exchange, etc.).
//
// First run triggers a one-time TCC prompt ("pulse wants to access your
// calendar"); after the user clicks OK, subsequent calls are silent.
package maccal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/michaelpappas/pulse/internal/providers"
)

type Provider struct {
	// Calendars, when non-empty, restricts the fetch to just these
	// calendar names. Empty means "every visible calendar".
	Calendars []string

	// Lookahead caps how many events to surface.
	Lookahead int

	// LookaheadDays caps the time window in days.
	LookaheadDays int
}

func New(cals []string, lookahead, lookaheadDays int) *Provider {
	return &Provider{
		Calendars:     cals,
		Lookahead:     lookahead,
		LookaheadDays: lookaheadDays,
	}
}

func (p *Provider) Name() string             { return "Calendar" }
func (p *Provider) Interval() time.Duration  { return 5 * time.Minute }
func (p *Provider) PreferredWidth() int      { return 64 }

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := providers.Snapshot{Name: p.Name(), Status: providers.StatusOK}

	if runtime.GOOS != "darwin" {
		snap.Status = providers.StatusUnknown
		snap.Subtitle = "macOS Calendar.app only"
		return snap
	}

	lookahead := p.Lookahead
	if lookahead <= 0 {
		lookahead = 6
	}
	days := p.LookaheadDays
	if days <= 0 {
		days = 7
	}

	events, err := fetchEvents(ctx, p.Calendars, days)
	if err != nil {
		snap.Status = providers.StatusWarn
		snap.Err = err
		snap.Subtitle = describeErr(err)
		return snap
	}

	now := time.Now()
	events = filterUpcoming(events, now)
	sort.Slice(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	if len(events) > lookahead {
		events = events[:lookahead]
	}

	snap.Events = events
	switch len(events) {
	case 0:
		snap.Subtitle = fmt.Sprintf("no events in next %dd", days)
	case 1:
		snap.Subtitle = "1 event upcoming"
	default:
		snap.Subtitle = fmt.Sprintf("%d events in next %dd", len(events), days)
	}
	if next := nextEvent(events, now); next != nil {
		snap.Header = headerForNext(*next, now)
	}
	return snap
}

func filterUpcoming(events []providers.Event, now time.Time) []providers.Event {
	out := events[:0]
	cutoff := now.Add(-5 * time.Minute)
	for _, e := range events {
		// Still "now" if it started recently and hasn't ended.
		if !e.End.IsZero() && e.End.Before(cutoff) {
			continue
		}
		if e.End.IsZero() && e.Start.Before(cutoff) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func nextEvent(events []providers.Event, now time.Time) *providers.Event {
	for i, e := range events {
		if !e.Start.Before(now) {
			return &events[i]
		}
	}
	return nil
}

func headerForNext(e providers.Event, now time.Time) string {
	d := e.Start.Sub(now)
	switch {
	case d <= 0:
		return "happening now"
	case d < time.Hour:
		return fmt.Sprintf("next in %dm", int(d.Minutes())+1)
	case d < 24*time.Hour:
		return fmt.Sprintf("next in %dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return "next " + e.Start.Format("Mon 15:04")
	}
}

// fetchEvents shells out to osascript. AppleScript is the most boring,
// permission-friendly path: a single 5min refresh costs ~200ms and never
// re-prompts after the user grants access once.
func fetchEvents(ctx context.Context, cals []string, days int) ([]providers.Event, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	script := buildScript(cals, days)
	out, err := exec.CommandContext(cctx, "/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("osascript: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return parseEvents(string(out)), nil
}

// buildScript composes the AppleScript that emits one line per event:
//
//	START_ISO|END_ISO|ALL_DAY|CALENDAR|TITLE|LOCATION
//
// Using a delimiter that's vanishingly unlikely to appear in a meeting
// title ('|') keeps parsing trivial; commas / tabs would collide with
// event names like "Q1, Q2 planning".
func buildScript(cals []string, days int) string {
	// Build the calendar filter as an AppleScript list literal.
	var filter string
	if len(cals) > 0 {
		quoted := make([]string, len(cals))
		for i, c := range cals {
			quoted[i] = `"` + escapeAS(c) + `"`
		}
		filter = fmt.Sprintf("set wanted to {%s}\n", strings.Join(quoted, ", "))
	} else {
		filter = "set wanted to {}\n"
	}

	return filter + fmt.Sprintf(`
set fromDate to (current date) - (5 * minutes)
set toDate to (current date) + (%d * days)
set out to ""

tell application "Calendar"
  repeat with c in calendars
    set cn to name of c
    if (count of wanted) = 0 or wanted contains cn then
      set evs to (every event of c whose start date is greater than or equal to fromDate and start date is less than or equal to toDate)
      repeat with e in evs
        set sd to start date of e
        set ed to end date of e
        set ad to (allday event of e)
        try
          set tt to summary of e
        on error
          set tt to ""
        end try
        try
          set ll to location of e
          if ll is missing value then set ll to ""
        on error
          set ll to ""
        end try
        set out to out & (my isoDate(sd)) & "|" & (my isoDate(ed)) & "|" & ad & "|" & cn & "|" & tt & "|" & ll & linefeed
      end repeat
    end if
  end repeat
end tell

return out

on isoDate(d)
  set y to year of d
  set m to (month of d as integer)
  set dd to day of d
  set hh to hours of d
  set mm to minutes of d
  set ss to seconds of d
  return (my pad(y, 4)) & "-" & (my pad(m, 2)) & "-" & (my pad(dd, 2)) & "T" & (my pad(hh, 2)) & ":" & (my pad(mm, 2)) & ":" & (my pad(ss, 2))
end isoDate

on pad(n, w)
  set s to (n as text)
  repeat while length of s < w
    set s to "0" & s
  end repeat
  return s
end pad
`, days)
}

func parseEvents(raw string) []providers.Event {
	// Dedupe by (start, title). AppleScript's `every event` query can
	// emit the same event multiple times when a recurring series has been
	// expanded server-side and the parent rule is also returned — and
	// users frequently end up with the same event invite mirrored across
	// two calendars (personal + work). Either way the right move is to
	// collapse identical (start, title) pairs.
	seen := make(map[string]struct{})
	var out []providers.Event

	sc := bufio.NewScanner(strings.NewReader(raw))
	// Calendar.app can dump events with very long location strings —
	// 64KiB is the default Scanner limit and we've already overflowed it.
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}
		start := parseLocalISO(parts[0])
		if start.IsZero() {
			continue
		}
		title := strings.TrimSpace(parts[4])
		key := start.Format(time.RFC3339) + "|" + strings.ToLower(title)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		end := parseLocalISO(parts[1])
		allDay := strings.EqualFold(strings.TrimSpace(parts[2]), "true")
		out = append(out, providers.Event{
			Start:    start,
			End:      end,
			Title:    title,
			Location: strings.TrimSpace(parts[5]),
			AllDay:   allDay,
		})
	}
	return out
}

// parseLocalISO parses our YYYY-MM-DDTHH:MM:SS strings as the user's
// local time — AppleScript dates are unzoned, and "wall clock in local
// tz" is exactly what we want for displaying meeting times.
func parseLocalISO(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func describeErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not authorized"), strings.Contains(msg, "denied"):
		return "macOS Calendar access denied — grant in System Settings → Privacy → Calendars"
	case strings.Contains(msg, "Application isn’t running"), strings.Contains(msg, "Application isn't running"):
		return "open Calendar.app once so it can start syncing"
	}
	if len(msg) > 80 {
		msg = msg[:77] + "…"
	}
	return msg
}

func escapeAS(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}
