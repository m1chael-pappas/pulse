package panels

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Cost struct {
	today float64
	month float64
}

func NewCost() Cost { return Cost{} }

func (c Cost) Init() tea.Cmd { return nil }

func (c Cost) Update(msg tea.Msg) (Panel, tea.Cmd) {
	return c, nil
}

func (c Cost) View() string {
	title := titleStyle.Render("💸 Cost")
	body := fmt.Sprintf(
		"%s  $%.2f\n%s  $%.2f",
		labelStyle.Render("today"), c.today,
		labelStyle.Render("month"), c.month,
	)
	return lipgloss.JoinVertical(lipgloss.Left, title, "", body)
}
