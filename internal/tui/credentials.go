package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

type credentialsMsg struct {
	creds   []models.Credential
	credErr error
	vars    []models.Variable
	varErr  error
}

type credentialsScreen struct {
	s *Session

	creds   []models.Credential
	vars    []models.Variable
	credErr error
	varErr  error
	section int // 0 = credentials, 1 = variables
	reveal  bool
	loading bool
	loaded  bool

	credTable *components.Table
	varTable  *components.Table

	width, height int
}

func newCredentials(s *Session) *credentialsScreen {
	ct := components.NewTable([]components.Column{
		{Title: "NAME", Width: 16, Flex: 2},
		{Title: "TYPE", Width: 16, Flex: 1},
		{Title: "UPDATED", Width: 10, Priority: 1},
		{Title: "ID", Width: 16, Priority: 2},
	})
	ct.Empty = "No credentials."
	ct.CellStyle = func(_, col int, _ string) lipgloss.Style {
		if col >= 2 {
			return styleMuted
		}
		return lipgloss.NewStyle()
	}
	vt := components.NewTable([]components.Column{
		{Title: "KEY", Width: 16, Flex: 1},
		{Title: "VALUE", Width: 16, Flex: 2},
		{Title: "TYPE", Width: 8, Priority: 1},
	})
	vt.Empty = "No variables."
	vt.Focused = false
	return &credentialsScreen{s: s, credTable: ct, varTable: vt}
}

func (c *credentialsScreen) Init() tea.Cmd { return c.load() }

// Refresh: credentials rarely change, so only manual refreshes reload.
func (c *credentialsScreen) Refresh(auto bool) tea.Cmd {
	if auto {
		return nil
	}
	return c.load()
}

func (c *credentialsScreen) load() tea.Cmd {
	if c.loading {
		return nil
	}
	c.loading = true
	return c.s.do(func(ctx context.Context, cl Client) tea.Msg {
		var m credentialsMsg
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); m.creds, m.credErr = cl.ListCredentials(ctx) }()
		go func() { defer wg.Done(); m.vars, m.varErr = cl.ListVariables(ctx) }()
		wg.Wait()
		return m
	})
}

func (c *credentialsScreen) Capturing() bool { return false }

func (c *credentialsScreen) Keys() []keyHelp {
	reveal := "reveal values"
	if c.reveal {
		reveal = "mask values"
	}
	return []keyHelp{{"tab", "switch section"}, {"v", reveal}, {"r", "refresh"}}
}

func (c *credentialsScreen) SetSize(w, h int) {
	c.width, c.height = w, h
	// two sections, each: title line + table; 1 spacer line
	avail := max(h-3, 4)
	credH := avail / 2
	c.credTable.SetSize(w, credH)
	c.varTable.SetSize(w, avail-credH)
}

func (c *credentialsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case credentialsMsg:
		c.loading = false
		c.loaded = true
		c.creds, c.credErr = msg.creds, msg.credErr
		c.vars, c.varErr = msg.vars, msg.varErr
		sort.Slice(c.creds, func(i, j int) bool { return strings.ToLower(c.creds[i].Name) < strings.ToLower(c.creds[j].Name) })
		sort.Slice(c.vars, func(i, j int) bool { return c.vars[i].Key < c.vars[j].Key })
		c.rebuild()
		return nil

	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab":
			c.section = 1 - c.section
			c.credTable.Focused = c.section == 0
			c.varTable.Focused = c.section == 1
			return nil
		case "v":
			c.reveal = !c.reveal
			c.rebuild()
			return nil
		}
		if c.section == 0 {
			c.credTable.Update(msg)
		} else {
			c.varTable.Update(msg)
		}
	}
	return nil
}

func (c *credentialsScreen) rebuild() {
	now := c.s.Now()
	rows := make([][]string, len(c.creds))
	for i, cr := range c.creds {
		rows[i] = []string{cr.Name, cr.Type, components.RelTime(cr.UpdatedAt, now), cr.ID}
	}
	c.credTable.SetRows(rows)

	vrows := make([][]string, len(c.vars))
	for i, v := range c.vars {
		val := config.MaskSecret(v.Value)
		if c.reveal {
			val = v.Value
		}
		vrows[i] = []string{v.Key, val, v.Type}
	}
	c.varTable.SetRows(vrows)
}

func sectionError(what string, err error) string {
	switch {
	case api.IsUnauthorized(err):
		return styleError.Render("✖ unauthorized — check the API key")
	case api.IsUnsupported(err):
		return styleWarn.Render(fmt.Sprintf("%s are not available on this instance (requires a newer n8n version, the right API key scope, or a license)", what))
	default:
		return styleError.Render("✖ " + err.Error())
	}
}

func (c *credentialsScreen) View() string {
	if !c.loaded {
		return styleMuted.Render("Loading credentials and variables…")
	}
	title := func(s string, active bool, n int) string {
		st := styleMuted
		if active {
			st = styleKey
		}
		return st.Render(fmt.Sprintf("%s (%d)", s, n))
	}

	var b strings.Builder
	b.WriteString(title("Credentials", c.section == 0, len(c.creds)) +
		styleMuted.Render("  · secret data is never retrieved") + "\n")
	if c.credErr != nil {
		b.WriteString(sectionError("Credentials", c.credErr) + "\n")
	} else {
		b.WriteString(c.credTable.View() + "\n")
	}
	b.WriteString("\n")
	hint := "  · values masked (v to reveal)"
	if c.reveal {
		hint = "  · values revealed (v to mask)"
	}
	b.WriteString(title("Variables", c.section == 1, len(c.vars)) + styleMuted.Render(hint) + "\n")
	if c.varErr != nil {
		b.WriteString(sectionError("Variables", c.varErr))
	} else {
		b.WriteString(c.varTable.View())
	}
	return b.String()
}
