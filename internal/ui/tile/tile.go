// Package tile renders a single provider snapshot as a bordered card.
// CodexBar-inspired layout: header (name + status dot + plan tier),
// usage windows with "% left" and reset countdowns, stats grid,
// 30d daily histogram, optional breakdown, and footer note.
package tile

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/michaelpappas/pulse/internal/providers"
)

const barWidth = 18

// Render lays out a snapshot inside a bordered box.
//
// Sizing rules:
//   - width  is honored exactly (so adjacent tiles in a grid align).
//   - height ≤ 0 → the tile sizes to its content (no trailing blank rows).
//   - height > 0 → tile is padded or truncated to that height.
//
// The natural-height mode is what you want when a tile holds genuinely
// variable content; the fixed-height mode is for symmetrical grids.
func Render(snap providers.Snapshot, width, height int, focused bool) string {
	border := borderStyle
	if focused {
		border = borderStyle.BorderForeground(lipgloss.Color("205"))
	}
	inner := width - 4
	if inner < 20 {
		inner = 20
	}

	lines := buildLines(snap, inner)
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)

	style := border.Width(width)
	if height > 0 {
		style = style.Height(height)
	}
	return style.Render(body)
}

// NaturalHeight returns the line count Render would produce for a snapshot
// at the given width, including the two border rows. Callers in a flex
// layout can use this to size each tile to its content.
func NaturalHeight(snap providers.Snapshot, width int) int {
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	return len(buildLines(snap, inner)) + 2 // top + bottom border
}

func buildLines(snap providers.Snapshot, inner int) []string {
	lines := []string{headerRow(snap, inner)}
	if snap.Subtitle != "" {
		lines = append(lines, subtitleStyle.Render(clip(snap.Subtitle, inner)))
	}
	lines = append(lines, "")

	if snap.Err != nil {
		lines = append(lines, errStyle.Render("error: "+snap.Err.Error()))
	} else {
		for _, w := range snap.Windows {
			lines = append(lines, windowLines(w, inner)...)
		}
		if len(snap.Stats) > 0 {
			lines = append(lines, "", statsGrid(snap.Stats, inner))
		}
		if len(snap.History) > 0 {
			lines = append(lines, "", labelStyle.Render("Last 30 days"))
			lines = append(lines, histogram(snap.History, inner))
		}
		if len(snap.Breakdown) > 0 {
			lines = append(lines, "", labelStyle.Render("Where it came from"))
			for _, b := range snap.Breakdown {
				lines = append(lines, breakdownLine(b, inner))
			}
		}
	}

	if snap.Note != "" {
		lines = append(lines, "", noteStyle.Render(clip(snap.Note, inner)))
	}
	return lines
}

func headerRow(snap providers.Snapshot, inner int) string {
	dot := statusDot(snap.Status)
	right := ""
	rightW := 0
	if snap.Header != "" {
		right = planStyle.Render(snap.Header)
		rightW = lipgloss.Width(right)
	}
	// Reserve room for "<dot> <name>  <…gap…>  <Header>".
	// dot+space = 2 cols, minimum gap = 2 cols.
	nameBudget := inner - 2 - rightW - 2
	if nameBudget < 8 {
		nameBudget = 8
	}
	name := clip(snap.Name, nameBudget)
	left := lipgloss.JoinHorizontal(lipgloss.Top, dot, " ", nameStyle.Render(name))
	if right == "" {
		return left
	}
	gap := inner - lipgloss.Width(left) - rightW
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func windowLines(w providers.Window, inner int) []string {
	label := nameStyle.Render(w.Label)
	value := valueStyle.Render(formatValue(w.Unit, w.Used))
	right := resetStyle.Render(countdown(w.ResetsAt))

	first := label + "  " + value
	if w.Limit > 0 && w.Unit != providers.UnitPercent {
		first += labelStyle.Render(fmt.Sprintf(" / %s", formatValue(w.Unit, w.Limit)))
	}
	gap := inner - lipgloss.Width(first) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	header := first + strings.Repeat(" ", gap) + right
	bar := renderBar(w, inner)
	return []string{header, bar}
}

func formatValue(u providers.Unit, v float64) string {
	switch u {
	case providers.UnitUSD:
		return fmt.Sprintf("$%.2f", v)
	case providers.UnitTokens:
		return humanTokens(int64(v))
	case providers.UnitPercent:
		return fmt.Sprintf("%.0f%%", v)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func breakdownLine(b providers.BreakdownEntry, inner int) string {
	label := labelStyle.Render(padRight(clip(b.Label, inner-12), inner-12))
	val := ""
	switch b.Unit {
	case providers.UnitUSD:
		val = fmt.Sprintf("$%.2f", b.Value)
	case providers.UnitTokens:
		val = humanTokens(int64(b.Value))
	default:
		val = fmt.Sprintf("%.0f", b.Value)
	}
	return label + " " + val
}

func statsGrid(stats []providers.BreakdownEntry, inner int) string {
	colW := inner / 2
	var rows []string
	for i := 0; i < len(stats); i += 2 {
		left := statCell(stats[i], colW)
		if i+1 < len(stats) {
			right := statCell(stats[i+1], inner-colW)
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, left, right))
		} else {
			rows = append(rows, left)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func statCell(b providers.BreakdownEntry, w int) string {
	val := formatValue(b.Unit, b.Value)
	return lipgloss.NewStyle().Width(w).Render(
		labelStyle.Render(b.Label) + "\n" + costStyle.Render(val),
	)
}

// histogram renders a multi-row bar chart spanning the full inner width.
// Each day gets one column (or a fractional column when there are more
// days than columns); column height uses 8-step block-quadrant precision
// (▁▂▃▄▅▆▇█) so a 6-row chart effectively has 48 levels of resolution.
func histogram(points []providers.HistoryPoint, inner int) string {
	if len(points) == 0 || inner < 4 {
		return ""
	}
	const rows = 6
	const subSteps = 8 // ▁..█

	// One column per day, padded with leading dots if the window is wider
	// than the data so the chart still spans full width.
	cols := inner
	leading := 0
	if cols > len(points) {
		leading = cols - len(points)
	}

	maxV := 0.0
	for _, p := range points {
		if p.Value > maxV {
			maxV = p.Value
		}
	}

	// Pick a heatmap color per column (cooler at low utilization, warmer
	// at high). All columns in one row get rendered as a single string for
	// a given color so each row is built up by iterating columns.
	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

	type col struct {
		fillRows int  // full ▌ rows
		topRune  rune // partial top rune ('' if none)
		empty    bool
	}
	colData := make([]col, cols)
	for i := 0; i < cols; i++ {
		if i < leading {
			colData[i] = col{empty: true}
			continue
		}
		p := points[i-leading]
		if maxV == 0 || p.Value <= 0 {
			colData[i] = col{empty: true}
			continue
		}
		levels := int((p.Value / maxV) * float64(rows*subSteps))
		if levels < 1 {
			levels = 1
		}
		full := levels / subSteps
		partial := levels % subSteps
		var top rune
		if partial > 0 && full < rows {
			top = bars[partial-1]
		}
		colData[i] = col{fillRows: full, topRune: top}
	}

	rowStrs := make([]string, rows)
	for r := 0; r < rows; r++ {
		// Top row corresponds to the highest visual position (rows-1).
		rowFromBottom := rows - 1 - r
		var b strings.Builder
		for _, c := range colData {
			switch {
			case c.empty:
				b.WriteRune(' ')
			case c.fillRows > rowFromBottom:
				b.WriteRune('█')
			case c.fillRows == rowFromBottom && c.topRune != 0:
				b.WriteRune(c.topRune)
			default:
				b.WriteRune(' ')
			}
		}
		rowStrs[r] = histStyle.Render(b.String())
	}
	return lipgloss.JoinVertical(lipgloss.Left, rowStrs...)
}

func renderBar(w providers.Window, inner int) string {
	width := inner
	if width > barWidth*2 {
		width = barWidth * 2
	}
	switch {
	case w.Limit > 0:
		return budgetBar(w.Used, w.Limit, width)
	case !w.Start.IsZero() && !w.ResetsAt.IsZero():
		return elapsedBar(w.Start, w.ResetsAt, width)
	default:
		return labelStyle.Render(strings.Repeat("·", width))
	}
}

func budgetBar(used, limit float64, width int) string {
	ratio := used / limit
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	color := lipgloss.Color("82")
	switch {
	case ratio >= 0.9:
		color = lipgloss.Color("196")
	case ratio >= 0.7:
		color = lipgloss.Color("214")
	}
	fill := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("░", width-filled))
	return fill + empty
}

func elapsedBar(start, end time.Time, width int) string {
	total := end.Sub(start).Seconds()
	if total <= 0 {
		return labelStyle.Render(strings.Repeat("·", width))
	}
	done := time.Since(start).Seconds() / total
	if done < 0 {
		done = 0
	}
	if done > 1 {
		done = 1
	}
	filled := int(done * float64(width))
	fill := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Render(strings.Repeat("▓", filled))
	empty := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("░", width-filled))
	return fill + empty
}

func countdown(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := time.Until(at)
	if d <= 0 {
		return "resetting…"
	}
	switch {
	case d > 24*time.Hour:
		return fmt.Sprintf("resets in %dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d > time.Hour:
		return fmt.Sprintf("resets in %dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("resets in %dm", int(d.Minutes())+1)
	}
}

func statusDot(s providers.Status) string {
	color := lipgloss.Color("82")
	switch s {
	case providers.StatusWarn:
		color = lipgloss.Color("214")
	case providers.StatusIncident:
		color = lipgloss.Color("196")
	case providers.StatusUnknown:
		color = lipgloss.Color("244")
	}
	return lipgloss.NewStyle().Foreground(color).Render("●")
}

func humanTokens(n int64) string {
	f := float64(n)
	switch {
	case f >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", f/1_000_000_000)
	case f >= 1_000_000:
		return fmt.Sprintf("%.1fM", f/1_000_000)
	case f >= 1_000:
		return fmt.Sprintf("%.1fk", f/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func padRight(s string, n int) string {
	if lipgloss.Width(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-lipgloss.Width(s))
}

func clip(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

var (
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	nameStyle     = lipgloss.NewStyle().Bold(true)
	subtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	costStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	valueStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	planStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	resetStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	noteStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("248")).Italic(true)
	histStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)
