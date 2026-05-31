package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/michaelpappas/pulse/internal/ui/panels"
)

type App struct {
	width, height int
	focus         int
	panels        []panels.Panel
}

func NewApp() App {
	return App{
		panels: []panels.Panel{
			panels.NewCost(),
			panels.NewCalendar(),
			panels.NewUsage(),
			panels.NewActivity(),
		},
	}
}

func (a App) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(a.panels))
	for _, p := range a.panels {
		cmds = append(cmds, p.Init())
	}
	return tea.Batch(cmds...)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "tab":
			a.focus = (a.focus + 1) % len(a.panels)
			return a, nil
		case "shift+tab":
			a.focus = (a.focus - 1 + len(a.panels)) % len(a.panels)
			return a, nil
		}
	}

	cmds := make([]tea.Cmd, 0, len(a.panels))
	for i, p := range a.panels {
		updated, cmd := p.Update(msg)
		a.panels[i] = updated
		cmds = append(cmds, cmd)
	}
	return a, tea.Batch(cmds...)
}

func (a App) View() string {
	if a.width == 0 {
		return "loading pulse…"
	}

	cellW := a.width/2 - 2
	cellH := (a.height-2)/2 - 1

	cell := func(i int) string {
		style := panelStyle.Width(cellW).Height(cellH)
		if i == a.focus {
			style = style.BorderForeground(lipgloss.Color("205"))
		}
		return style.Render(a.panels[i].View())
	}

	top := lipgloss.JoinHorizontal(lipgloss.Top, cell(0), cell(1))
	bot := lipgloss.JoinHorizontal(lipgloss.Top, cell(2), cell(3))
	grid := lipgloss.JoinVertical(lipgloss.Left, top, bot)

	help := helpStyle.Render(fmt.Sprintf("tab: focus  q: quit  •  panel %d/%d", a.focus+1, len(a.panels)))
	return lipgloss.JoinVertical(lipgloss.Left, grid, help)
}

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)
)
