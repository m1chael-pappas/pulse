package panels

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Usage struct {
	inTokens  int64
	outTokens int64
}

func NewUsage() Usage { return Usage{} }

func (u Usage) Init() tea.Cmd { return nil }

func (u Usage) Update(msg tea.Msg) (Panel, tea.Cmd) {
	return u, nil
}

func (u Usage) View() string {
	title := titleStyle.Render("📊 Tokens")
	body := fmt.Sprintf(
		"%s  %d\n%s  %d",
		labelStyle.Render("input "), u.inTokens,
		labelStyle.Render("output"), u.outTokens,
	)
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body)
}
