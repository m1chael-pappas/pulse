package panels

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Activity struct {
	lines []string
}

func NewActivity() Activity {
	return Activity{
		lines: []string{"(no recent sessions found)"},
	}
}

func (a Activity) Init() tea.Cmd { return nil }

func (a Activity) Update(msg tea.Msg) (Panel, tea.Cmd) {
	return a, nil
}

func (a Activity) View() string {
	title := titleStyle.Render("🕒 Recent activity")
	out := []string{title, ""}
	out = append(out, a.lines...)
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}
