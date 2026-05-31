package gcal

import (
	"context"
	"time"

	"github.com/michaelpappas/pulse/internal/sources"
)

type Event struct {
	Title string
	Start time.Time
	End   time.Time
}

type CalendarSnapshot struct {
	Upcoming []Event
}

type Source struct {
	CalendarID string
}

func (s *Source) Name() string            { return "gcal" }
func (s *Source) Interval() time.Duration { return 5 * time.Minute }

func (s *Source) Refresh(ctx context.Context) (sources.Snapshot, error) {
	// TODO: OAuth via golang.org/x/oauth2 (loopback flow), token cached to
	// ~/.config/pulse/gcal-token.json. Use google.golang.org/api/calendar/v3
	// to list events for the next 24h.
	return sources.Snapshot{
		At:   time.Now(),
		Data: CalendarSnapshot{},
	}, nil
}
