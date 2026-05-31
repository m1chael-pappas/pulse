package claudecode

import (
	"context"
	"time"

	"github.com/michaelpappas/pulse/internal/sources"
)

type ActivitySnapshot struct {
	SessionsToday int
	LastPrompt    string
	LastProject   string
}

type Source struct {
	// ProjectsDir defaults to ~/.claude/projects
	ProjectsDir string
}

func (s *Source) Name() string            { return "claudecode" }
func (s *Source) Interval() time.Duration { return 30 * time.Second }

func (s *Source) Refresh(ctx context.Context) (sources.Snapshot, error) {
	// TODO: walk ~/.claude/projects/**/*.jsonl, parse the last N lines per file,
	// aggregate session counts and most recent prompt.
	return sources.Snapshot{
		At:   time.Now(),
		Data: ActivitySnapshot{},
	}, nil
}
