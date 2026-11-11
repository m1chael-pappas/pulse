// Package aiusage merges usage and spend from several AI tools into one
// tile. Claude Code supplies the base snapshot and Copilot adds a spend
// window and per-model rows.
package aiusage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/m1chael-pappas/pulse/internal/providers"
	"github.com/m1chael-pappas/pulse/internal/providers/claudecode"
)

const copilotAccountTTL = 5 * time.Minute

type Provider struct {
	claude *claudecode.Provider

	account   copilotAccount
	accountAt time.Time
}

func New(claude *claudecode.Provider) *Provider { return &Provider{claude: claude} }

func (p *Provider) Name() string            { return "AI usage" }
func (p *Provider) Interval() time.Duration { return p.claude.Interval() }
func (p *Provider) PreferredWidth() int     { return p.claude.PreferredWidth() }
func (p *Provider) Hero() bool              { return true }

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := p.claude.Refresh(ctx)
	prefix := strings.TrimSpace("Claude " + snap.Header)
	for i := range snap.Windows {
		snap.Windows[i].Label = prefix + " · " + snap.Windows[i].Label
	}
	for _, s := range snap.Sections {
		for i := range s.Rows {
			s.Rows[i].Label = "Claude · " + s.Rows[i].Label
		}
	}
	snap.Name = p.Name()
	snap.Header = ""

	now := time.Now()
	models, _ := fetchCopilotModels(ctx, now.AddDate(0, 0, -29))
	if now.Sub(p.accountAt) > copilotAccountTTL {
		account, err := fetchCopilotAccount(ctx)
		switch {
		case err == nil:
			p.account, p.accountAt = account, now
		case len(models) > 0 && p.accountAt.IsZero():
			snap.Subtitle += " · Copilot account spend unavailable"
		}
	}
	if !p.accountAt.IsZero() {
		snap.Windows = append(snap.Windows, copilotWindow(p.account))
	}
	return withModelRows(snap, copilotRows(models))
}

func copilotWindow(a copilotAccount) providers.Window {
	label := "Copilot"
	if a.Plan != "" {
		label += " " + strings.ToUpper(a.Plan[:1]) + a.Plan[1:]
	}
	w := providers.Window{
		Label:    label + " · Monthly spend",
		Used:     a.SpendUSD,
		Unit:     providers.UnitUSD,
		ResetsAt: a.ResetsAt,
	}
	if !a.ResetsAt.IsZero() {
		w.Start = a.ResetsAt.AddDate(0, -1, 0)
	}
	return w
}

func copilotRows(models []copilotModel) []providers.BreakdownEntry {
	rows := make([]providers.BreakdownEntry, 0, len(models))
	for _, m := range models {
		rows = append(rows, providers.BreakdownEntry{
			Label:      "Copilot · " + m.Model,
			Value:      m.SpendUSD,
			Unit:       providers.UnitUSD,
			Detail:     float64(m.Tokens),
			DetailUnit: providers.UnitTokens,
		})
	}
	return rows
}

// withModelRows adds rows to the snapshot's per-model section and sorts
// the section by spend, highest first. It creates the section when the
// snapshot has none.
func withModelRows(snap providers.Snapshot, rows []providers.BreakdownEntry) providers.Snapshot {
	if len(rows) == 0 {
		return snap
	}
	if len(snap.Sections) == 0 {
		snap.Sections = []providers.Section{{Title: claudecode.ModelsTitle}}
	}
	merged := append(append([]providers.BreakdownEntry{}, snap.Sections[0].Rows...), rows...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Value > merged[j].Value })
	snap.Sections[0].Rows = merged
	return snap
}
