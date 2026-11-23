package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

const (
	executionsPageSize = 50
	maxAutoRefreshRows = 250
)

type (
	executionsMsg struct {
		reqID  int
		page   models.Page[models.Execution]
		append bool
		err    error
	}
	executionDeletedMsg struct {
		id  string
		err error
	}
	executionRetriedMsg struct {
		id   string
		exec *models.Execution
		err  error
	}
)

type executionsScreen struct {
	s *Session

	rows        []models.Execution
	table       *components.Table
	workflowID  string
	statusIdx   int // -1 = all, otherwise index into models.FilterableStatuses
	nextCursor  string
	reqID       int
	loading     bool
	loadingMore bool
	loaded      bool
	err         error

	width, height int
}

func newExecutions(s *Session) *executionsScreen {
	t := components.NewTable([]components.Column{
		{Title: "ID", Width: 8},
		{Title: "STATUS", Width: 11},
		{Title: "WORKFLOW", Width: 16, Flex: 1},
		{Title: "MODE", Width: 9, Priority: 2},
		{Title: "STARTED", Width: 19, Priority: 3},
		{Title: "DURATION", Width: 9, Priority: 1},
	})
	t.Empty = "No executions found."
	e := &executionsScreen{s: s, table: t, statusIdx: -1}
	t.CellStyle = func(row, col int, _ string) lipgloss.Style {
		switch col {
		case 1:
			if row < len(e.rows) {
				return statusStyle(e.rows[row].EffectiveStatus())
			}
		case 0, 3, 4:
			return styleMuted
		}
		return lipgloss.NewStyle()
	}
	return e
}

func (e *executionsScreen) Init() tea.Cmd { return e.load(false) }

func (e *executionsScreen) Refresh(auto bool) tea.Cmd { return e.load(auto) }

func (e *executionsScreen) Capturing() bool { return false }

func (e *executionsScreen) Keys() []keyHelp {
	keys := []keyHelp{{"enter", "inspect"}, {"f", "status filter"}}
	if e.workflowID != "" {
		keys = append(keys, keyHelp{"a", "all workflows"})
	}
	return append(keys, keyHelp{"R", "retry"}, keyHelp{"d", "delete"}, keyHelp{"r", "refresh"})
}

func (e *executionsScreen) SetSize(w, h int) {
	e.width, e.height = w, h
	e.table.SetSize(w-4, h-3)
}

// SetWorkflowFilter restricts the list to one workflow ("" for all).
func (e *executionsScreen) SetWorkflowFilter(id string) tea.Cmd {
	if id == e.workflowID && e.loaded {
		return nil
	}
	e.workflowID = id
	e.reset()
	return e.load(false)
}

func (e *executionsScreen) reset() {
	e.rows = nil
	e.nextCursor = ""
	e.loaded = false
	e.loading = false
	e.loadingMore = false
	e.err = nil
	e.table.SetRows(nil)
	e.table.SetCursor(0)
}

func (e *executionsScreen) filter() api.ExecutionFilter {
	f := api.ExecutionFilter{WorkflowID: e.workflowID, Limit: executionsPageSize}
	if e.statusIdx >= 0 {
		f.Status = models.FilterableStatuses[e.statusIdx]
	}
	return f
}

// load fetches the first page. Auto refreshes retain up to one API page of
// loaded rows. Once browsing deeper history, pause polling rather than silently
// dropping older pages and moving the user's selection back to the present.
func (e *executionsScreen) load(auto bool) tea.Cmd {
	if auto && (e.loading || e.loadingMore || len(e.rows) > maxAutoRefreshRows) {
		return nil
	}
	e.reqID++ // supersedes any in-flight page request
	e.loading = true
	e.loadingMore = false
	f := e.filter()
	if auto && len(e.rows) > f.Limit {
		f.Limit = min(len(e.rows), maxAutoRefreshRows)
	}
	id := e.reqID
	return e.s.do(func(ctx context.Context, c Client) tea.Msg {
		page, err := c.ListExecutions(ctx, f)
		return executionsMsg{reqID: id, page: page, err: err}
	})
}

func (e *executionsScreen) loadMore() tea.Cmd {
	if e.nextCursor == "" || e.loadingMore || e.loading {
		return nil
	}
	e.loadingMore = true
	f := e.filter()
	f.Cursor = e.nextCursor
	id := e.reqID
	return e.s.do(func(ctx context.Context, c Client) tea.Msg {
		page, err := c.ListExecutions(ctx, f)
		return executionsMsg{reqID: id, page: page, append: true, err: err}
	})
}

func (e *executionsScreen) selected() (models.Execution, bool) {
	i := e.table.Cursor()
	if i < 0 || i >= len(e.rows) {
		return models.Execution{}, false
	}
	return e.rows[i], true
}

// SelectedWorkflow implements workflowSelector so `w` presets the webhook
// view for the selected execution's workflow.
func (e *executionsScreen) SelectedWorkflow() (models.Workflow, bool) {
	ex, ok := e.selected()
	if !ok {
		return models.Workflow{}, false
	}
	return e.s.Workflow(ex.WorkflowID)
}

func (e *executionsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case executionsMsg:
		if msg.reqID != e.reqID {
			return nil // filter changed since the request was sent
		}
		if msg.append {
			e.loadingMore = false
		} else {
			e.loading = false
		}
		if msg.err != nil {
			e.err = msg.err
			return errStatus("Loading executions", msg.err)
		}
		recovered := e.err != nil
		e.err = nil
		e.loaded = true
		sel, hadSel := e.selected()
		if msg.append {
			e.rows = append(e.rows, msg.page.Data...)
		} else {
			e.rows = msg.page.Data
		}
		e.nextCursor = msg.page.NextCursor
		e.rebuild()
		if hadSel {
			for i, r := range e.rows {
				if r.ID == sel.ID {
					e.table.SetCursor(i)
					break
				}
			}
		}
		if recovered {
			return statusCmd("Executions refreshed · connection restored", false)
		}
		return nil

	case executionDeletedMsg:
		if msg.err != nil {
			return errStatus("Deleting execution", msg.err)
		}
		for i, r := range e.rows {
			if r.ID == msg.id {
				e.rows = append(e.rows[:i], e.rows[i+1:]...)
				break
			}
		}
		e.rebuild()
		return statusCmd("Deleted execution #"+msg.id, false)

	case executionRetriedMsg:
		if msg.err != nil {
			if api.IsUnsupported(msg.err) {
				return statusCmd("Retry is not supported by this n8n version (needs ≥ 1.83)", true)
			}
			return errStatus("Retrying execution", msg.err)
		}
		text := "Retried execution #" + msg.id
		if msg.exec != nil && msg.exec.ID != "" {
			text += " → #" + msg.exec.ID
		}
		return tea.Batch(statusCmd(text, false), e.load(true))

	case workflowsMsg:
		e.rebuild() // workflow names may have changed
		return nil

	case tea.KeyMsg:
		if e.table.Update(msg) {
			if e.table.AtBottom() {
				return e.loadMore()
			}
			return nil
		}
		return e.handleKey(msg)
	}
	return nil
}

func (e *executionsScreen) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if ex, ok := e.selected(); ok {
			return send(openExecutionMsg{id: ex.ID})
		}
	case "f":
		e.statusIdx++
		if e.statusIdx >= len(models.FilterableStatuses) {
			e.statusIdx = -1
		}
		e.reset()
		return e.load(false)
	case "a":
		if e.workflowID != "" {
			return e.SetWorkflowFilter("")
		}
	case "R":
		if ex, ok := e.selected(); ok {
			return retryCmd(e.s, ex)
		}
	case "d":
		if ex, ok := e.selected(); ok {
			id := ex.ID
			return send(confirmMsg{
				prompt: fmt.Sprintf("Delete execution #%s of %q?", id, e.s.WorkflowName(ex.WorkflowID)),
				onYes: e.s.do(func(ctx context.Context, c Client) tea.Msg {
					return executionDeletedMsg{id: id, err: c.DeleteExecution(ctx, id)}
				}),
			})
		}
	}
	return nil
}

func retryCmd(s *Session, ex models.Execution) tea.Cmd {
	st := ex.EffectiveStatus()
	if st != models.StatusError && st != models.StatusCrashed && st != models.StatusCanceled {
		return statusCmd("Only failed or canceled executions can be retried", true)
	}
	id := ex.ID
	return tea.Batch(statusCmd("Retrying execution #"+id+"…", false),
		s.do(func(ctx context.Context, c Client) tea.Msg {
			res, err := c.RetryExecution(ctx, id, true)
			return executionRetriedMsg{id: id, exec: res, err: err}
		}))
}

func (e *executionsScreen) rebuild() {
	now := e.s.Now()
	rows := make([][]string, len(e.rows))
	for i, ex := range e.rows {
		started := "—"
		if ex.StartedAt != nil {
			started = components.Timestamp(*ex.StartedAt)
		}
		rows[i] = []string{
			ex.ID,
			statusLabel(ex.EffectiveStatus()),
			e.s.WorkflowName(ex.WorkflowID),
			ex.Mode,
			started,
			components.Duration(ex.Duration(now)),
		}
	}
	e.table.SetRows(rows)
}

func (e *executionsScreen) View() string {
	parts := []string{}
	if e.workflowID != "" {
		parts = append(parts, "workflow: "+styleBold.Render(e.s.WorkflowName(e.workflowID)))
	} else {
		parts = append(parts, "all workflows")
	}
	status := "all"
	if e.statusIdx >= 0 {
		status = string(models.FilterableStatuses[e.statusIdx])
	}
	parts = append(parts, "status: "+styleBold.Render(status))
	switch {
	case e.err != nil:
		parts = append(parts, styleError.Render("✖ Refresh failed · r retry · ! details"))
	case !e.loaded:
		parts = append(parts, "loading…")
	default:
		n := fmt.Sprintf("%d loaded", len(e.rows))
		if e.nextCursor != "" {
			n += " (scroll for more)"
		}
		if e.loadingMore {
			n += " · loading more…"
		} else if e.loading {
			n += " · refreshing…"
		}
		parts = append(parts, n)
		if len(e.rows) > maxAutoRefreshRows {
			parts = append(parts, "history: auto-refresh paused · r newest")
		}
	}
	header := styleMuted.Render(strings.Join(parts, styleMuted.Render(" · ")))
	e.table.Empty = "No executions match. f status · a all workflows · r refresh"
	if !e.loaded {
		e.table.Empty = "Loading executions…"
		if e.err != nil {
			e.table.Empty = "Unable to load executions. r retry · h health"
		}
	}
	position := fmt.Sprintf("%d/%d · ↑/↓ navigate · enter inspect", e.table.Cursor()+1, len(e.rows))
	if e.nextCursor != "" {
		position += " · scroll for more"
	}
	if len(e.rows) > maxAutoRefreshRows {
		position = fmt.Sprintf("%d/%d · history: auto-refresh paused · r newest", e.table.Cursor()+1, len(e.rows))
	}
	return panel("Executions", position, line(header, e.width-4)+"\n"+e.table.View(), e.width, e.height, true)
}
