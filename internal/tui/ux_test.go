package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/models"
)

func assertFits(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	assert.LessOrEqual(t, len(lines), h, "view exceeds terminal height")
	for i, line := range lines {
		assert.LessOrEqual(t, ansi.StringWidth(line), w, "line %d exceeds terminal width: %s", i, line)
	}
}

func TestWorkspaceResponsiveLayout(t *testing.T) {
	h := newHarness(t)
	d := h.app.screens[scrDashboard].(*dashboardScreen)
	require.Contains(t, d.preview.View(), "Lead Intake", "initial selection must populate the preview")
	for _, size := range [][2]int{{160, 48}, {100, 30}, {80, 30}, {80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			h.dispatch(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			w, height := h.app.bodySize()
			assertFits(t, h.app.View(), size[0], size[1])
			assertFits(t, d.View(), w, height)
			assert.Contains(t, h.app.View(), "Lead Intake")
			assert.Contains(t, h.app.View(), "? help", "help must survive footer truncation")
			h.keys("tab")
			assert.Contains(t, h.app.View(), "Workflow details")
			h.keys("esc")
			assert.Equal(t, scrDashboard, h.app.active)
			assert.Equal(t, 0, d.focus)
		})
	}
	h.dispatch(tea.WindowSizeMsg{Width: 140, Height: 40})
	h.keys("e", "enter")
	for _, size := range [][2]int{{140, 40}, {80, 30}, {80, 24}, {40, 12}} {
		h.dispatch(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		w, height := h.app.bodySize()
		assertFits(t, h.app.current().View(), w, height)
		assert.Contains(t, h.app.View(), "HTTP Request", "selected node must stay visible")
		h.keys("tab")
		assert.Contains(t, h.app.View(), "ECONNREFUSED", "output must be reachable in a small terminal")
		h.keys("esc")
	}
	h.dispatch(tea.WindowSizeMsg{Width: 30, Height: 8})
	assertFits(t, h.app.View(), 30, 8)
	assert.Contains(t, h.app.View(), "Terminal too small")
}

func TestDashboardFiltersFocusAndSelection(t *testing.T) {
	h := newHarness(t)
	d := h.app.screens[scrDashboard].(*dashboardScreen)
	h.keys("down")
	assert.Contains(t, d.preview.View(), "Order Sync")
	// A refresh reorders the list; keep the selected ID and its preview together.
	ws := append([]models.Workflow(nil), d.all...)
	for i := range ws {
		if ws[i].ID == "w1" {
			ws[i].UpdatedAt = time.Now()
		}
	}
	h.dispatch(workflowsMsg{workflows: ws})
	wf, _ := d.SelectedWorkflow()
	assert.Equal(t, "w1", wf.ID)
	assert.Contains(t, d.preview.View(), "Order Sync")
	h.keys("f")
	require.Len(t, d.visible, 1)
	assert.Equal(t, "w2", d.visible[0].ID)
	assert.Contains(t, d.preview.View(), "Lead Intake")
	h.keys("esc")
	assert.Len(t, d.visible, 2)
	h.keys("/", "bill", "enter")
	require.Len(t, d.visible, 1)
	h.keys("esc")
	assert.Empty(t, d.filter.Value())
	assert.Len(t, d.visible, 2)
	h.keys("o")
	assert.True(t, d.sortName)
	assert.Equal(t, "Lead Intake", d.visible[0].Name)
}

func TestActionMenuSearchNavigationAndConfirmation(t *testing.T) {
	h := newHarness(t)
	open := func() { h.dispatch(tea.KeyMsg{Type: tea.KeyCtrlP}) }
	open()
	require.NotNil(t, h.app.menu)
	h.typeText("delete")
	require.Len(t, h.app.menu.visible, 1)
	h.keys("enter")
	assert.Nil(t, h.app.menu)
	assert.True(t, h.app.confirm.Active(), "menu must use the same confirmation flow as the shortcut")
	assert.Empty(t, h.client().deleted)
	h.keys("esc")
	open()
	h.typeText("q")
	assert.False(t, h.quit(), "typing in the menu must not execute shortcuts")
	h.keys("esc")
	open()
	h.typeText("go settings")
	require.Len(t, h.app.menu.visible, 1)
	h.keys("enter")
	assert.Equal(t, scrSettings, h.app.active)
	h.keys("]")
	assert.Equal(t, scrDashboard, h.app.active, "view cycling wraps")
	h.keys("[")
	assert.Equal(t, scrSettings, h.app.active)
}

func TestHelpAndMessagesAreScrollable(t *testing.T) {
	h := newHarness(t)
	h.dispatch(tea.WindowSizeMsg{Width: 60, Height: 16})
	h.keys("?")
	assert.True(t, h.app.showHelp)
	h.keys("G")
	assert.True(t, h.app.showHelp)
	assert.Contains(t, h.app.View(), "confirmation")
	assertFits(t, h.app.View(), 60, 16)
	h.keys("esc")
	text := strings.Repeat("A long connection error that should remain readable. ", 30) + "END_OF_ERROR"
	cmd := h.app.update(statusMsg{text: text, isErr: true})
	assert.Nil(t, cmd, "errors must not disappear on a timeout")
	h.keys("!")
	h.keys("G")
	assert.Contains(t, h.app.View(), "END_OF_ERROR")
	assertFits(t, h.app.View(), 60, 16)
}

func TestInspectorRetryLoadsNewExecutionWithoutExtraHistory(t *testing.T) {
	h := newHarness(t)
	h.keys("e", "enter")
	h.client().details["new-101"] = &models.Execution{ID: "new-101", WorkflowID: "w2", Status: models.StatusRunning}
	history := len(h.app.history)
	h.keys("R")
	d := h.app.current().(*executionDetailScreen)
	require.NotNil(t, d.exec, "retry must load the new inspector even when already on that screen")
	assert.Equal(t, "new-101", d.exec.ID)
	assert.Equal(t, history, len(h.app.history))
	assert.Contains(t, h.client().gotExec, "new-101")
	h.keys("esc")
	assert.Equal(t, scrExecutions, h.app.active)
}

func TestInspectorRefreshPreservesRunAndScroll(t *testing.T) {
	h := newHarness(t)
	h.keys("e", "enter")
	d := h.app.current().(*executionDetailScreen)
	ex := &models.Execution{ID: "101", WorkflowID: "w2", Data: &models.ExecutionData{}}
	ex.Data.ResultData.RunData = map[string][]models.TaskData{
		"Loop": {
			{StartTime: 1},
			{StartTime: 2, Error: &models.ExecutionError{Message: strings.Repeat("failure\n", 80)}},
		},
	}
	h.dispatch(executionDetailMsg{id: "101", exec: ex})
	assert.Equal(t, 1, d.nodes.Cursor())
	h.keys("tab")
	d.output.SetYOffset(12)
	h.dispatch(executionDetailMsg{id: "101", exec: ex})
	assert.Equal(t, 1, d.nodes.Cursor(), "keep the selected run, not just its node name")
	assert.Equal(t, 12, d.output.YOffset, "polling must not jump to the top")
	h.dispatch(tea.WindowSizeMsg{Width: 120, Height: 32})
	assert.Equal(t, 12, d.output.YOffset, "resizing must preserve reading position")
}

func TestWebhookPresetClearsPreviousTarget(t *testing.T) {
	h := newHarness(t)
	h.keys("w")
	w := h.app.current().(*webhookScreen)
	w.test = true
	h.keys("esc", "esc", "down", "w")
	assert.Empty(t, w.path.Value(), "a workflow without a webhook must not retain the previous target")
	assert.True(t, w.test, "changing the target preserves the explicitly selected test mode")
	assert.Equal(t, fieldPath, w.focus)
	h.keys("ctrl+s")
	assert.Empty(t, h.client().hooks)
	assert.Contains(t, h.app.status, "Enter a webhook path")
}

func TestRefreshFailureKeepsWorkflowsVisible(t *testing.T) {
	h := newHarness(t)
	h.dispatch(workflowsMsg{err: errors.New("connection refused")})
	assert.Contains(t, h.app.View(), "Lead Intake")
	assert.Contains(t, h.app.View(), "Couldn't load workflows")
	h.keys("!")
	assert.Contains(t, h.app.View(), "connection refused")
	h.keys("esc")
	h.dispatch(workflowsMsg{workflows: h.client().workflows})
	assert.False(t, h.app.statusErr)
	assert.Contains(t, h.app.status, "connection restored")
}

func TestDeepExecutionHistoryDoesNotDisappearOnPoll(t *testing.T) {
	h := newHarness(t)
	h.keys("2")
	e := h.app.current().(*executionsScreen)
	rows := make([]models.Execution, 300)
	for i := range rows {
		rows[i] = models.Execution{ID: fmt.Sprint(1000 - i), Status: models.StatusSuccess}
	}
	h.dispatch(executionsMsg{reqID: e.reqID, page: models.Page[models.Execution]{Data: rows, NextCursor: "older"}})
	e.table.SetCursor(275)
	assert.Nil(t, e.Refresh(true))
	assert.Len(t, e.rows, 300)
	assert.Equal(t, 275, e.table.Cursor())
	assert.Contains(t, h.app.View(), "auto-refresh paused")
	h.keys("r")
	assert.Len(t, e.rows, 2, "explicit refresh returns to the newest executions")
}

func TestFirstLoadFailureExplainsInDetailsPane(t *testing.T) {
	h := newHarness(t)
	d := h.app.screens[scrDashboard].(*dashboardScreen)
	d.loaded, d.all, d.visible = false, nil, nil
	d.rebuild()
	h.dispatch(workflowsMsg{err: &api.Error{StatusCode: 401, Method: "GET", Path: "/workflows"}})
	view := h.app.View()
	assert.Contains(t, view, "Can't reach n8n")
	assert.Contains(t, view, "n8n rejected the API key")
	assert.Contains(t, h.app.status, "Loading workflows failed")
}

func TestCredentialsErrorsStayWithinTheirSections(t *testing.T) {
	h := newHarness(t)
	h.client().credErr = &api.Error{StatusCode: 404}
	h.keys("5")
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}} {
		h.dispatch(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.keys("tab")
		assert.Contains(t, h.app.View(), "API_TOKEN", "variables remain accessible when credentials fail")
		w, height := h.app.bodySize()
		assertFits(t, h.app.current().View(), w, height)
		assertFits(t, h.app.View(), size[0], size[1])
		h.keys("tab", "G")
		assert.Contains(t, h.app.View(), "works without it.", "the full credentials error can be scrolled")
	}

	h.dispatch(tea.WindowSizeMsg{Width: 40, Height: 12})
	h.dispatch(credentialsMsg{
		creds:  []models.Credential{{Name: "Slack Bot"}},
		varErr: errors.New(strings.Repeat("Long variable error. ", 20) + "END_OF_VARIABLE_ERROR"),
	})
	h.keys("tab", "G")
	assert.Contains(t, h.app.View(), "Slack Bot", "variable errors must not obscure credentials either")
	assert.Contains(t, h.app.View(), "END_OF_VARIABLE_ERROR")
	assertFits(t, h.app.current().View(), h.app.width, h.app.height-headerHeight-footerHeight)
}
