package anthropic

import (
	"context"
	"time"

	"github.com/michaelpappas/pulse/internal/sources"
)

type UsageSnapshot struct {
	TodayUSD     float64
	MonthUSD     float64
	InputTokens  int64
	OutputTokens int64
}

type Source struct {
	APIKey string
}

func (s *Source) Name() string             { return "anthropic" }
func (s *Source) Interval() time.Duration  { return 15 * time.Minute }

func (s *Source) Refresh(ctx context.Context) (sources.Snapshot, error) {
	// TODO: call Anthropic Admin API:
	//   GET /v1/organizations/usage_report/messages
	//   GET /v1/organizations/cost_report
	// Auth header: x-api-key: <admin key>, anthropic-version: 2023-06-01
	return sources.Snapshot{
		At: time.Now(),
		Data: UsageSnapshot{
			TodayUSD: 0,
			MonthUSD: 0,
		},
	}, nil
}
