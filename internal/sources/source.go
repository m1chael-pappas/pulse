package sources

import (
	"context"
	"time"
)

// Source is the contract every data feed implements. Add a new panel by
// implementing this in internal/sources/<name>/ and wiring it into ui.NewApp.
type Source interface {
	Name() string
	Refresh(ctx context.Context) (Snapshot, error)
	Interval() time.Duration
}

// Snapshot is a generic point-in-time view of a source. Panels type-assert
// Data to their concrete shape (e.g. anthropic.UsageSnapshot).
type Snapshot struct {
	At   time.Time
	Data any
}
