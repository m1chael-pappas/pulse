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

	"github.com/m1chael-pappas/pulse/internal/providers"
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
	inner := contentWidth(width)

	lines := buildLines(snap, inner)
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)

	// lipgloss.Width() sets content+padding; the border adds 2 more cols
	// on top. We subtract 2 here so the rendered tile's visible width
	// matches the `width` argument exactly — keeps adjacent tiles in a
	// row aligned and stops the right edge spilling past the terminal.
	style := border.Width(width - 2)
	if height > 0 {
		// Height parameter follows the same convention: subtract 2 for
		// the top + bottom border rows.
		style = style.Height(height - 2)
	}
	return style.Render(body)
}

// NaturalHeight returns the line count Render would produce for a snapshot
// at the given width, including the two border rows. Callers in a flex
// layout can use this to size each tile to its content.
func NaturalHeight(snap providers.Snapshot, width int) int {
	return len(buildLines(snap, contentWidth(width))) + 2 // top + bottom border
}

// contentWidth returns the usable inner width inside the tile's borders
// and padding. Render() and NaturalHeight() must agree on this, so it
// lives in one place.
func contentWidth(width int) int {
	// 2 cols border + 2 cols horizontal padding = 4 cols of chrome.
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	return inner
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
		if len(snap.Events) > 0 {
			lines = append(lines, eventLines(snap.Events, inner)...)
		}
		for _, section := range snap.Sections {
			lines = append(lines, "", labelStyle.Render(section.Title))
			for _, row := range section.Rows {
				lines = append(lines, breakdownLine(row, inner))
			}
		}
		if len(snap.Stats) > 0 {
			lines = append(lines, "", statsGrid(snap.Stats, inner))
		}
		if len(snap.History) > 0 {
			lines = append(lines, "", labelStyle.Render("Last 30 days"))
			lines = append(lines, histogram(snap.History, inner))
		}
		if len(snap.Breakdown) > 0 {
			lines = append(lines, "", labelStyle.Render("Where it came from (API equiv.)"))
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
		return formatUSD(v)
	case providers.UnitTokens:
		return humanTokens(int64(v))
	case providers.UnitPercent:
		return fmt.Sprintf("%.0f%%", v)
	case providers.UnitGiB:
		return fmt.Sprintf("%.1f GiB", v)
	case providers.UnitCount:
		return fmt.Sprintf("%.0f", v)
	case "age":
		return humanDuration(time.Duration(v) * time.Second)
	case "status":
		// Status-pill rows render their state in the label itself; the
		// value column would just be noise. Suppress it.
		return ""
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// humanDuration formats a duration the way GitHub does in the UI:
// "now", "12m", "3h", "5d", "2mo".
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	default:
		return fmt.Sprintf("%dmo", int(d.Hours())/(24*30))
	}
}

// formatUSD adds thousand separators: 1234.5 → "$1,234.50".
func formatUSD(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	cents := int64((v-float64(whole))*100 + 0.5)
	if cents == 100 {
		whole++
		cents = 0
	}
	// Group digits in threes from the right.
	in := fmt.Sprintf("%d", whole)
	n := len(in)
	if n <= 3 {
		if neg {
			return fmt.Sprintf("-$%s.%02d", in, cents)
		}
		return fmt.Sprintf("$%s.%02d", in, cents)
	}
	var b strings.Builder
	first := n % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(in[:first])
	for i := first; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(in[i : i+3])
	}
	if neg {
		return fmt.Sprintf("-$%s.%02d", b.String(), cents)
	}
	return fmt.Sprintf("$%s.%02d", b.String(), cents)
}

// eventLines renders upcoming-event rows. Layout:
//
//	Today
//	  09:00 — Standup (15m)
//	  14:00 — 1:1 Alex
//	Tomorrow
//	  all day — Public holiday
func eventLines(events []providers.Event, inner int) []string {
	if len(events) == 0 {
		return nil
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var lines []string
	var lastBucket string
	for _, e := range events {
		bucket := dayBucket(e.Start, today)
		if bucket != lastBucket {
			lines = append(lines, "", labelStyle.Render(bucket))
			lastBucket = bucket
		}
		lines = append(lines, eventRow(e, inner))
	}
	return lines
}

func dayBucket(at, today time.Time) string {
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	switch days := int(day.Sub(today).Hours() / 24); {
	case days < 0:
		return "Earlier"
	case days == 0:
		return "Today"
	case days == 1:
		return "Tomorrow"
	case days < 7:
		return at.Format("Monday")
	default:
		return at.Format("Mon Jan 2")
	}
}

func eventRow(e providers.Event, inner int) string {
	timeStr := "all day"
	if !e.AllDay {
		timeStr = e.Start.Format("15:04")
	}
	title := e.Title
	if title == "" {
		title = "(no title)"
	}
	left := fmt.Sprintf("  %s  %s", valueStyle.Render(timeStr), title)
	if lipgloss.Width(left) > inner {
		left = clip(left, inner)
	}
	return left
}

func breakdownLine(b providers.BreakdownEntry, inner int) string {
	val := formatValue(b.Unit, b.Value)
	// Reserve room for value + separator. If a glyph is present it eats
	// 2 cols (glyph + trailing space) of the label budget — pre-colored
	// and emitted verbatim so the green/red ANSI survives.
	valW := lipgloss.Width(val)
	sep := 0
	if val != "" {
		sep = 1
	}
	glyphPrefix := ""
	glyphW := 0
	if b.Glyph != "" {
		glyphPrefix = b.Glyph + " "
		glyphW = lipgloss.Width(glyphPrefix)
	}
	labelW := inner - valW - sep - glyphW
	if labelW < 1 {
		labelW = 1
	}
	clipped := clip(b.Label, labelW)
	padded := padRight(clipped, labelW)
	label := glyphPrefix + labelStyle.Render(padded)
	if b.URL != "" {
		label = osc8Link(b.URL, label)
	}
	if val == "" {
		return label
	}
	return label + " " + val
}

// osc8Link wraps body in an OSC 8 hyperlink escape. Modern terminals
// (iTerm2, Kitty, WezTerm, recent Terminal.app and VS Code) render the
// body as cmd-clickable, opening URL in the user's browser. Older
// terminals silently ignore the escapes and just show the body text.
func osc8Link(url, body string) string {
	const esc = "\x1b]8;;"
	const st = "\x1b\\"
	return esc + url + st + body + esc + st
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

// histogram renders a braille-based area chart spanning the full inner
// width. Braille cells encode 2 horizontal × 4 vertical dot positions
// each, so a `rows`-tall chart gives `rows*4` levels of vertical
// resolution and 2× horizontal resolution per cell — roughly 8× the
// detail of an equivalently sized block-character chart. Each cell is
// colored by its peak intensity to give a heatmap feel that mirrors how
// modern sparkline UIs (Grafana, Datadog) read at a glance.
func histogram(points []providers.HistoryPoint, inner int) string {
	if len(points) == 0 || inner < 2 {
		return ""
	}
	const rows = 4
	const dotRowsPerCell = 4
	totalDots := rows * dotRowsPerCell

	maxV := 0.0
	for _, p := range points {
		if p.Value > maxV {
			maxV = p.Value
		}
	}

	// Each cell holds 2 dot-columns. We project the N data points across
	// inner*2 dot-columns by uniform sampling — that way 30 days stretches
	// smoothly across whatever the tile width happens to be without
	// visible aliasing in the rendered shape.
	totalDotCols := inner * 2
	heights := make([]int, totalDotCols)
	for col := 0; col < totalDotCols; col++ {
		idx := col * len(points) / totalDotCols
		if idx >= len(points) {
			idx = len(points) - 1
		}
		v := points[idx].Value
		h := 0
		if maxV > 0 && v > 0 {
			h = int(v/maxV*float64(totalDots) + 0.5)
			if h < 1 {
				h = 1
			}
		}
		heights[col] = h
	}

	rowStrs := make([]string, rows)
	for r := 0; r < rows; r++ {
		// Row 0 is the top of the chart; the bottom row is `rows-1`. Each
		// row covers a 4-dot band; minDot is the dot index at the bottom
		// of this row's band, measured from the chart's baseline.
		rowFromBottom := rows - 1 - r
		minDot := rowFromBottom * dotRowsPerCell
		var b strings.Builder
		for cell := 0; cell < inner; cell++ {
			left := clampDot(heights[cell*2]-minDot, dotRowsPerCell)
			right := clampDot(heights[cell*2+1]-minDot, dotRowsPerCell)
			peak := left
			if right > peak {
				peak = right
			}
			if peak == 0 && left == 0 && right == 0 {
				b.WriteRune(' ')
				continue
			}
			rune := brailleCell(left, right)
			// Color by absolute height (rowFromBottom * 4 + peak) so the
			// gradient stays consistent across rows of the same column.
			absHeight := rowFromBottom*dotRowsPerCell + peak
			ratio := float64(absHeight) / float64(totalDots)
			b.WriteString(heatmap(ratio).Render(string(rune)))
		}
		rowStrs[r] = b.String()
	}
	axis := histogramAxisBraille(points, inner)
	if axis != "" {
		rowStrs = append(rowStrs, axisStyle.Render(axis))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rowStrs...)
}

func clampDot(v, max int) int {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// brailleCell maps a (left, right) fill count in dots (0–4 each) to the
// matching braille glyph. Braille codepoints (U+2800–U+28FF) carry one
// bit per dot, with this visual layout:
//
//	dot1 dot4     left-top
//	dot2 dot5
//	dot3 dot6
//	dot7 dot8     left-bottom (added in 8-dot braille)
//
// We fill from the bottom up, so 1 dot in the left column lights dot7,
// 2 dots lights dot7+dot3, etc.
func brailleCell(left, right int) rune {
	const base = rune(0x2800)
	// Bottom-up offsets within the braille bit pattern.
	leftMasks := [4]rune{0x40, 0x04, 0x02, 0x01}  // dot7, dot3, dot2, dot1
	rightMasks := [4]rune{0x80, 0x20, 0x10, 0x08} // dot8, dot6, dot5, dot4
	var mask rune
	for i := 0; i < left; i++ {
		mask |= leftMasks[i]
	}
	for i := 0; i < right; i++ {
		mask |= rightMasks[i]
	}
	return base + mask
}

// heatmap returns a lipgloss style for a value in [0,1]: low values use a
// cool blue, mid values are green, high values shift through amber to
// red. Five stops keep the gradient legible on a 256-color terminal.
func heatmap(ratio float64) lipgloss.Style {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	color := lipgloss.Color("33") // blue
	switch {
	case ratio >= 0.85:
		color = lipgloss.Color("196") // red
	case ratio >= 0.65:
		color = lipgloss.Color("214") // amber
	case ratio >= 0.4:
		color = lipgloss.Color("82") // green
	case ratio >= 0.2:
		color = lipgloss.Color("44") // teal
	}
	return lipgloss.NewStyle().Foreground(color)
}

// histogramAxisBraille places day-of-week initials evenly under the
// braille cells. Each cell holds 2 points, so we sample every other
// point — that keeps the axis from being a wall of letters and aligns
// each label with the cell that contains its data.
func histogramAxisBraille(points []providers.HistoryPoint, inner int) string {
	if len(points) == 0 || inner < 1 {
		return ""
	}
	var b strings.Builder
	for cell := 0; cell < inner; cell++ {
		idx := (cell*2 + 1) * len(points) / (inner * 2)
		if idx >= len(points) {
			idx = len(points) - 1
		}
		// Show an initial only at the rightmost dot-column of each cell
		// so labels stay aligned with the data underneath.
		if cell == 0 || idx != prevAxisIdx(points, cell-1, inner) {
			b.WriteString(dayInitial(points[idx].At))
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func prevAxisIdx(points []providers.HistoryPoint, cell, inner int) int {
	idx := (cell*2 + 1) * len(points) / (inner * 2)
	if idx >= len(points) {
		idx = len(points) - 1
	}
	return idx
}

// histogramAxis renders a row of day-of-week initials (M/T/W/T/F/S/S)
// aligned to each bar column. When columns are 2+ wide, the initial sits
// in the left cell of each column with a space on the right so labels
// don't bleed into each other.
func histogramAxis(points []providers.HistoryPoint, perPoint, extra int) string {
	if len(points) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range points {
		width := perPoint
		if i < extra {
			width++
		}
		initial := dayInitial(p.At)
		if width <= 1 {
			b.WriteString(initial)
			continue
		}
		b.WriteString(initial)
		for j := 1; j < width; j++ {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func dayInitial(t time.Time) string {
	switch t.Weekday() {
	case time.Monday:
		return "M"
	case time.Tuesday:
		return "T"
	case time.Wednesday:
		return "W"
	case time.Thursday:
		return "T"
	case time.Friday:
		return "F"
	case time.Saturday:
		return "S"
	case time.Sunday:
		return "S"
	}
	return " "
}

func renderBar(w providers.Window, inner int) string {
	// Bars span the full inner width — anything narrower wastes the room
	// the tile already reserves.
	width := inner
	if width < 8 {
		width = 8
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
	color := lipgloss.Color("82")
	switch {
	case ratio >= 0.9:
		color = lipgloss.Color("196")
	case ratio >= 0.7:
		color = lipgloss.Color("214")
	}
	return roundedBar(ratio, width, color)
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
	return roundedBar(done, width, lipgloss.Color("63"))
}

// roundedBar renders a progress bar with semicircle end caps (◖ … ◗),
// solid blocks in the middle, and a dotted track for the empty portion.
// The caps make the bar read as a discrete pill even on terminals with
// no anti-aliasing — much more "modern" than the flat block / ░ combo
// most TUIs ship.
func roundedBar(ratio float64, width int, color lipgloss.Color) string {
	if width < 2 {
		return ""
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}

	fillStyle := lipgloss.NewStyle().Foreground(color)
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	var fill string
	switch filled {
	case 0:
		fill = ""
	case 1:
		fill = fillStyle.Render("◖")
	case 2:
		fill = fillStyle.Render("◖◗")
	default:
		fill = fillStyle.Render("◖" + strings.Repeat("█", filled-2) + "◗")
	}
	empty := emptyStyle.Render(strings.Repeat("░", width-filled))
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
	axisStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)
