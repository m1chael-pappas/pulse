package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/michaelpappas/pulse/internal/config"
	"github.com/michaelpappas/pulse/internal/providers"
	"github.com/michaelpappas/pulse/internal/providers/claudecode"
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
}

func NewApp(cfg config.Config) App {
	cc := claudecode.FromConfig(
		cfg.ClaudeCode.Note,
		cfg.ClaudeCode.SessionBudget,
		cfg.ClaudeCode.DailyBudget,
		cfg.ClaudeCode.MonthBudget,
	)
	provs := []providers.Provider{cc, system.New()}
	if cfg.GitHub.Enabled {
		provs = append(provs, github.New(cfg.GitHub.Limit))
	}
	switch {
	case cfg.MacCal.Enabled:
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

	// Each provider declares its own preferred width. We pack tiles
	// left-to-right, wrapping when the next tile would overflow the
	// terminal width. Tiles size to natural height so the layout reflects
	// content, not the terminal's vertical extent.
	tiles := make([]string, len(a.providers))
	tileWidths := make([]int, len(a.providers))
	for i, snap := range a.snapshots {
		if snap.Name == "" {
			snap = providers.Snapshot{
				Name:   a.providers[i].Name(),
				Status: providers.StatusUnknown,
				Note:   "loading…",
			}
		}
		w := a.providers[i].PreferredWidth()
		if w <= 0 || w > a.width {
			w = a.width
		}
		tileWidths[i] = w
		tiles[i] = tile.Render(snap, w, 0, i == a.focus)
	}

	var rowsView []string
	var rowTiles []string
	rowWidth := 0
	for i, t := range tiles {
		if rowWidth > 0 && rowWidth+tileWidths[i] > a.width {
			rowsView = append(rowsView, lipgloss.JoinHorizontal(lipgloss.Top, rowTiles...))
			rowTiles = nil
			rowWidth = 0
		}
		rowTiles = append(rowTiles, t)
		rowWidth += tileWidths[i]
	}
	if len(rowTiles) > 0 {
		rowsView = append(rowsView, lipgloss.JoinHorizontal(lipgloss.Top, rowTiles...))
	}
	grid := lipgloss.JoinVertical(lipgloss.Left, rowsView...)

	help := helpStyle.Render(fmt.Sprintf(
		"tab: focus  r: refresh  q: quit  •  %s",
		time.Now().Format("Mon 15:04:05"),
	))
	return lipgloss.JoinVertical(lipgloss.Left, grid, help)
}

var helpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("241")).
	MarginTop(1)
