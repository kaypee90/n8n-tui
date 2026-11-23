package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// line and block measure terminal cells, not bytes, and never wrap a row into
// the footer. Individual scrollable panes wrap their own content as needed.
func line(s string, width int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return ansi.Truncate(s, max(width, 0), "…")
}

func block(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			out[i] = line(lines[i], width)
		}
		out[i] += strings.Repeat(" ", max(width-ansi.StringWidth(out[i]), 0))
	}
	return strings.Join(out, "\n")
}

// panel keeps titles, focus and scroll position visible even without color.
// Width and height include the border; the content area is (w-4) × (h-2).
func panel(title, position, content string, w, h int, focused bool) string {
	if w < 4 || h < 3 {
		return block(content, w, h)
	}
	st := styleMuted
	if focused {
		st = styleKey
		title = "▸ " + title
	}
	edge := func(left, text, right string) string {
		if text != "" {
			text = " " + text + " "
		}
		text = line(text, w-2)
		return st.Render(left + text + strings.Repeat("─", max(w-2-ansi.StringWidth(text), 0)) + right)
	}
	rows := strings.Split(block(content, w-4, h-2), "\n")
	for i, row := range rows {
		rows[i] = st.Render("│") + " " + row + " " + st.Render("│")
	}
	return edge("╭", title, "╮") + "\n" + strings.Join(rows, "\n") + "\n" + edge("╰", position, "╯")
}

type paneLayout struct {
	lw, lh, rw, rh  int
	stacked, single bool
}

func splitPanes(w, h, leftPercent int) paneLayout {
	if w >= 100 {
		lw := w * leftPercent / 100
		return paneLayout{lw: lw, lh: h, rw: w - lw, rh: h}
	}
	if h >= 20 {
		lh := h * 2 / 5
		return paneLayout{lw: w, lh: lh, rw: w, rh: h - lh, stacked: true}
	}
	return paneLayout{lw: w, lh: h, rw: w, rh: h, single: true}
}

func (p paneLayout) join(left, right string, focus int) string {
	if p.single {
		if focus == 1 {
			return right
		}
		return left
	}
	if p.stacked {
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}
