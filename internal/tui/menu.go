package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

type menuAction struct {
	label, key string
	run        func() tea.Cmd // invoked on the update loop, never a command goroutine
}

type actionMenu struct {
	input   textinput.Model
	table   *components.Table
	actions []menuAction
	visible []menuAction
}

func (a *App) openMenu() tea.Cmd {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Type to find an action or view…"
	input.CharLimit = 100
	m := &actionMenu{input: input, table: components.NewTable([]components.Column{
		{Title: "ACTION", Width: 20, Flex: 1}, {Title: "KEY", Width: 10},
	})}
	m.table.Empty = "No matching actions. Backspace to edit."
	if !a.current().Capturing() {
		for _, k := range a.current().Keys() {
			key := k.key
			if key != "/" {
				key, _, _ = strings.Cut(key, "/")
			}
			msg, ok := shortcutKey(key)
			if !ok {
				continue
			}
			m.actions = append(m.actions, menuAction{k.desc, k.key, func() tea.Cmd { return a.handleKey(msg) }})
		}
	}
	for _, t := range tabs {
		m.actions = append(m.actions, menuAction{"Go to " + t.title, t.key, func() tea.Cmd { return a.navigate(t.id) }})
	}
	m.actions = append(m.actions,
		menuAction{"Keyboard help", "?", func() tea.Cmd { a.openHelp(false); return nil }},
		menuAction{"Show last message", "!", func() tea.Cmd { a.openHelp(true); return nil }},
		menuAction{"Quit n8n-tui", "q", func() tea.Cmd { return tea.Quit }},
	)
	a.menu = m
	m.filter()
	a.sizeMenu()
	return m.input.Focus()
}

func shortcutKey(key string) (tea.KeyMsg, bool) {
	types := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "tab": tea.KeyTab, "space": tea.KeySpace,
		"esc": tea.KeyEsc, "ctrl+s": tea.KeyCtrlS, "ctrl+n": tea.KeyCtrlN,
	}
	if t, ok := types[key]; ok {
		return tea.KeyMsg{Type: t}, true
	}
	if len(key) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}, true
	}
	return tea.KeyMsg{}, false
}

func (m *actionMenu) filter() {
	m.visible = nil
	var rows [][]string
	for _, action := range m.actions {
		match := true
		for _, word := range strings.Fields(strings.ToLower(m.input.Value())) {
			if !strings.Contains(strings.ToLower(action.label+" "+action.key), word) {
				match = false
			}
		}
		if match {
			m.visible = append(m.visible, action)
			rows = append(rows, []string{action.label, action.key})
		}
	}
	m.table.SetRows(rows)
	m.table.SetCursor(0)
}

func (a *App) sizeMenu() {
	if a.menu == nil {
		return
	}
	w, h := a.menuSize()
	a.menu.input.Width = max(w-7, 1) // include prompt and cursor in the pane width
	a.menu.table.SetSize(w-4, max(h-4, 2))
}

func (a *App) menuSize() (int, int) {
	w, h := a.bodySize()
	return min(w, 76), min(h, min(20, max(len(a.menu.visible)+5, 7)))
}

func (a *App) updateMenu(msg tea.Msg) tea.Cmd {
	m := a.menu
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "ctrl+p":
			a.menu = nil
			return nil
		case "enter":
			i := m.table.Cursor()
			if i >= 0 && i < len(m.visible) {
				a.menu = nil
				return m.visible[i].run()
			}
			return nil
		case "up", "down", "pgup", "pgdown":
			m.table.Update(k)
			return nil
		case "ctrl+n":
			m.table.Move(1)
			return nil
		case "ctrl+k":
			m.table.Move(-1)
			return nil
		}
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.filter()
		a.sizeMenu()
	}
	return cmd
}

func (a *App) menuView() string {
	w, h := a.bodySize()
	mw, mh := a.menuSize()
	m := a.menu
	content := m.input.View() + "\n\n" + m.table.View()
	box := panel("Actions · "+a.screenTitle(), fmt.Sprintf("%d matches · ↑/↓ select · enter run · esc close", len(m.visible)), content, mw, mh, true)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}
