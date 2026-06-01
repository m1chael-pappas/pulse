// Package providers defines the data model for AI / dev-tooling providers
// pulse surfaces in the dashboard. Inspired by steipete/CodexBar — each
// provider exposes one or more quota windows, an optional cost figure, a
// status, and an optional human-readable note (e.g. last activity).
package providers

import (
	"context"
	"time"
)

type Status string

const (
	StatusOK       Status = "ok"
	StatusWarn     Status = "warn"
	StatusIncident Status = "incident"
	StatusUnknown  Status = "unknown"
)

type Unit string

const (
	UnitTokens  Unit = "tok"
	UnitUSD     Unit = "USD"
	UnitPercent Unit = "%"   // value held as 0-100 (already a percentage)
	UnitGiB     Unit = "GiB" // value held in GiB
	UnitCount   Unit = "n"   // bare integer-ish counter
)

// Window describes a single quota / counter for a provider.
//
// Bar rendering rules (highest precedence first):
//  1. Limit > 0          → budget-mode bar: Used / Limit, colored by burn
//  2. !Start.IsZero()    → time-elapsed bar: (now - Start) / (ResetsAt - Start)
//  3. otherwise          → dots (no signal)
//
// The UI never invents a Limit. Limit stays zero unless a real one is known
// (user config, or a real plan API).
type Window struct {
	Label    string
	Used     float64
	Limit    float64
	Unit     Unit
	Start    time.Time // when this window began (for time-elapsed bar)
	ResetsAt time.Time
}

// BreakdownEntry is a labelled slice of cost or count (e.g. per-model spend).
type BreakdownEntry struct {
	Label string
	Value float64
	Unit  Unit
}

// HistoryPoint is one data point in a time-series (e.g. cost per day).
type HistoryPoint struct {
	At    time.Time
	Value float64
}

// Event is a single calendar entry rendered as one line in a tile.
type Event struct {
	Start    time.Time
	End      time.Time
	Title    string
	Location string
	AllDay   bool
}

// Snapshot is the point-in-time state of a provider.
type Snapshot struct {
	Name      string
	Status    Status
	Subtitle  string // e.g. "estimated from local logs"
	Header    string // top-right corner of tile (e.g. plan tier)
	CostUSD   float64
	Windows   []Window
	Events    []Event          // optional — upcoming calendar events
	Breakdown []BreakdownEntry // optional — "where the cost came from"
	History   []HistoryPoint   // optional — daily cost histogram
	Stats     []BreakdownEntry // optional — labelled stat grid (Today / 30d / etc)
	Note      string
	Err       error
}

// Provider is the contract every data feed implements.
//
// PreferredWidth returns the tile width this provider wants. Return 0 to
// let the app pick (used for flexible tiles); return a fixed number when
// the provider's content needs the room (e.g. the Claude tile renders a
// 30-day histogram that's easier to read at >=64 cols).
type Provider interface {
	Name() string
	Refresh(ctx context.Context) Snapshot
	Interval() time.Duration
	PreferredWidth() int
}
