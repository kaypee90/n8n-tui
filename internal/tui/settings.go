package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

type settingsScreen struct {
	s     *Session
	table *components.Table

	width, height int
}

func newSettings(s *Session) *settingsScreen {
	t := components.NewTable([]components.Column{
		{Title: " ", Width: 1},
		{Title: "NAME", Width: 12, Flex: 1},
		{Title: "URL", Width: 20, Flex: 2},
		{Title: "AUTH", Width: 20, Priority: 1},
		{Title: "API KEY", Width: 14, Priority: 2},
	})
	t.CellStyle = func(_, col int, v string) lipgloss.Style {
		if col == 0 && v != "" {
			return styleSuccess
		}
		if col == 4 {
			return styleMuted
		}
		return lipgloss.NewStyle()
	}
	st := &settingsScreen{s: s, table: t}
	st.rebuild()
	return st
}

func (st *settingsScreen) Init() tea.Cmd        { return nil }
func (st *settingsScreen) Refresh(bool) tea.Cmd { return nil }
func (st *settingsScreen) Capturing() bool      { return false }
func (st *settingsScreen) Keys() []keyHelp {
	return []keyHelp{{"enter", "connect to selected instance"}}
}

func (st *settingsScreen) About() string {
	return "The n8n instances from your config file. Select one and press enter to switch to it. Edit the config file to add or change instances."
}

func (st *settingsScreen) SetSize(w, h int) {
	st.width, st.height = w, h
	st.table.SetSize(w, min(len(st.s.Config.Instances)+1, max(h-10, 3)))
}

func (st *settingsScreen) rebuild() {
	rows := make([][]string, len(st.s.Config.Instances))
	cursor := 0
	for i, in := range st.s.Config.Instances {
		mark := ""
		if in.Name == st.s.Instance.Name {
			mark = "●"
			cursor = i
		}
		rows[i] = []string{mark, in.Name, in.BaseURL(), in.AuthMethod(), config.MaskSecret(in.APIKey)}
	}
	st.table.SetRows(rows)
	st.table.SetCursor(cursor)
}

func (st *settingsScreen) Update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if st.table.Update(k) {
		return nil
	}
	if k.String() == "enter" {
		i := st.table.Cursor()
		if i >= 0 && i < len(st.s.Config.Instances) {
			in := st.s.Config.Instances[i]
			if in.Name == st.s.Instance.Name {
				return statusCmd("Already connected to "+in.Name, false)
			}
			return send(switchInstanceMsg{name: in.Name})
		}
	}
	return nil
}

func (st *settingsScreen) View() string {
	cfg := st.s.Config
	path := cfg.Path
	if path == "" {
		path = "(none — using N8N_URL / N8N_API_KEY environment variables)"
	}
	row := func(label, value string) string { return styleLabel.Render(label) + value }

	lines := []string{
		styleSection.UnsetMarginTop().Render("Instances"),
		st.table.View(),
		"",
		styleSection.UnsetMarginTop().Render("Configuration"),
		wrap(row("Config file", path), st.width),
		row("Default", cfg.DefaultInstance),
		row("Refresh", cfg.RefreshInterval.String()),
		row("Timeout", cfg.Timeout.String()),
	}
	if wh := st.s.Instance.WebhookURL; wh != "" {
		lines = append(lines, row("Webhook URL", wh))
	}
	for _, w := range cfg.Warnings {
		lines = append(lines, styleWarn.Render("⚠ "+w))
	}
	editPath := cfg.Path
	if editPath == "" {
		editPath = config.DefaultPath()
	}
	lines = append(lines, "", styleMuted.Render(wrap(
		"To add or change instances, edit "+editPath+" (or run `n8n-tui init`) and restart n8n-tui. "+
			"Write secrets as ${ENV_VAR} so they stay out of the file.", st.width)))
	return strings.Join(lines, "\n")
}
