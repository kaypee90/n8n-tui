package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInitialModel(t *testing.T) {
	m := model{}

	if cmd := m.Init(); cmd != nil {
		t.Fatal("Init() returned a command; want nil")
	}
	if view := m.View(); !strings.Contains(view, "n8n TUI") {
		t.Fatalf("View() = %q, want n8n TUI text", view)
	}
}

func TestQuitKeys(t *testing.T) {
	m := model{}

	tests := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{name: "q", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}},
		{name: "ctrl+c", msg: tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := m.Update(tt.msg)
			if cmd == nil {
				t.Fatalf("Update() did not return a quit command for %q", tt.name)
			}
		})
	}
}
