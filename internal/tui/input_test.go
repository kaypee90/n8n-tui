package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaypee90/n8n-tui/internal/models"
)

// Exercise Bubbles' real, unexported paste messages without reading or changing
// the user's clipboard. Unix clipboard helpers are replaced in a child process
// so the clipboard package discovers the fixtures during its initialization.
func TestClipboardInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("clipboard fixtures use Unix helper commands; supported on Linux/WSL and macOS")
	}
	if os.Getenv("N8N_TUI_TEST_CLIPBOARD_CHILD") != "1" {
		dir := t.TempDir()
		for _, name := range []string{"pbpaste", "xclip"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '%s' \"$N8N_TUI_TEST_CLIPBOARD_TEXT\"\n"), 0o700))
		}
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestClipboardInput$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), "PATH="+dir, "WAYLAND_DISPLAY=", "N8N_TUI_TEST_CLIPBOARD_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	t.Run("filter applies before enter", func(t *testing.T) {
		h := newHarness(t)
		h.keys("/")
		t.Setenv("N8N_TUI_TEST_CLIPBOARD_TEXT", "order")
		h.dispatch(textinput.Paste())
		d := h.app.current().(*dashboardScreen)
		require.Equal(t, "order", d.filter.Value())
		require.Len(t, d.visible, 1)
		selected, _ := d.SelectedWorkflow()
		assert.Equal(t, "w1", selected.ID)
		h.keys("enter", "e")
		assert.Equal(t, "w1", h.app.current().(*executionsScreen).workflowID)
	})

	t.Run("menu and inactive editors are isolated", func(t *testing.T) {
		h := newHarness(t)
		h.keys("3")
		w := h.app.current().(*webhookScreen)
		w.path.SetValue("leads")
		w.path.CursorEnd()
		h.dispatch(tea.KeyMsg{Type: tea.KeyCtrlP})
		t.Setenv("N8N_TUI_TEST_CLIPBOARD_TEXT", "go workflows")
		h.dispatch(textinput.Paste())
		require.NotNil(t, h.app.menu)
		assert.Equal(t, "go workflows", h.app.menu.input.Value())
		assert.Equal(t, "leads", w.path.Value(), "menu paste must not change the underlying path")
		h.keys("enter", "/")
		t.Setenv("N8N_TUI_TEST_CLIPBOARD_TEXT", "order")
		h.dispatch(textinput.Paste())
		assert.Equal(t, "order", h.app.current().(*dashboardScreen).filter.Value())
		assert.Equal(t, "leads", w.path.Value(), "paste must not reach a previously focused screen")
	})

	t.Run("hidden editors ignore queued paste", func(t *testing.T) {
		for _, mode := range []string{"help", "confirmation", "small terminal"} {
			t.Run(mode, func(t *testing.T) {
				h := newHarness(t)
				h.keys("3")
				w := h.app.current().(*webhookScreen)
				w.path.SetValue("leads")
				t.Setenv("N8N_TUI_TEST_CLIPBOARD_TEXT", "unwanted")
				pendingPaste := textinput.Paste()
				switch mode {
				case "help":
					h.app.openHelp(false)
				case "confirmation":
					h.dispatch(confirmMsg{prompt: "Confirm?"})
				case "small terminal":
					h.dispatch(tea.WindowSizeMsg{Width: 30, Height: 8})
				}
				h.dispatch(pendingPaste)
				assert.Equal(t, "leads", w.path.Value())
			})
		}
	})

	t.Run("payload editor still receives paste", func(t *testing.T) {
		h := newHarness(t)
		h.keys("w")
		w := h.app.current().(*webhookScreen)
		w.body.SetValue("")
		h.keys("enter") // start editing the body
		t.Setenv("N8N_TUI_TEST_CLIPBOARD_TEXT", `{"name":"Ada"}`)
		h.dispatch(textarea.Paste())
		assert.Equal(t, `{"name":"Ada"}`, w.body.Value())
	})
}

func TestWebhookPresetCyclePreservesMode(t *testing.T) {
	h := newHarness(t)
	h.keys("w")
	w := h.app.current().(*webhookScreen)
	// Select test mode using the same field and key as an interactive session.
	h.run(w.setFocus(fieldMode, false))
	h.keys("space")
	require.True(t, w.test)
	h.dispatch(tea.KeyMsg{Type: tea.KeyCtrlN})
	h.keys("ctrl+s")
	require.Len(t, h.client().hooks, 1)
	assert.True(t, h.client().hooks[0].Test)
	// Explicitly switching back to production must also survive preset changes.
	h.run(w.setFocus(fieldMode, false))
	h.keys("space")
	require.False(t, w.test)
	h.dispatch(tea.KeyMsg{Type: tea.KeyCtrlN})
	h.keys("ctrl+s")
	require.Len(t, h.client().hooks, 2)
	assert.False(t, h.client().hooks[1].Test)
}

func TestSmallTerminalBlocksHiddenActions(t *testing.T) {
	h := newHarness(t)
	h.dispatch(tea.WindowSizeMsg{Width: 30, Height: 8})
	h.keys("d", "y", "space", "t", "s")
	assert.False(t, h.app.confirm.Active())
	assert.Empty(t, h.client().deleted)
	assert.Empty(t, h.client().toggled)
	assert.Empty(t, h.client().hooks)
	assert.Equal(t, scrDashboard, h.app.active)

	// A confirmation opened before the resize must also wait until visible.
	h.dispatch(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.keys("d")
	require.True(t, h.app.confirm.Active())
	h.dispatch(tea.WindowSizeMsg{Width: 80, Height: 11})
	h.keys("y")
	assert.Empty(t, h.client().deleted)
	assert.True(t, h.app.confirm.Active())
	h.dispatch(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.keys("y")
	assert.Equal(t, []string{"w2"}, h.client().deleted)
}

func TestSmallTerminalKeepsQuitAvailable(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	} {
		t.Run(key.String(), func(t *testing.T) {
			h := newHarness(t)
			h.keys("w")
			w := h.app.current().(*webhookScreen)
			before := w.body.Value()
			h.dispatch(tea.WindowSizeMsg{Width: 39, Height: 24})
			h.dispatch(key)
			assert.True(t, h.quit(), "quit must work even when an editor has focus")
			assert.Equal(t, before, w.body.Value())
		})
	}
}

func TestDataResponsesStillReachScreensBehindMenu(t *testing.T) {
	h := newHarness(t)
	h.keys("w")
	w := h.app.current().(*webhookScreen)
	w.sending = true
	h.dispatch(tea.KeyMsg{Type: tea.KeyCtrlP})
	h.typeText("settings")
	h.dispatch(envelope{gen: h.app.session.gen, msg: webhookResultMsg{resp: &models.WebhookResponse{
		StatusCode: 200, Status: "200 OK", Body: []byte(`{"ok":true}`),
	}}})
	assert.False(t, w.sending)
	assert.Contains(t, w.response.View(), `"ok": true`)
	require.NotNil(t, h.app.menu)
	assert.Equal(t, "settings", h.app.menu.input.Value())
}
