package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

type healthMsg struct{ health models.Health }

type healthScreen struct {
	s       *Session
	health  *models.Health
	loading bool

	width, height int
}

func newHealth(s *Session) *healthScreen { return &healthScreen{s: s} }

func (h *healthScreen) Init() tea.Cmd        { return h.load() }
func (h *healthScreen) Refresh(bool) tea.Cmd { return h.load() }
func (h *healthScreen) Capturing() bool      { return false }
func (h *healthScreen) Keys() []keyHelp      { return []keyHelp{{"r", "check again"}} }

func (h *healthScreen) About() string {
	return "Checks each part of the connection separately: is n8n up, is its database ready, and is your API key accepted. Start here when something isn't working."
}
func (h *healthScreen) SetSize(w, ht int) { h.width, h.height = w, ht }

func (h *healthScreen) load() tea.Cmd {
	if h.loading {
		return nil
	}
	h.loading = true
	return h.s.do(func(ctx context.Context, c Client) tea.Msg {
		return healthMsg{health: c.Health(ctx)}
	})
}

func (h *healthScreen) Update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(healthMsg); ok {
		h.loading = false
		h.health = &m.health
	}
	return nil
}

func check(ok bool, good, bad string) string {
	if ok {
		return styleSuccess.Render("✔ " + good)
	}
	return styleError.Render("✖ " + bad)
}

func (h *healthScreen) View() string {
	if h.health == nil {
		return styleMuted.Render("Checking instance health…")
	}
	hl := h.health
	in := h.s.Instance
	row := func(label, value string) string { return styleLabel.Render(label) + value }

	version := hl.Version
	if version == "" {
		version = styleMuted.Render("unknown (/rest/settings not reachable)")
	}
	mode := hl.ExecutionMode
	if mode == "" {
		mode = styleMuted.Render("unknown")
	} else if mode == "queue" {
		mode = "queue " + styleMuted.Render("(workers via Redis)")
	}

	db := styleMuted.Render("unknown (/healthz/readiness not available)")
	if hl.ReadyKnown {
		db = check(hl.Ready, "connected & migrated", "not ready (database or Redis unavailable)")
	}

	api := check(hl.APIReachable, "authenticated", "failed")
	if !hl.APIReachable && hl.APIError != "" {
		api += styleMuted.Render("  " + truncate(hl.APIError, max(h.width-40, 20)))
	}

	count := func(n int, more bool) string {
		s := fmt.Sprintf("%d", n)
		if more {
			s += "+"
		}
		return s
	}

	process := check(hl.Healthy, "healthy", "unreachable")
	if !hl.Healthy && hl.HealthError != "" {
		process += styleMuted.Render("  " + truncate(hl.HealthError, max(h.width-40, 20)))
	}

	lines := []string{
		styleSection.UnsetMarginTop().Render("Instance"),
		row("Name", styleBold.Render(in.Name)),
		row("URL", in.BaseURL()),
		row("Auth", in.AuthMethod()),
		row("n8n version", version),
		"",
		styleSection.UnsetMarginTop().Render("Status"),
		row("Process", process),
		row("Database", db),
		row("Public API", api),
		row("Latency", components.Duration(hl.Latency)),
		"",
		styleSection.UnsetMarginTop().Render("Queue"),
		row("Execution mode", mode),
		row("Running", count(hl.Running, hl.MoreRunning)),
		row("Waiting", count(hl.Waiting, hl.MoreWaiting)),
		"",
		styleMuted.Render("Checked " + components.Timestamp(hl.CheckedAt) + " · auto-refreshes every " + h.s.Config.RefreshInterval.String()),
	}
	if h.loading {
		lines[len(lines)-1] += styleMuted.Render(" · checking…")
	}
	return strings.Join(lines, "\n")
}
