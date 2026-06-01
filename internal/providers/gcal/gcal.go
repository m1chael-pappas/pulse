// Package gcal surfaces upcoming Google Calendar events via the calendar's
// "secret iCal URL" — no OAuth, no Google Cloud Console required.
//
// In Google Calendar: Settings → pick a calendar → Integrate Calendar →
// copy "Secret address in iCal format". Paste it into pulse's config:
//
//	[gcal]
//	ics_url = "https://calendar.google.com/calendar/ical/.../basic.ics"
//
// pulse fetches and parses the feed every few minutes. Anyone holding the
// URL can read your events, so treat it like a password.
package gcal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"

	"github.com/m1chael-pappas/pulse/internal/providers"
)

const (
	defaultLookahead     = 6
	defaultLookaheadDays = 7
)

type Provider struct {
	ICSURL        string
	Lookahead     int
	LookaheadDays int
}

func New(icsURL string, lookahead, lookaheadDays int) *Provider {
	return &Provider{
		ICSURL:        icsURL,
		Lookahead:     lookahead,
		LookaheadDays: lookaheadDays,
	}
}

func (p *Provider) Name() string             { return "Calendar" }
func (p *Provider) Interval() time.Duration  { return 5 * time.Minute }
func (p *Provider) PreferredWidth() int      { return 64 }

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := providers.Snapshot{Name: p.Name(), Status: providers.StatusOK}
	if p.ICSURL == "" {
		snap.Status = providers.StatusUnknown
		snap.Subtitle = "set gcal.ics_url in config to enable"
		return snap
	}

	lookahead := p.Lookahead
	if lookahead <= 0 {
		lookahead = defaultLookahead
	}
	lookaheadDays := p.LookaheadDays
	if lookaheadDays <= 0 {
		lookaheadDays = defaultLookaheadDays
	}

	cal, err := fetchAndParse(ctx, p.ICSURL)
	if err != nil {
		snap.Status = providers.StatusWarn
		snap.Err = err
		snap.Subtitle = "fetch failed: " + clipErr(err.Error())
		return snap
	}

	now := time.Now()
	horizon := now.Add(time.Duration(lookaheadDays) * 24 * time.Hour)
	events := upcomingEvents(cal, now, horizon, lookahead)

	snap.Events = events
	switch len(events) {
	case 0:
		snap.Subtitle = fmt.Sprintf("no events in next %dd", lookaheadDays)
	case 1:
		snap.Subtitle = "1 event upcoming"
	default:
		snap.Subtitle = fmt.Sprintf("%d events in next %dd", len(events), lookaheadDays)
	}

	// Surface the "next event" as the tile header so it's visible even
	// in tiny terminals where only the first row fits.
	if next := nextEvent(events, now); next != nil {
		snap.Header = headerForNext(*next, now)
	}
	return snap
}

func fetchAndParse(ctx context.Context, url string) (*ics.Calendar, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pulse/0.1 (+https://github.com/m1chael-pappas/pulse)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return ics.ParseCalendar(resp.Body)
}

func upcomingEvents(cal *ics.Calendar, from, until time.Time, limit int) []providers.Event {
	if cal == nil {
		return nil
	}
	out := make([]providers.Event, 0, limit)
	for _, ev := range cal.Events() {
		start, err := ev.GetStartAt()
		if err != nil || start.IsZero() {
			continue
		}
		end, _ := ev.GetEndAt()
		// Drop events ended before `from` (the 5-min staleness margin
		// avoids dropping the one happening right now).
		if !end.IsZero() && end.Before(from.Add(-5*time.Minute)) {
			continue
		}
		if start.After(until) {
			continue
		}
		// Cancelled events surface with STATUS:CANCELLED — skip them.
		if status := propVal(ev, ics.ComponentPropertyStatus); strings.EqualFold(status, "CANCELLED") {
			continue
		}
		title := propVal(ev, ics.ComponentPropertySummary)
		loc := propVal(ev, ics.ComponentPropertyLocation)

		out = append(out, providers.Event{
			Start:    start.Local(),
			End:      end.Local(),
			Title:    title,
			Location: loc,
			AllDay:   isAllDay(start, end),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	if len(out) > limit {
		out = out[:limit]
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
		return fmt.Sprintf("next: %s in %dm", clipTitle(e.Title), int(d.Minutes())+1)
	case d < 24*time.Hour:
		return fmt.Sprintf("next: %s in %dh%dm", clipTitle(e.Title), int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("next: %s on %s", clipTitle(e.Title), e.Start.Format("Mon 15:04"))
	}
}

func propVal(ev *ics.VEvent, name ics.ComponentProperty) string {
	if p := ev.GetProperty(name); p != nil {
		return p.Value
	}
	return ""
}

// isAllDay infers all-day from a start that lands on midnight and a
// duration that's an exact multiple of 24h. ICS technically distinguishes
// DATE vs DATE-TIME values but the parser already normalises both into
// time.Time, so we infer from the shape.
func isAllDay(start, end time.Time) bool {
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
		return false
	}
	if end.IsZero() {
		return false
	}
	diff := end.Sub(start)
	return diff%(24*time.Hour) == 0 && diff > 0
}

func clipTitle(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 28 {
		return s[:25] + "…"
	}
	return s
}

func clipErr(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		return s[:57] + "…"
	}
	return s
}
