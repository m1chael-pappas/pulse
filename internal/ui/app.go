package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/michaelpappas/pulse/internal/config"
	"github.com/michaelpappas/pulse/internal/providers"
	"github.com/michaelpappas/pulse/internal/providers/claudecode"
	"github.com/michaelpappas/pulse/internal/providers/claudestatus"
	"github.com/michaelpappas/pulse/internal/providers/gcal"
	"github.com/michaelpappas/pulse/internal/providers/github"
	"github.com/michaelpappas/pulse/internal/providers/maccal"
	"github.com/michaelpappas/pulse/internal/providers/system"
	"github.com/michaelpappas/pulse/internal/ui/tile"
)

type App struct {
	width, height int
	focus         int
	providers     []providers.Provider
	snapshots     []providers.Snapshot
	showBreakdown bool // press 'b' to toggle the "Where it came from" rows
	scrollY       int  // vertical scroll offset into the rendered grid
}

func NewApp(cfg config.Config) App {
	cc := claudecode.FromConfig(
		cfg.ClaudeCode.Note,
		cfg.ClaudeCode.SessionBudget,
		cfg.ClaudeCode.DailyBudget,
		cfg.ClaudeCode.MonthBudget,
	)
	provs := []providers.Provider{cc, system.New()}
	if cfg.ClaudeStatus.IsEnabled() {
		provs = append(provs, claudestatus.New())
	}
	if cfg.GitHub.IsEnabled() {
		provs = append(provs, github.New(cfg.GitHub.Limit, cfg.GitHub.Repos, cfg.GitHub.Orgs, cfg.GitHub.ShowAll))
	}
	switch {
	case cfg.MacCal.IsEnabled():
		provs = append(provs, maccal.New(cfg.MacCal.Calendars, cfg.MacCal.Lookahead, cfg.MacCal.LookaheadDays))
	case cfg.GCal.ICSURL != "":
		provs = append(provs, gcal.New(cfg.GCal.ICSURL, cfg.GCal.Lookahead, cfg.GCal.LookaheadDays))
	}
	return App{
		providers: provs,
		snapshots: make([]providers.Snapshot, len(provs)),
	}
}

type refreshMsg struct {
	idx  int
	snap providers.Snapshot
}

type tickMsg struct {
	idx int
}

type clockMsg time.Time

func (a App) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(a.providers)*2+1)
	for i := range a.providers {
		cmds = append(cmds, a.refresh(i))
		cmds = append(cmds, a.tick(i))
	}
	cmds = append(cmds, clockTick())
	return tea.Batch(cmds...)
}

func (a App) refresh(i int) tea.Cmd {
	p := a.providers[i]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return refreshMsg{idx: i, snap: p.Refresh(ctx)}
	}
}

func (a App) tick(i int) tea.Cmd {
	p := a.providers[i]
	return tea.Tick(p.Interval(), func(time.Time) tea.Msg {
		return tickMsg{idx: i}
	})
}

func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockMsg(t) })
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "r":
			cmds := make([]tea.Cmd, len(a.providers))
			for i := range a.providers {
				cmds[i] = a.refresh(i)
			}
			return a, tea.Batch(cmds...)
		case "tab":
			if len(a.providers) > 0 {
				a.focus = (a.focus + 1) % len(a.providers)
			}
			return a, nil
		case "shift+tab":
			if len(a.providers) > 0 {
				a.focus = (a.focus - 1 + len(a.providers)) % len(a.providers)
			}
			return a, nil
		case "b":
			a.showBreakdown = !a.showBreakdown
			a.scrollY = 0
			return a, nil
		case "j", "down":
			a.scrollY++
			return a, nil
		case "k", "up":
			if a.scrollY > 0 {
				a.scrollY--
			}
			return a, nil
		case "pgdown", "f", "ctrl+d":
			a.scrollY += pageStep(a.height)
			return a, nil
		case "pgup", "ctrl+u":
			a.scrollY -= pageStep(a.height)
			if a.scrollY < 0 {
				a.scrollY = 0
			}
			return a, nil
		case "g", "home":
			a.scrollY = 0
			return a, nil
		}

	case refreshMsg:
		if msg.idx < len(a.snapshots) {
			a.snapshots[msg.idx] = msg.snap
		}
		return a, nil

	case tickMsg:
		return a, tea.Batch(a.refresh(msg.idx), a.tick(msg.idx))

	case clockMsg:
		return a, clockTick()
	}
	return a, nil
}

func (a App) View() string {
	if a.width == 0 || a.height == 0 {
		return "loading pulse…"
	}

	// Two-phase layout:
	//   1. Group providers into rows. Heroes get their own row;
	//      secondaries pack greedy-left-to-right using PreferredWidth as a
	//      sizing hint that determines how many fit per row.
	//   2. Render each row to fill the full terminal width — tiles in the
	//      row split the width evenly so columns always align with each
	//      other and with the hero. Within a row, every tile renders at
	//      the row's max natural height so adjacent tiles share a baseline.
	snaps := make([]providers.Snapshot, len(a.providers))
	for i, snap := range a.snapshots {
		if snap.Name == "" {
			snap = providers.Snapshot{
				Name:   a.providers[i].Name(),
				Status: providers.StatusUnknown,
				Note:   "loading…",
			}
		}
		if !a.showBreakdown {
			snap.Breakdown = nil
		}
		snaps[i] = snap
	}

	rows := planRows(a.providers, a.width)
	rowsView := make([]string, 0, len(rows))
	for _, row := range rows {
		n := len(row)
		perW := a.width / n
		extra := a.width - perW*n

		// First pass: measure each tile's natural height at its row width.
		rowHeight := 0
		tileWs := make([]int, n)
		for k, idx := range row {
			w := perW
			if k < extra {
				w++
			}
			tileWs[k] = w
			if h := tile.NaturalHeight(snaps[idx], w); h > rowHeight {
				rowHeight = h
			}
		}

		// Second pass: render every tile in the row at the row's max height
		// so left/right neighbors line up cleanly at top AND bottom.
		rowTiles := make([]string, n)
		for k, idx := range row {
			rowTiles[k] = tile.Render(snaps[idx], tileWs[k], rowHeight, idx == a.focus)
		}
		rowsView = append(rowsView, lipgloss.JoinHorizontal(lipgloss.Top, rowTiles...))
	}
	grid := lipgloss.JoinVertical(lipgloss.Left, rowsView...)

	// Manual scroll: chop the rendered grid into lines, take a window
	// starting at scrollY. This is simpler than wiring bubbles/viewport
	// just to handle "content taller than terminal" and works the same
	// regardless of how tall any single tile ends up.
	gridLines := strings.Split(grid, "\n")
	visible := a.height - 2 // reserve 1 row for help, 1 spacer
	if visible < 4 {
		visible = 4
	}
	maxScroll := len(gridLines) - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.scrollY > maxScroll {
		a.scrollY = maxScroll
	}
	end := a.scrollY + visible
	if end > len(gridLines) {
		end = len(gridLines)
	}
	view := strings.Join(gridLines[a.scrollY:end], "\n")

	breakdownHint := "b: cost breakdown"
	if a.showBreakdown {
		breakdownHint = "b: hide breakdown"
	}
	scrollInfo := ""
	if maxScroll > 0 {
		scrollInfo = fmt.Sprintf("  j/k: scroll (%d/%d)", a.scrollY, maxScroll)
	}
	help := helpStyle.Render(fmt.Sprintf(
		"tab: focus  r: refresh  %s%s  q: quit  •  %s",
		breakdownHint,
		scrollInfo,
		time.Now().Format("Mon 15:04:05"),
	))
	return lipgloss.JoinVertical(lipgloss.Left, view, help)
}

// pageStep returns roughly one screenful for PgUp/PgDn / Ctrl-U / Ctrl-D.
// Leaves a couple of lines of overlap so the user keeps their place.
func pageStep(termHeight int) int {
	step := termHeight - 4
	if step < 1 {
		step = 1
	}
	return step
}

// planRows groups providers into rows. Hero providers always get their
// own row (full terminal width). Secondaries pack greedily — the next
// tile joins the current row if its PreferredWidth still fits, otherwise
// it wraps. The renderer then splits the actual terminal width evenly
// across the tiles in each row, so a row of one tile stretches to fill,
// and a row of two splits 50/50.
func planRows(provs []providers.Provider, termWidth int) [][]int {
	var rows [][]int
	var current []int
	currentW := 0
	flush := func() {
		if len(current) > 0 {
			rows = append(rows, current)
			current = nil
			currentW = 0
		}
	}
	for i, p := range provs {
		if hero, ok := p.(interface{ Hero() bool }); ok && hero.Hero() {
			flush()
			rows = append(rows, []int{i})
			continue
		}
		w := p.PreferredWidth()
		if w <= 0 {
			w = termWidth
		}
		if currentW > 0 && currentW+w > termWidth {
			flush()
		}
		current = append(current, i)
		currentW += w
	}
	flush()
	return rows
}

var helpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("241")).
	MarginTop(1)
