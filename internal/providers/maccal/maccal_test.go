package maccal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/m1chael-pappas/pulse/internal/providers"
)

// Real icalBuddy 1.10.1 output under the flags fetchEvents passes
// (-nc -nrd -eep ... -df %Y-%m-%d -tf %H:%M).
const sample = `• 🇪🇺 Amanda on holiday
    2026-07-03 - 2026-07-27
• Public Holiday
    2026-07-28
• Web team standup - slack update
    2026-07-28 at 09:45 - 10:00
• Sprint showcase (+AI) & retro
    location: Sydney-2-MEET 2.03 (5) [Zoom]
    2026-07-30 at 10:30 - 10:45
• Marketing website deployment
    2026-07-30
• Multi-day offsite
    2026-08-03 at 09:00 - 2026-08-04 at 17:00
`

// anchor is 2026-07-28 08:00 local — past midnight, so an all-day event
// today has already "ended" if end-of-day handling is missing.
var anchor = time.Date(2026, 7, 28, 8, 0, 0, 0, time.Local)

func find(t *testing.T, title string) providers.Event {
	t.Helper()
	for _, e := range parseIcalBuddy(sample) {
		if e.Title == title {
			return e
		}
	}
	t.Fatalf("event %q not parsed", title)
	return providers.Event{}
}

func kept(t *testing.T, title string) bool {
	t.Helper()
	for _, e := range filterUpcoming(parseIcalBuddy(sample), anchor) {
		if e.Title == title {
			return true
		}
	}
	return false
}

func TestParsesEveryEvent(t *testing.T) {
	if got, want := len(parseIcalBuddy(sample)), 6; got != want {
		t.Errorf("parsed %d events, want %d", got, want)
	}
}

// Titles legitimately contain parentheses; -nc means there is no calendar
// suffix to strip, so nothing may be trimmed off the end.
func TestTitleParenthesesPreserved(t *testing.T) {
	e := find(t, "Sprint showcase (+AI) & retro")
	if e.Location != "Sydney-2-MEET 2.03 (5) [Zoom]" {
		t.Errorf("location = %q", e.Location)
	}
}

func TestTimedEvent(t *testing.T) {
	e := find(t, "Web team standup - slack update")
	want := time.Date(2026, 7, 28, 9, 45, 0, 0, time.Local)
	if !e.Start.Equal(want) {
		t.Errorf("start = %v, want %v", e.Start, want)
	}
	if e.AllDay {
		t.Error("timed event marked all-day")
	}
}

// An all-day event today must survive past 00:00 — the regression that made
// holidays disappear from the tile every morning.
func TestAllDayTodaySurvivesFilter(t *testing.T) {
	if e := find(t, "Public Holiday"); !e.AllDay {
		t.Error("bare date not marked all-day")
	}
	if !kept(t, "Public Holiday") {
		t.Fatal("today's all-day event was filtered out")
	}
}

// A multi-day all-day range that ended yesterday should still be dropped.
func TestFinishedAllDayRangeDropped(t *testing.T) {
	if kept(t, "🇪🇺 Amanda on holiday") {
		t.Fatal("all-day range ending 2026-07-27 kept on 2026-07-28")
	}
}

func TestMultiDayTimedEvent(t *testing.T) {
	e := find(t, "Multi-day offsite")
	wantEnd := time.Date(2026, 8, 4, 17, 0, 0, 0, time.Local)
	if !e.End.Equal(wantEnd) {
		t.Errorf("end = %v, want %v", e.End, wantEnd)
	}
}

// icalBuddy reports an unmatched -ic filter as "No calendars." — capital N.
// A case-sensitive match let that fall through to the raw error text.
func TestDescribeErrMatchesIcalBuddyCasing(t *testing.T) {
	got := describeErr(context.Background(), "", errors.New("icalBuddy: error: No calendars."), []string{"Work"})
	if strings.Contains(got, "No calendars") {
		t.Errorf("raw icalBuddy text surfaced to the user: %q", got)
	}
	if !strings.Contains(got, "calendar") {
		t.Errorf("describeErr = %q, want a calendar-config hint", got)
	}
}

func TestDescribeErrPermissionDenied(t *testing.T) {
	got := describeErr(context.Background(), "", errors.New("icalBuddy: Access Denied"), nil)
	if !strings.Contains(got, "System Settings") {
		t.Errorf("describeErr = %q, want the TCC hint", got)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	got := truncate(strings.Repeat("🇪🇺", 60))
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
}

// Guards the original bug: relative dates are unreachable while -nrd is
// passed, but if the flag is ever dropped these must not silently vanish.
func TestRelativeDatesStillParse(t *testing.T) {
	for _, in := range []string{"today at 09:45 - 10:00", "tomorrow at 14:00 - 15:00"} {
		if start, _, _ := parseDateTime(in); start.IsZero() {
			t.Errorf("parseDateTime(%q) returned zero start", in)
		}
	}
}
