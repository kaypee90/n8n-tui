package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/models"
)

var (
	colorAccent  = lipgloss.AdaptiveColor{Light: "#EA4B71", Dark: "#FF6D8A"} // n8n pink
	colorMuted   = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#7A7A7A"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	colorError   = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	colorWarn    = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	colorInfo    = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#58A6FF"}

	styleTitle     = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleTab       = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)
	styleTabActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(colorAccent).Padding(0, 1)
	styleMuted     = lipgloss.NewStyle().Foreground(colorMuted)
	styleBold      = lipgloss.NewStyle().Bold(true)
	styleError     = lipgloss.NewStyle().Foreground(colorError)
	styleSuccess   = lipgloss.NewStyle().Foreground(colorSuccess)
	styleWarn      = lipgloss.NewStyle().Foreground(colorWarn)
	styleInfo      = lipgloss.NewStyle().Foreground(colorInfo)
	styleKey       = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleLabel     = lipgloss.NewStyle().Foreground(colorMuted).Width(16)
	styleSection   = lipgloss.NewStyle().Bold(true).Foreground(colorInfo).MarginTop(1)
	stylePanel     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorMuted).Padding(0, 1)
	stylePanelFoc  = stylePanel.BorderForeground(colorAccent)
)

// statusIcon returns a glyph and color for an execution status.
func statusIcon(s models.ExecutionStatus) (string, lipgloss.Style) {
	switch s {
	case models.StatusSuccess:
		return "✔", styleSuccess
	case models.StatusError, models.StatusCrashed:
		return "✖", styleError
	case models.StatusRunning, models.StatusNew:
		return "▶", styleInfo
	case models.StatusWaiting:
		return "⏸", styleWarn
	case models.StatusCanceled:
		return "⊘", styleMuted
	default:
		return "?", styleMuted
	}
}

func statusLabel(s models.ExecutionStatus) string {
	icon, _ := statusIcon(s)
	return icon + " " + string(s)
}

func statusStyle(s models.ExecutionStatus) lipgloss.Style {
	_, st := statusIcon(s)
	return st
}

func activeLabel(active bool) string {
	if active {
		return "● active"
	}
	return "○ inactive"
}
