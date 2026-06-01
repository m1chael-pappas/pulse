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

	"github.com/michaelpappas/pulse/internal/providers"
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

// fetchEvents runs icalBuddy and parses its bullet-delimited output.
//
// We use `-b` to control the bullet, `-ps` for "no separator" (single
// space) between item parts, and `-iep` to choose which event properties
// to dump in a known order. icalBuddy's `eventsToday+N` window covers
// today plus N more days and crucially expands recurring events.
func fetchEvents(ctx context.Context, bin string, cals []string, days int) ([]providers.Event, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	args := []string{
		"-nc",                              // no calendar names header
		"-npn",                             // no property names
		"-eep", "notes,attendees,url",      // exclude noisy props
		"-iep", "title,datetime,location",  // include only these
		"-b", "@@EVENT@@",                  // unique bullet so events split cleanly
		"-ps", "| @@FIELD@@ |",             // property separator
		"-df", "%Y-%m-%d",                  // ISO dates
		"-tf", "%H:%M",                     // 24h times
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

// parseIcalBuddy parses our bullet-delimited icalBuddy output. Each event
// looks like:
//
//	@@EVENT@@ Standup
//	    | @@FIELD@@ | 2026-06-01 at 12:30 - 13:00
//	    | @@FIELD@@ | Sydney-G-Ground
//
// We split on @@EVENT@@ to get one chunk per event, then walk lines to
// extract title (the first line) and parse the datetime line.
func parseIcalBuddy(raw string) []providers.Event {
	seen := make(map[string]struct{})
	var out []providers.Event

	chunks := strings.Split(raw, "@@EVENT@@")
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		title, datetimeLine, location := splitChunk(chunk)
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

func splitChunk(chunk string) (title, datetime, location string) {
	sc := bufio.NewScanner(strings.NewReader(chunk))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			title = line
			first = false
			continue
		}
		// Property lines are "| @@FIELD@@ | value"
		const sep = "| @@FIELD@@ |"
		if i := strings.Index(line, sep); i >= 0 {
			value := strings.TrimSpace(line[i+len(sep):])
			switch {
			case looksLikeDateTime(value):
				datetime = value
			default:
				if location == "" {
					location = value
				}
			}
		}
	}
	return
}

func looksLikeDateTime(s string) bool {
	return len(s) >= 10 && (s[4] == '-' || strings.Contains(s, " at "))
}

// parseDateTime handles icalBuddy's date/time formats:
//
//	2026-06-01 at 12:30 - 13:00              (today / single-day)
//	2026-06-01 at 12:30 - 2026-06-02 at 13:00 (multi-day)
//	2026-06-01                               (all-day)
//	today at 12:30 - 13:00                   (relative — we asked for ISO so unusual)
func parseDateTime(s string) (start, end time.Time, allDay bool) {
	if s == "" {
		return
	}

	// Multi-day case has " - " between two complete "<date> at <time>"
	// fragments; single-day has just " - <time>".
	if i := strings.Index(s, " - "); i >= 0 {
		left := strings.TrimSpace(s[:i])
		right := strings.TrimSpace(s[i+3:])
		start = parseFragment(left, time.Time{})
		end = parseFragment(right, start)
		allDay = !strings.Contains(left, " at ")
		return
	}
	start = parseFragment(strings.TrimSpace(s), time.Time{})
	allDay = !strings.Contains(s, " at ")
	return
}

// parseFragment turns "2026-06-01 at 12:30" or "12:30" or "2026-06-01"
// into a local time. For a bare "12:30" we borrow the date from the start
// time of the same event (handed in as base).
func parseFragment(s string, base time.Time) time.Time {
	if strings.Contains(s, " at ") {
		t, err := time.ParseInLocation("2006-01-02 at 15:04", s, time.Local)
		if err != nil {
			return time.Time{}
		}
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t
	}
	// Bare time — combine with the date from the base.
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
