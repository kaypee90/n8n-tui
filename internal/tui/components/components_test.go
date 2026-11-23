package components

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestTableLayoutAndScrolling(t *testing.T) {
	tb := NewTable([]Column{{Title: "A", Width: 4}, {Title: "B", Width: 4, Flex: 1}})
	tb.SetSize(20, 4)                        // header + 3 rows
	assert.Equal(t, []int{4, 13}, tb.widths) // two cells reserved for the selection marker

	var rows [][]string
	for i := 0; i < 10; i++ {
		rows = append(rows, []string{string(rune('a' + i)), strings.Repeat("x", 30)})
	}
	tb.SetRows(rows)
	assert.Equal(t, 0, tb.Cursor())

	tb.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	assert.True(t, tb.AtBottom())
	assert.Equal(t, 7, tb.offset)

	view := tb.View()
	lines := strings.Split(view, "\n")
	assert.Len(t, lines, 4)
	for _, l := range lines[1:] {
		assert.LessOrEqual(t, ansi.StringWidth(l), 20)
	}
	assert.Contains(t, view, "…", "long cells are truncated")

	assert.False(t, tb.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}), "d is left for actions")
	tb.SetRows(nil)
	assert.Equal(t, -1, tb.Cursor())
	assert.Contains(t, tb.View(), "Nothing to show.")
}

func TestTableStyledCellsKeepWidth(t *testing.T) {
	tb := NewTable([]Column{{Title: "S", Width: 6}})
	tb.SetSize(6, 3)
	tb.CellStyle = func(int, int, string) lipgloss.Style { return lipgloss.NewStyle().Bold(true) }
	tb.SetRows([][]string{{"✔ success"}, {"✖ error"}})
	for _, l := range strings.Split(tb.View(), "\n") {
		assert.Equal(t, 6, ansi.StringWidth(l))
	}
}

func TestTableHidesOptionalColumnsAndKeepsSelectionVisible(t *testing.T) {
	tb := NewTable([]Column{
		{Title: "STATE", Width: 8}, {Title: "NAME", Width: 18, Flex: 1},
		{Title: "TAGS", Width: 14, Priority: 1}, {Title: "ID", Width: 12, Priority: 2},
	})
	tb.SetRows([][]string{{"active", "日本語ワークフロー", "important", "w1"}, {"inactive", "Second", "", "w2"}})
	for _, width := range []int{100, 42, 30, 12, 2, 1} {
		tb.SetSize(width, 3)
		view := tb.View()
		for _, line := range strings.Split(view, "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(line), width)
		}
		if width >= 30 {
			assert.Contains(t, view, "▸", "selection must not rely on color")
			assert.Contains(t, view, "日本語")
		}
		if width == 30 {
			assert.NotContains(t, view, "TAGS")
			assert.NotContains(t, view, "ID")
		}
	}
	// Returning to a wide terminal restores the optional columns.
	tb.SetSize(100, 3)
	assert.Contains(t, tb.View(), "TAGS")
	assert.Contains(t, tb.View(), "ID")
}

func TestFormat(t *testing.T) {
	now := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "just now", RelTime(now.Add(-time.Second), now))
	assert.Equal(t, "5m ago", RelTime(now.Add(-5*time.Minute), now))
	assert.Equal(t, "3d ago", RelTime(now.Add(-72*time.Hour), now))
	assert.Equal(t, "—", RelTime(time.Time{}, now))

	assert.Equal(t, "—", Duration(0))
	assert.Equal(t, "850ms", Duration(850*time.Millisecond))
	assert.Equal(t, "12.3s", Duration(12300*time.Millisecond))
	assert.Equal(t, "4m05s", Duration(4*time.Minute+5*time.Second))
	assert.Equal(t, "2h03m", Duration(2*time.Hour+3*time.Minute))
}

func TestConfirm(t *testing.T) {
	var c Confirm
	ran := false
	c.Open("Delete?", func() tea.Msg { ran = true; return nil })
	assert.True(t, c.Active())
	assert.Nil(t, c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}))
	assert.True(t, c.Active(), "unrelated keys keep the dialog open")
	cmd := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	assert.False(t, c.Active())
	cmd()
	assert.True(t, ran)
	assert.Contains(t, c.View(40, 10), "Delete?")
}
