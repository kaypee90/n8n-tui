package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

const (
	maxOutputItems = 50
	maxOutputBytes = 256 << 10
	maxStackLines  = 15
	summaryLines   = 4
)

type executionDetailMsg struct {
	id   string
	exec *models.Execution
	err  error
}

type executionDetailScreen struct {
	s  *Session
	id string

	exec    *models.Execution
	runs    []models.NodeRun
	nodes   *components.Table
	output  viewport.Model
	focus   int // 0 = nodes, 1 = output
	loading bool
	err     error
	shownIx int

	width, height int
}

func newExecutionDetail(s *Session, id string) *executionDetailScreen {
	t := components.NewTable([]components.Column{
		{Title: " ", Width: 1},
		{Title: "NODE", Width: 14, Flex: 1},
		{Title: "TIME", Width: 7, Priority: 1},
		{Title: "ITEMS", Width: 5, Priority: 2},
	})
	t.Empty = "No node data."
	d := &executionDetailScreen{s: s, id: id, nodes: t, output: viewport.New(0, 0), shownIx: -1}
	t.CellStyle = func(row, col int, _ string) lipgloss.Style {
		if col == 0 && row < len(d.runs) {
			return statusStyle(d.runs[row].Status)
		}
		if col >= 2 {
			return styleMuted
		}
		return lipgloss.NewStyle()
	}
	return d
}

func (d *executionDetailScreen) Init() tea.Cmd { return d.load() }

// Refresh reloads; automatic refreshes only happen while the execution is
// still running.
func (d *executionDetailScreen) Refresh(auto bool) tea.Cmd {
	if auto && d.exec != nil && d.exec.EffectiveStatus().IsTerminal() {
		return nil
	}
	return d.load()
}

func (d *executionDetailScreen) load() tea.Cmd {
	if d.loading {
		return nil
	}
	d.loading = true
	id := d.id
	return d.s.do(func(ctx context.Context, c Client) tea.Msg {
		ex, err := c.GetExecution(ctx, id, true)
		return executionDetailMsg{id: id, exec: ex, err: err}
	})
}

func (d *executionDetailScreen) Capturing() bool { return false }

func (d *executionDetailScreen) Keys() []keyHelp {
	return []keyHelp{{"tab", "switch pane"}, {"↑/↓", "select/scroll"}, {"R", "retry"}, {"esc", "back"}}
}

func (d *executionDetailScreen) SetSize(w, h int) {
	d.width, d.height = w, h
	d.layout()
}

func (d *executionDetailScreen) layout() {
	p := splitPanes(d.width, d.height-d.summaryHeight(), 38)
	d.nodes.SetSize(p.lw-4, max(p.lh-2, 2))
	d.output.Width, d.output.Height = max(p.rw-4, 1), max(p.rh-2, 1)
	offset := d.output.YOffset
	d.shownIx = -1
	d.syncOutput()
	d.output.SetYOffset(offset)
}

func (d *executionDetailScreen) summaryHeight() int {
	return min(summaryLines, max(d.height-5, 1))
}

func (d *executionDetailScreen) Back() bool {
	if d.focus == 1 {
		d.focus = 0
		d.nodes.Focused = true
		return true
	}
	return false
}

// SelectedWorkflow implements workflowSelector.
func (d *executionDetailScreen) SelectedWorkflow() (models.Workflow, bool) {
	if d.exec == nil {
		return models.Workflow{}, false
	}
	return d.s.Workflow(d.exec.WorkflowID)
}

func (d *executionDetailScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case executionDetailMsg:
		if msg.id != d.id {
			return nil
		}
		d.loading = false
		if msg.err != nil {
			d.err = msg.err
			return errStatus("Loading execution #"+d.id, msg.err)
		}
		d.err = nil
		prevNode := ""
		prevRun := -1
		offset := d.output.YOffset
		if i := d.nodes.Cursor(); i >= 0 && i < len(d.runs) {
			prevNode = d.runs[i].Node
			prevRun = d.runs[i].RunIndex
		}
		d.exec = msg.exec
		d.runs = msg.exec.NodeRuns()
		d.rebuild()
		d.selectInitial(prevNode, prevRun)
		d.shownIx = -1
		d.syncOutput()
		if i := d.nodes.Cursor(); i >= 0 && d.runs[i].Node == prevNode && d.runs[i].RunIndex == prevRun {
			d.output.SetYOffset(offset)
		}
		return nil

	case executionRetriedMsg:
		if msg.id == d.id && msg.err == nil && msg.exec != nil && msg.exec.ID != "" {
			return send(openExecutionMsg{id: msg.exec.ID})
		}
		return nil

	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab", "enter":
			d.focus = 1 - d.focus
			d.nodes.Focused = d.focus == 0
			return nil
		case "R":
			if d.exec != nil {
				return retryCmd(d.s, *d.exec)
			}
			return nil
		}
		if d.focus == 0 {
			if d.nodes.Update(msg) {
				d.syncOutput()
			}
			return nil
		}
		var cmd tea.Cmd
		d.output, cmd = d.output.Update(msg)
		return cmd
	}
	return nil
}

// selectInitial keeps the previously selected node, or jumps to the first
// failed node so errors are visible immediately.
func (d *executionDetailScreen) selectInitial(prev string, run int) {
	for i, r := range d.runs {
		if prev != "" && r.Node == prev && r.RunIndex == run {
			d.nodes.SetCursor(i)
			return
		}
	}
	for i, r := range d.runs {
		if r.Error != nil {
			d.nodes.SetCursor(i)
			return
		}
	}
	d.nodes.SetCursor(0)
}

func (d *executionDetailScreen) rebuild() {
	rows := make([][]string, len(d.runs))
	for i, r := range d.runs {
		icon, _ := statusIcon(r.Status)
		name := r.Node
		if r.RunIndex > 0 {
			name += fmt.Sprintf(" (run %d)", r.RunIndex+1)
		}
		rows[i] = []string{icon, name, components.Duration(r.Duration), fmt.Sprintf("%d", r.Items)}
	}
	d.nodes.SetRows(rows)
}

func (d *executionDetailScreen) syncOutput() {
	i := d.nodes.Cursor()
	if i == d.shownIx {
		return
	}
	d.shownIx = i
	if i < 0 || i >= len(d.runs) {
		d.output.SetContent(styleMuted.Render(d.emptyOutputText()))
		return
	}
	d.output.SetContent(wrap(renderNodeRun(d.runs[i], d.output.Width), d.output.Width))
	d.output.GotoTop()
}

func (d *executionDetailScreen) emptyOutputText() string {
	switch {
	case d.exec == nil:
		return "Loading…"
	case d.exec.Data == nil:
		return "This execution has no stored data (check EXECUTIONS_DATA_SAVE_ON_* settings)."
	default:
		return "No node has run yet."
	}
}

func renderNodeRun(r models.NodeRun, width int) string {
	var b strings.Builder
	icon, st := statusIcon(r.Status)
	b.WriteString(styleBold.Render(r.Node) + "  " + st.Render(icon+" "+string(r.Status)) + "\n")

	if r.Error != nil {
		b.WriteString(wrap(styleError.Render("Error: "+r.Error.Message), width) + "\n")
		if r.Error.Description != "" {
			b.WriteString(wrap(r.Error.Description, width) + "\n")
		}
		if r.Error.Stack != "" {
			lines := strings.Split(r.Error.Stack, "\n")
			if len(lines) > maxStackLines {
				lines = append(lines[:maxStackLines], "…")
			}
			b.WriteString(styleMuted.Render(strings.Join(lines, "\n")) + "\n")
		}
	}
	b.WriteString(styleMuted.Render(fmt.Sprintf("run %d · started %s · took %s · %d item(s)",
		r.RunIndex+1, components.Timestamp(r.StartedAt), components.Duration(r.Duration), r.Items)) + "\n")

	b.WriteString("\n" + styleSection.UnsetMarginTop().Render(fmt.Sprintf("Output (%d item(s))", r.Items)) + "\n")
	if len(r.Output) == 0 {
		b.WriteString(styleMuted.Render("(no output)"))
		return b.String()
	}
	written := 0
	for i, raw := range r.Output {
		if i >= maxOutputItems || written > maxOutputBytes {
			b.WriteString(styleMuted.Render(fmt.Sprintf("… %d more item(s) not shown", len(r.Output)-i)))
			break
		}
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			buf.Reset()
			buf.Write(raw)
		}
		b.WriteString(styleMuted.Render(fmt.Sprintf("[%d]", i)) + " " + buf.String() + "\n")
		written += buf.Len()
	}
	return b.String()
}

func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

func (d *executionDetailScreen) View() string {
	sh := d.summaryHeight()
	p := splitPanes(d.width, d.height-sh, 38)
	nodes := panel("Nodes", fmt.Sprintf("%d/%d · tab output", d.nodes.Cursor()+1, len(d.runs)), d.nodes.View(), p.lw, p.lh, d.focus == 0)
	out := panel("Output / errors", fmt.Sprintf("%.0f%% · tab nodes", d.output.ScrollPercent()*100), d.output.View(), p.rw, p.rh, d.focus == 1)
	return block(d.summaryView(), d.width, sh) + "\n" + p.join(nodes, out, d.focus)
}

func (d *executionDetailScreen) summaryView() string {
	lines := make([]string, 0, summaryLines)
	if d.exec == nil {
		if d.err != nil {
			lines = append(lines, styleError.Render("✖ "+d.err.Error()))
		} else {
			lines = append(lines, styleMuted.Render("Loading execution #"+d.id+"…"))
		}
	} else {
		ex := d.exec
		st := ex.EffectiveStatus()
		lines = append(lines,
			styleBold.Render("Execution #"+ex.ID)+"  "+statusStyle(st).Render(statusLabel(st))+
				styleMuted.Render("  ·  ")+d.s.WorkflowName(ex.WorkflowID))
		started := "—"
		if ex.StartedAt != nil {
			started = components.Timestamp(*ex.StartedAt)
		}
		meta := fmt.Sprintf("mode %s · started %s · duration %s · %d node run(s)",
			ex.Mode, started, components.Duration(ex.Duration(d.s.Now())), len(d.runs))
		if ex.RetryOf != "" {
			meta += " · retry of #" + ex.RetryOf
		}
		lines = append(lines, styleMuted.Render(meta))
		if ex.Data != nil && ex.Data.ResultData.Error != nil {
			e := ex.Data.ResultData.Error
			msg := e.Message
			if e.Node != nil && e.Node.Name != "" {
				msg = e.Node.Name + ": " + msg
			}
			lines = append(lines, styleError.Render("✖ "+truncate(msg, d.width-4)))
		}
		lines = append(lines, styleMuted.Render(d.s.Instance.BaseURL()+"/workflow/"+ex.WorkflowID+"/executions/"+ex.ID))
	}
	for len(lines) < summaryLines {
		lines = append(lines, "")
	}
	return strings.Join(lines[:summaryLines], "\n")
}
