package panels

import tea "github.com/charmbracelet/bubbletea"

// Panel is a self-contained Bubble Tea model rendered inside one grid cell.
// Each panel owns its data, refresh timer, and view rendering.
type Panel interface {
	Init() tea.Cmd
	Update(tea.Msg) (Panel, tea.Cmd)
	View() string
}
