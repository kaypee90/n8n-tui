// Package components holds reusable TUI widgets.
package components

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Column describes a table column. Width is the minimum width; Flex columns
// share any remaining horizontal space proportionally.
type Column struct {
	Title string
	Width int
	Flex  int
	// Optional columns are hidden on smaller terminals, highest priority first.
	// Zero keeps a column visible, shrinking it only as a last resort.
	Priority int
}

// Table is a scrollable, selectable table that supports styled cells.
//
// It exists instead of bubbles/table because that widget truncates cells
// without understanding ANSI sequences (breaking colored status cells) and its
// default key map binds d/f/space, which n8n-tui uses for actions.
type Table struct {
	cols    []Column
	widths  []int
	rows    [][]string
	cursor  int
	offset  int
	width   int
	height  int // rows of body, excluding header
	Empty   string
	Focused bool

	// CellStyle optionally styles a cell (foreground only; the selection
	// background is applied on top).
	CellStyle func(row, col int, value string) lipgloss.Style

	HeaderStyle   lipgloss.Style
	SelectedStyle lipgloss.Style
	BlurredStyle  lipgloss.Style
}

// NewTable creates a table with the given columns.
func NewTable(cols []Column) *Table {
	return &Table{
		cols:          cols,
		Empty:         "Nothing to show.",
		Focused:       true,
		HeaderStyle:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#A5A1FF"}),
		SelectedStyle: lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#E4E2FF", Dark: "#3B3A6B"}).Bold(true),
		BlurredStyle:  lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#EEEEEE", Dark: "#303030"}),
	}
}

// SetSize sets the outer size including the header line.
func (t *Table) SetSize(w, h int) {
	t.width = max(w, 0)
	t.height = max(h-1, 1)
	t.layout()
	t.clamp()
}

// SetRows replaces the table content, keeping the cursor in range.
func (t *Table) SetRows(rows [][]string) {
	t.rows = rows
	t.clamp()
}

// Len returns the number of rows.
func (t *Table) Len() int { return len(t.rows) }

// Cursor returns the selected row index (or -1 when empty).
func (t *Table) Cursor() int {
	if len(t.rows) == 0 {
		return -1
	}
	return t.cursor
}

// SetCursor moves the selection.
func (t *Table) SetCursor(i int) {
	t.cursor = i
	t.clamp()
}

// AtBottom reports whether the last row is selected.
func (t *Table) AtBottom() bool { return len(t.rows) > 0 && t.cursor == len(t.rows)-1 }

// Move moves the cursor by delta rows.
func (t *Table) Move(delta int) {
	t.cursor += delta
	t.clamp()
}

// Update handles navigation keys. It reports whether the key was consumed.
func (t *Table) Update(msg tea.Msg) bool {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}
	switch k.String() {
	case "up", "k":
		t.Move(-1)
	case "down", "j":
		t.Move(1)
	case "pgup", "ctrl+b":
		t.Move(-t.height)
	case "pgdown", "ctrl+f":
		t.Move(t.height)
	case "ctrl+u":
		t.Move(-t.height / 2)
	case "ctrl+d":
		t.Move(t.height / 2)
	case "home", "g":
		t.SetCursor(0)
	case "end", "G":
		t.SetCursor(len(t.rows) - 1)
	default:
		return false
	}
	return true
}

func (t *Table) clamp() {
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.height > 0 && t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	if maxOff := max(len(t.rows)-t.height, 0); t.offset > maxOff {
		t.offset = maxOff
	}
}

func (t *Table) layout() {
	t.widths = make([]int, len(t.cols))
	used, flex := 0, 0
	for i, c := range t.cols {
		t.widths[i] = c.Width
		used += c.Width
		flex += c.Flex
	}
	used += max(len(t.cols)-1, 0)  // single-space gaps
	available := max(t.width-2, 0) // selection marker, including in NO_COLOR terminals
	for used > available {
		hide := -1
		for i, c := range t.cols {
			if t.widths[i] > 0 && c.Priority > 0 && (hide < 0 || c.Priority > t.cols[hide].Priority) {
				hide = i
			}
		}
		if hide < 0 {
			break
		}
		used -= t.widths[hide] + 1
		flex -= t.cols[hide].Flex
		t.widths[hide] = 0
	}
	for used > available {
		widest := -1
		for i, width := range t.widths {
			if width > 1 && (widest < 0 || width > t.widths[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break
		}
		t.widths[widest]--
		used--
	}
	remaining := available - used
	if remaining <= 0 || flex == 0 {
		return
	}
	given := 0
	lastFlex := -1
	for i, c := range t.cols {
		if c.Flex == 0 || t.widths[i] == 0 {
			continue
		}
		extra := remaining * c.Flex / flex
		t.widths[i] += extra
		given += extra
		lastFlex = i
	}
	t.widths[lastFlex] += remaining - given
}

// View renders the table.
func (t *Table) View() string {
	if t.width <= 0 {
		return ""
	}
	var b strings.Builder
	var hdr []string
	for i, c := range t.cols {
		if t.widths[i] > 0 {
			hdr = append(hdr, fit(c.Title, t.widths[i]))
		}
	}
	b.WriteString(fit(t.HeaderStyle.Render("  "+strings.Join(hdr, " ")), t.width))

	if len(t.rows) == 0 {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Faint(true).Render(fit(t.Empty, t.width)))
		return b.String()
	}

	end := min(t.offset+t.height, len(t.rows))
	for r := t.offset; r < end; r++ {
		b.WriteString("\n")
		b.WriteString(t.renderRow(r))
	}
	return b.String()
}

func (t *Table) renderRow(r int) string {
	selected := r == t.cursor
	var rowStyle lipgloss.Style
	if selected {
		rowStyle = t.BlurredStyle
		if t.Focused {
			rowStyle = t.SelectedStyle
		}
	}

	var cells []string
	for c := range t.cols {
		if t.widths[c] == 0 {
			continue
		}
		val := ""
		if c < len(t.rows[r]) {
			val = t.rows[r][c]
		}
		st := lipgloss.NewStyle()
		if t.CellStyle != nil {
			st = t.CellStyle(r, c, val)
		}
		if selected {
			st = st.Inherit(rowStyle).Background(rowStyle.GetBackground())
		}
		cells = append(cells, st.Render(fit(val, t.widths[c])))
	}
	gap := " "
	if selected {
		gap = rowStyle.Render(" ")
	}
	marker := "  "
	if selected {
		marker = "▸ "
	}
	line := rowStyle.Render(marker) + strings.Join(cells, gap)
	if selected {
		if pad := t.width - lipgloss.Width(line); pad > 0 {
			line += rowStyle.Render(strings.Repeat(" ", pad))
		}
	}
	return fit(line, t.width)
}

// fit truncates or pads s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}
