package components

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Confirm is a modal yes/no prompt. The zero value is inactive.
type Confirm struct {
	Prompt string
	OnYes  tea.Cmd
	active bool
}

// Open shows the prompt.
func (c *Confirm) Open(prompt string, onYes tea.Cmd) {
	c.Prompt, c.OnYes, c.active = prompt, onYes, true
}

// Active reports whether the prompt is shown.
func (c *Confirm) Active() bool { return c.active }

// Update handles a key while active. It returns the command to run when the
// user confirmed (nil otherwise).
func (c *Confirm) Update(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		c.active = false
		return c.OnYes
	case "n", "N", "esc", "q", "enter":
		// enter cancels: destructive actions require an explicit "y".
		c.active = false
	}
	return nil
}

var confirmBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("#FF5F87")).
	Padding(1, 3)

// View renders the dialog centered in w×h.
func (c *Confirm) View(w, h int) string {
	box := confirmBox.Width(max(w-4, 1)).MaxWidth(w)
	if w > 80 {
		box = confirmBox.Width(76)
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.NewStyle().Bold(true).Width(max(min(w-12, 68), 1)).Render(c.Prompt),
		"",
		lipgloss.NewStyle().Faint(true).Render("y: confirm   enter/n/esc: cancel"),
	)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box.Render(body))
}
