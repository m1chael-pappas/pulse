package panels

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Calendar struct {
	events []string
}

func NewCalendar() Calendar {
	return Calendar{
		events: []string{"(connect Google Calendar to see events)"},
	}
}

func (c Calendar) Init() tea.Cmd { return nil }

func (c Calendar) Update(msg tea.Msg) (Panel, tea.Cmd) {
	return c, nil
}

func (c Calendar) View() string {
	title := titleStyle.Render("📅 Up next")
	lines := make([]string, 0, len(c.events)+1)
	lines = append(lines, title, "")
	for _, e := range c.events {
		lines = append(lines, "• "+e)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
