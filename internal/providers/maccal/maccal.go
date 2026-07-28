// Package maccal reads upcoming events from macOS Calendar.app.
//
// Uses icalBuddy (brew install ical-buddy) because it talks to EventKit
// and properly expands recurring events. AppleScript's `every event whose
// start date >=` filter silently drops every recurring event whose first
// occurrence is in the past — which on most work calendars is most events.
//
// Works with any account Calendar.app is already syncing (Google via
// System Settings → Internet Accounts, iCloud, Exchange). First run pops
// a one-time TCC prompt; after Allow it's silent.
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

	"github.com/m1chael-pappas/pulse/internal/providers"
)

type Provider struct {
	// Calendars, when non-empty, restricts the fetch to just these
	// calendar names. Empty = every visible calendar.
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

func (p *Provider) Name() string            { return "Calendar" }
func (p *Provider) Interval() time.Duration { return 5 * time.Minute }
func (p *Provider) PreferredWidth() int     { return 64 }

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := providers.Snapshot{Name: p.Name(), Status: providers.StatusOK}

	if runtime.GOOS != "darwin" {
		snap.Status = providers.StatusUnknown
		snap.Subtitle = "macOS Calendar.app only"
		return snap
	}

	bin, err := findIcalBuddy()
	if err != nil {
		snap.Status = providers.StatusWarn
		snap.Subtitle = "install icalBuddy: brew install ical-buddy"
		snap.Err = err
		return snap
	}

	lookahead := p.Lookahead
	if lookahead <= 0 {
		lookahead = 20
	}
	days := p.LookaheadDays
	if days <= 0 {
		days = 7
	}

	events, err := fetchEvents(ctx, bin, p.Calendars, days)
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

func findIcalBuddy() (string, error) {
	for _, name := range []string{"icalBuddy", "icalbuddy"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("icalBuddy not on PATH (brew install ical-buddy)")
}

// fetchEvents runs icalBuddy with sensible defaults and parses its
// standard bullet-prefixed output. icalBuddy's `eventsToday+N` window
// covers today plus N more days and (crucially, unlike AppleScript)
// expands recurring events into their occurrences.
func fetchEvents(ctx context.Context, bin string, cals []string, days int) ([]providers.Event, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	args := []string{
		"-nc",                                               // strip the " (calendar name)" suffix from titles
		"-nrd",                                              // absolute dates only — see parseFragment
		"-eep", "notes,attendees,url,uid,phone,description", // suppress huge fields
		"-df", "%Y-%m-%d",
		"-tf", "%H:%M",
	}
	if len(cals) > 0 {
		args = append(args, "-ic", strings.Join(cals, ","))
	}
	args = append(args, fmt.Sprintf("eventsToday+%d", days))

	out, err := exec.CommandContext(cctx, bin, args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("icalBuddy: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return parseIcalBuddy(string(out)), nil
}

// parseIcalBuddy parses icalBuddy's output under the flags fetchEvents
// passes, which looks like:
//
//   - Web team standup
//     location: Sydney-2-MEET 2.05 (3) [Zoom]
//     2026-06-01 at 09:45 - 10:00
//   - Sprint kick-off
//     location: ...
//     2026-06-02 at 10:00 - 11:00
//
// Bullet character is `•` (U+2022). Property lines are indented and
// either start with `<name>: <value>` or are the date/time line which
// has no property name. `-nc` means titles carry no " (calendar name)"
// suffix, and `-nrd` means dates are always literal YYYY-MM-DD.
func parseIcalBuddy(raw string) []providers.Event {
	const bullet = "•"
	seen := make(map[string]struct{})
	var out []providers.Event

	// Split on the bullet character. Every chunk after the first is one
	// event; the first is whatever came before the first bullet (header).
	chunks := strings.Split(raw, bullet)
	for i, chunk := range chunks {
		if i == 0 {
			continue
		}
		title, datetimeLine, location := splitEventBlock(chunk)
		start, end, allDay := parseDateTime(datetimeLine)
		if start.IsZero() {
			continue
		}
		key := start.Format(time.RFC3339) + "|" + strings.ToLower(title)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, providers.Event{
			Start:    start,
			End:      end,
			Title:    title,
			Location: location,
			AllDay:   allDay,
		})
	}
	return out
}

// splitEventBlock extracts (title, datetime line, location) from one
// bullet's worth of icalBuddy output.
//
// Title is the first non-empty line, taken verbatim — `-nc` already
// removes the calendar-name suffix, and titles legitimately contain
// parentheses (e.g. "Sprint showcase (+AI) & retro"). Property lines are
// indented and use "name: value"; the date/time line has no name.
func splitEventBlock(chunk string) (title, datetime, location string) {
	sc := bufio.NewScanner(strings.NewReader(chunk))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if first {
			title = trimmed
			first = false
			continue
		}
		// Only indented lines are properties of the current event; an
		// un-indented line that somehow appears here would belong to the
		// next chunk and we shouldn't have entered this branch.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			continue
		}
		if name, value, ok := splitProperty(trimmed); ok {
			switch strings.ToLower(name) {
			case "location":
				location = value
			}
			continue
		}
		// Property line without "name:" prefix = the date/time row.
		if datetime == "" && looksLikeDateTime(trimmed) {
			datetime = trimmed
		}
	}
	return
}

func splitProperty(line string) (name, value string, ok bool) {
	// icalBuddy property names are short (location, attendees, notes…).
	// Cap the lookahead so values containing ":" (URLs, timestamps) don't
	// trick us into treating part of the value as a key.
	limit := 32
	if len(line) < limit {
		limit = len(line)
	}
	for i := 0; i < limit; i++ {
		c := line[i]
		if c == ':' {
			name = line[:i]
			value = strings.TrimSpace(line[i+1:])
			// A real property name is all letters and underscores.
			for _, r := range name {
				if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
					return "", "", false
				}
			}
			return name, value, true
		}
	}
	return "", "", false
}

func looksLikeDateTime(s string) bool {
	if strings.HasPrefix(strings.ToLower(s), "today") ||
		strings.HasPrefix(strings.ToLower(s), "tomorrow") {
		return true
	}
	return len(s) >= 10 && s[4] == '-' && s[7] == '-'
}

// parseDateTime handles icalBuddy's date/time formats:
//
//	2026-06-04 at 12:30 - 13:00               (specific day)
//	2026-06-04 at 12:30 - 2026-06-05 at 13:00 (multi-day)
//	2026-06-04                                (all-day)
//	2026-06-04 - 2026-06-08                   (all-day, multi-day)
func parseDateTime(s string) (start, end time.Time, allDay bool) {
	if s == "" {
		return
	}
	now := time.Now()
	if i := strings.Index(s, " - "); i >= 0 {
		left := strings.TrimSpace(s[:i])
		right := strings.TrimSpace(s[i+3:])
		start = parseFragment(left, time.Time{}, now)
		end = parseFragment(right, start, now)
		allDay = !strings.Contains(left, " at ")
	} else {
		start = parseFragment(strings.TrimSpace(s), time.Time{}, now)
		allDay = !strings.Contains(s, " at ")
	}

	// All-day events are bare dates, so both ends parse to midnight and
	// icalBuddy's end date is the inclusive last day. Without stretching the
	// end to end-of-day, filterUpcoming sees an event that "ended" at 00:00
	// and drops today's all-day events five minutes into the morning.
	if allDay && !start.IsZero() {
		if end.IsZero() {
			end = start
		}
		end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, time.Local)
	}
	return
}

// parseFragment turns one half of an icalBuddy date range into a local
// time. Accepts:
//
//	"2026-06-04 at 12:30"
//	"2026-06-04"        (all-day)
//	"12:30"             (bare time — borrowed date from base)
//	"today at 09:45" / "tomorrow at 14:00"
//
// The relative forms are unreachable while fetchEvents passes -nrd, but are
// kept so a build without that flag degrades to dropping the odd event
// rather than every event more than two days out.
func parseFragment(s string, base, now time.Time) time.Time {
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "today at "):
		t, err := time.ParseInLocation("15:04", strings.TrimPrefix(s, "today at "), time.Local)
		if err != nil {
			return time.Time{}
		}
		return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
	case strings.HasPrefix(low, "tomorrow at "):
		t, err := time.ParseInLocation("15:04", strings.TrimPrefix(s, "tomorrow at "), time.Local)
		if err != nil {
			return time.Time{}
		}
		tomorrow := now.AddDate(0, 0, 1)
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
	case low == "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	case low == "tomorrow":
		tomorrow := now.AddDate(0, 0, 1)
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, time.Local)
	}
	if strings.Contains(s, " at ") {
		if t, err := time.ParseInLocation("2006-01-02 at 15:04", s, time.Local); err == nil {
			return t
		}
		return time.Time{}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("15:04", s, time.Local); err == nil && !base.IsZero() {
		return time.Date(base.Year(), base.Month(), base.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
	}
	return time.Time{}
}

func filterUpcoming(events []providers.Event, now time.Time) []providers.Event {
	out := events[:0]
	cutoff := now.Add(-5 * time.Minute)
	for _, e := range events {
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

func describeErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not authorized"), strings.Contains(msg, "denied"):
		return "Calendar access denied — System Settings → Privacy → Calendars"
	case strings.Contains(msg, "no calendar"):
		return "no matching calendar (check [maccal].calendars in config)"
	}
	if len(msg) > 80 {
		msg = msg[:77] + "…"
	}
	return msg
}
