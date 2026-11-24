package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
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
	errViews  [2]viewport.Model

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
	return &credentialsScreen{
		s: s, credTable: ct, varTable: vt,
		errViews: [2]viewport.Model{viewport.New(0, 0), viewport.New(0, 0)},
	}
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
	keys := []keyHelp{{"tab", "credentials ↔ variables"}}
	if c.section == 0 && c.credErr != nil || c.section == 1 && c.varErr != nil {
		keys = append(keys, keyHelp{"↑/↓", "scroll error"})
	}
	return append(keys, keyHelp{"v", reveal}, keyHelp{"r", "refresh"})
}

func (c *credentialsScreen) About() string {
	return "Credentials (names and types only: n8n never sends their secrets) and instance variables. Variable values are hidden until you press v."
}

func (c *credentialsScreen) SetSize(w, h int) {
	c.width, c.height = w, h
	// two sections, each: title line + table; 1 spacer line
	avail := max(h-3, 4)
	credH := avail / 2
	c.credTable.SetSize(w, credH)
	c.varTable.SetSize(w, avail-credH)
	for i, height := range []int{credH, avail - credH} {
		c.errViews[i].Width, c.errViews[i].Height = w, height
	}
	c.syncErrors()
}

func (c *credentialsScreen) syncErrors() {
	for i, section := range []struct {
		name string
		err  error
	}{{"Credentials", c.credErr}, {"Variables", c.varErr}} {
		content := ""
		if section.err != nil {
			content = sectionError(section.name, section.err, c.width)
		}
		c.errViews[i].SetContent(content)
	}
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
		c.syncErrors()
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
		if c.section == 0 && c.credErr != nil || c.section == 1 && c.varErr != nil {
			view := &c.errViews[c.section]
			switch msg.String() {
			case "g", "home":
				view.GotoTop()
			case "G", "end":
				view.GotoBottom()
			default:
				var cmd tea.Cmd
				*view, cmd = view.Update(msg)
				return cmd
			}
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

func sectionError(what string, err error, width int) string {
	switch {
	case api.IsUnsupported(err):
		need := "a newer n8n version or an API key with permission to list credentials"
		if what == "Variables" {
			need = "an n8n licence that includes variables, and an API key allowed to read them"
		}
		return styleWarn.Render(wrap(fmt.Sprintf("%s aren't available from this instance. This needs %s. "+
			"Everything else in n8n-tui works without it.", what, need), width))
	default:
		return styleError.Render(wrap("✖ "+describeError(err), width))
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
	b.WriteString(line(title("Credentials", c.section == 0, len(c.creds))+
		styleMuted.Render("  · secret data is never retrieved"), c.width) + "\n")
	creds := c.credTable.View()
	if c.credErr != nil {
		creds = c.errViews[0].View()
	}
	b.WriteString(block(creds, c.width, c.errViews[0].Height) + "\n\n")
	hint := "  · values masked (v to reveal)"
	if c.reveal {
		hint = "  · values revealed (v to mask)"
	}
	b.WriteString(line(title("Variables", c.section == 1, len(c.vars))+styleMuted.Render(hint), c.width) + "\n")
	vars := c.varTable.View()
	if c.varErr != nil {
		vars = c.errViews[1].View()
	}
	b.WriteString(block(vars, c.width, c.errViews[1].Height))
	return b.String()
}
