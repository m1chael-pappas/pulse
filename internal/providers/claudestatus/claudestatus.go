// Package claudestatus surfaces the public Claude service status page
// (status.claude.com — a standard Statuspage instance) so users see at a
// glance whether claude.ai, the API, or Claude Code itself is degraded.
//
// Endpoint: GET https://status.claude.com/api/v2/summary.json
// Public, unauthenticated, no rate limits we need to worry about at a 60s
// refresh cadence. Returns: overall indicator, per-component status, and
// the list of unresolved incidents.
package claudestatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/michaelpappas/pulse/internal/providers"
)

const summaryURL = "https://status.claude.com/api/v2/summary.json"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string            { return "Claude Status" }
func (p *Provider) Interval() time.Duration { return 60 * time.Second }
func (p *Provider) PreferredWidth() int     { return 64 }

type summary struct {
	Page struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"page"`
	Status struct {
		Indicator   string `json:"indicator"` // none | minor | major | critical | maintenance
		Description string `json:"description"`
	} `json:"status"`
	Components []component `json:"components"`
	Incidents  []incident  `json:"incidents"`
}

type component struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // operational | degraded_performance | partial_outage | major_outage | under_maintenance
	Showcase bool   `json:"showcase"`
	Group    bool   `json:"group"`
	Only     bool   `json:"only_show_if_degraded"`
}

type incident struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Impact    string    `json:"impact"`
	Shortlink string    `json:"shortlink"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := providers.Snapshot{Name: p.Name()}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, summaryURL, nil)
	if err != nil {
		snap.Status = providers.StatusUnknown
		snap.Err = err
		return snap
	}
	req.Header.Set("User-Agent", "pulse/1.0 (status check)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		snap.Status = providers.StatusUnknown
		snap.Err = err
		snap.Subtitle = "fetch failed"
		return snap
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snap.Status = providers.StatusUnknown
		snap.Subtitle = fmt.Sprintf("HTTP %d from status.claude.com", resp.StatusCode)
		return snap
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		snap.Status = providers.StatusUnknown
		snap.Err = err
		return snap
	}

	var s summary
	if err := json.Unmarshal(body, &s); err != nil {
		snap.Status = providers.StatusUnknown
		snap.Err = fmt.Errorf("decode status summary: %w", err)
		return snap
	}

	snap.Status = statusFromIndicator(s.Status.Indicator)
	snap.Header = strings.ToUpper(s.Status.Indicator)
	snap.Subtitle = s.Status.Description

	// Components section: one row per showcased component, sorted so any
	// degraded ones float to the top (so the user notices instantly).
	rows := make([]providers.BreakdownEntry, 0, len(s.Components))
	for _, c := range s.Components {
		if c.Group || (c.Only && c.Status == "operational") {
			continue
		}
		rows = append(rows, providers.BreakdownEntry{
			Glyph: componentIcon(c.Status),
			Label: c.Name,
			Value: rankComponent(c.Status),
			Unit:  unitStatus,
		})
	}
	// Stable-ish sort: degraded rows first, by impact desc, then alpha.
	sortByImpact(rows)
	if len(rows) > 0 {
		snap.Sections = append(snap.Sections, providers.Section{
			Title: "Components",
			Rows:  rows,
		})
	}

	if len(s.Incidents) > 0 {
		ir := make([]providers.BreakdownEntry, 0, len(s.Incidents))
		for _, inc := range s.Incidents {
			ir = append(ir, providers.BreakdownEntry{
				Glyph: incidentIcon(inc.Impact),
				Label: inc.Name,
				Value: float64(time.Since(inc.UpdatedAt).Seconds()),
				Unit:  "age",
				URL:   inc.Shortlink,
			})
		}
		snap.Sections = append(snap.Sections, providers.Section{
			Title: fmt.Sprintf("Active incidents (%d)", len(s.Incidents)),
			Rows:  ir,
		})
	}

	return snap
}

// unitStatus is rendered by the tile as an empty string — the row label
// already carries the human-readable state via componentIcon, so the value
// column would just be noise.
const unitStatus providers.Unit = "status"

func statusFromIndicator(ind string) providers.Status {
	switch ind {
	case "none":
		return providers.StatusOK
	case "minor", "maintenance":
		return providers.StatusWarn
	case "major", "critical":
		return providers.StatusIncident
	default:
		return providers.StatusUnknown
	}
}

func componentIcon(s string) string {
	switch s {
	case "operational":
		return greenStyle.Render("✓")
	case "degraded_performance":
		return amberStyle.Render("◐")
	case "partial_outage":
		return amberStyle.Render("◑")
	case "major_outage":
		return redStyle.Render("✗")
	case "under_maintenance":
		return blueStyle.Render("⚙")
	default:
		return dimStyle.Render("·")
	}
}

func incidentIcon(impact string) string {
	switch impact {
	case "critical":
		return redStyle.Render("✗")
	case "major":
		return amberStyle.Render("◑")
	case "minor":
		return amberStyle.Render("◐")
	default:
		return dimStyle.Render("·")
	}
}

// Status-glyph palette. Pre-built lipgloss styles so we don't construct
// one per row on every refresh.
var (
	greenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	redStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	amberStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	blueStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// rankComponent returns a numeric severity so we can sort degraded
// components ahead of operational ones in the rendered list.
func rankComponent(s string) float64 {
	switch s {
	case "major_outage":
		return 4
	case "partial_outage":
		return 3
	case "degraded_performance":
		return 2
	case "under_maintenance":
		return 1
	default:
		return 0
	}
}

func sortByImpact(rows []providers.BreakdownEntry) {
	// Simple insertion-sort: tiny N (≤10 components) and stable order.
	for i := 1; i < len(rows); i++ {
		j := i
		for j > 0 && rows[j].Value > rows[j-1].Value {
			rows[j], rows[j-1] = rows[j-1], rows[j]
			j--
		}
	}
}
