package tui

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

type dashboardScreen struct {
	s *Session

	all      []models.Workflow
	visible  []models.Workflow
	table    *components.Table
	filter   textinput.Model
	filterOn bool
	loading  bool
	loaded   bool
	err      error
	pending  map[string]bool // workflow IDs with an in-flight action
	preview  viewport.Model
	shownID  string
	focus    int // 0 = workflows, 1 = preview
	status   int // 0 = all, 1 = active, 2 = inactive
	sortName bool

	width, height int
}

func newDashboard(s *Session) *dashboardScreen {
	t := components.NewTable([]components.Column{
		{Title: "STATUS", Width: 10},
		{Title: "NAME", Width: 20, Flex: 3},
		{Title: "TAGS", Width: 10, Flex: 1, Priority: 2},
		{Title: "TRIGGER", Width: 8, Priority: 3},
		{Title: "UPDATED", Width: 10, Priority: 1},
		{Title: "ID", Width: 16, Priority: 4},
	})
	t.Empty = "No workflows found."
	t.CellStyle = func(_, col int, v string) lipgloss.Style {
		if col == 0 {
			if strings.HasPrefix(v, "●") {
				return styleSuccess
			}
			if strings.HasPrefix(v, "…") {
				return styleWarn
			}
			return styleMuted
		}
		if col == 5 || col == 4 {
			return styleMuted
		}
		return lipgloss.NewStyle()
	}

	f := textinput.New()
	f.Prompt = "/ "
	f.Placeholder = "filter by name, tag or id"
	f.CharLimit = 100

	return &dashboardScreen{s: s, table: t, filter: f, preview: viewport.New(0, 0), pending: map[string]bool{}}
}

func (d *dashboardScreen) Init() tea.Cmd { return d.load() }

func (d *dashboardScreen) Refresh(bool) tea.Cmd { return d.load() }

func (d *dashboardScreen) load() tea.Cmd {
	if d.loading {
		return nil
	}
	d.loading = true
	return d.s.do(func(ctx context.Context, c Client) tea.Msg {
		ws, err := c.ListWorkflows(ctx)
		return workflowsMsg{workflows: ws, err: err}
	})
}

func (d *dashboardScreen) Capturing() bool { return d.filterOn }

func (d *dashboardScreen) Keys() []keyHelp {
	if d.filterOn {
		return []keyHelp{{"enter", "apply"}, {"esc", "clear filter"}}
	}
	return []keyHelp{
		{"enter", "executions"}, {"space", "activate/deactivate"}, {"t", "run (webhook)"}, {"/", "search"},
		{"tab", "details pane"}, {"f", "show all / active / inactive"}, {"o", "sort by name / last update"},
		{"w", "open in webhook form"}, {"d", "delete"}, {"r", "refresh"},
	}
}

func (d *dashboardScreen) About() string {
	return "Every workflow on the instance. Pick one to see its details on the right, its run history (enter), or to switch it on and off (space)."
}

func (d *dashboardScreen) SetSize(w, h int) {
	d.width, d.height = w, h
	d.layout()
}

func (d *dashboardScreen) layout() {
	p := splitPanes(d.width, d.height, 55)
	d.filter.Width = max(p.lw-7, 1) // panel padding, prompt and cursor cell
	d.table.SetSize(p.lw-4, max(p.lh-4, 2))
	d.preview.Width, d.preview.Height = max(p.rw-4, 1), max(p.rh-2, 1)
	d.syncPreview()
}

// Back consumes Escape locally before the app navigates away.
func (d *dashboardScreen) Back() bool {
	if d.focus == 1 {
		d.focus = 0
		d.table.Focused = true
		return true
	}
	if d.filter.Value() != "" || d.status != 0 {
		d.filter.SetValue("")
		d.status = 0
		d.applyFilter()
		return true
	}
	return false
}

// SelectedWorkflow implements workflowSelector.
func (d *dashboardScreen) SelectedWorkflow() (models.Workflow, bool) {
	i := d.table.Cursor()
	if i < 0 || i >= len(d.visible) {
		return models.Workflow{}, false
	}
	return d.visible[i], true
}

func (d *dashboardScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case workflowsMsg:
		d.loading = false
		recovered := d.err != nil && msg.err == nil
		d.err = msg.err
		if msg.err != nil {
			d.syncPreview() // the details pane explains the failure
			return errStatus("Loading workflows", msg.err)
		}
		d.loaded = true
		d.all = append([]models.Workflow(nil), msg.workflows...)
		d.applyFilter()
		if recovered {
			return statusCmd("Workflows refreshed · connection restored", false)
		}
		return nil

	case workflowUpdatedMsg:
		if msg.workflow != nil {
			delete(d.pending, msg.workflow.ID)
		}
		if msg.err != nil {
			d.pending = map[string]bool{}
			d.rebuild()
			return tea.Batch(errStatus("Updating workflow", msg.err), d.load())
		}
		for i := range d.all {
			if d.all[i].ID == msg.workflow.ID {
				d.all[i].Active = msg.workflow.Active
			}
		}
		if w, ok := d.s.workflows[msg.workflow.ID]; ok {
			w.Active = msg.workflow.Active
			d.s.workflows[w.ID] = w
		}
		d.applyFilter()
		state := "deactivated"
		if msg.workflow.Active {
			state = "activated"
		}
		return statusCmd(fmt.Sprintf("Workflow %q %s", msg.workflow.Name, state), false)

	case workflowDeletedMsg:
		delete(d.pending, msg.id)
		if msg.err != nil {
			d.rebuild()
			return errStatus("Deleting workflow", msg.err)
		}
		kept := d.all[:0]
		for _, w := range d.all {
			if w.ID != msg.id {
				kept = append(kept, w)
			}
		}
		d.all = kept
		delete(d.s.workflows, msg.id)
		d.applyFilter()
		return statusCmd(fmt.Sprintf("Deleted workflow %q", msg.name), false)

	case workflowTriggeredMsg:
		if msg.err != nil {
			return errStatus("Trigger "+msg.name, msg.err)
		}
		r := msg.resp
		ok := r.StatusCode >= 200 && r.StatusCode < 300
		text := fmt.Sprintf("Triggered %q → %s in %s · e shows its executions", msg.name, r.Status, components.Duration(r.Duration))
		if !ok {
			text = fmt.Sprintf("Triggered %q but n8n answered %s", msg.name, r.Status)
			text += " — " + strings.TrimSpace(truncate(string(r.Body), 120))
		}
		return statusCmd(text, !ok)

	case tea.KeyMsg:
		if d.filterOn {
			return d.updateFilter(msg)
		}
		if msg.String() == "tab" || msg.String() == "shift+tab" {
			d.focus = 1 - d.focus
			d.table.Focused = d.focus == 0
			return nil
		}
		if d.focus == 0 && d.table.Update(msg) {
			d.syncPreview()
			return nil
		}
		switch msg.String() {
		case "/", "f", "o", "enter", " ", "t", "d":
			return d.handleKey(msg)
		}
		if d.focus == 1 {
			var cmd tea.Cmd
			d.preview, cmd = d.preview.Update(msg)
			return cmd
		}
	default:
		if d.filterOn {
			return d.updateFilter(msg)
		}
	}
	return nil
}

func (d *dashboardScreen) updateFilter(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			d.filter.SetValue("")
			d.filter.Blur()
			d.filterOn = false
			d.applyFilter()
			return nil
		case "enter", "down", "up":
			d.filter.Blur()
			d.filterOn = false
			return nil
		}
	}
	before := d.filter.Value()
	var cmd tea.Cmd
	d.filter, cmd = d.filter.Update(msg)
	if d.filter.Value() != before {
		d.applyFilter()
	}
	return cmd
}

func (d *dashboardScreen) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "/":
		d.focus = 0
		d.table.Focused = true
		d.filterOn = true
		return d.filter.Focus()
	case "f":
		d.status = (d.status + 1) % 3
		d.applyFilter()
	case "o":
		d.sortName = !d.sortName
		d.applyFilter()
	case "enter":
		if wf, ok := d.SelectedWorkflow(); ok {
			return send(openExecutionsMsg{workflowID: wf.ID})
		}
	case " ":
		return d.toggle()
	case "t":
		return d.trigger()
	case "d":
		wf, ok := d.SelectedWorkflow()
		if !ok {
			return nil
		}
		id, name := wf.ID, wf.Name
		del := d.s.do(func(ctx context.Context, c Client) tea.Msg {
			return workflowDeletedMsg{id: id, name: name, err: c.DeleteWorkflow(ctx, id)}
		})
		return send(confirmMsg{
			prompt: fmt.Sprintf("Permanently delete workflow %q from n8n? This cannot be undone.", name),
			onYes:  tea.Batch(statusCmd(fmt.Sprintf("Deleting %q…", name), false), del),
		})
	}
	return nil
}

func (d *dashboardScreen) toggle() tea.Cmd {
	wf, ok := d.SelectedWorkflow()
	if !ok || d.pending[wf.ID] {
		return nil
	}
	d.pending[wf.ID] = true
	d.rebuild()
	id, want := wf.ID, !wf.Active
	return d.s.do(func(ctx context.Context, c Client) tea.Msg {
		w, err := c.SetWorkflowActive(ctx, id, want)
		if err != nil {
			return workflowUpdatedMsg{workflow: &models.Workflow{ID: id}, err: err}
		}
		return workflowUpdatedMsg{workflow: w}
	})
}

// trigger runs the workflow through its production Webhook node. The n8n
// public API has no endpoint to execute an arbitrary workflow.
func (d *dashboardScreen) trigger() tea.Cmd {
	wf, ok := d.SelectedWorkflow()
	if !ok {
		return nil
	}
	triggers := wf.WebhookTriggers()
	if len(triggers) == 0 {
		return statusCmd(fmt.Sprintf(
			"%q has no Webhook trigger node, so it can't be started from here (n8n's API only starts workflows through webhooks). Add a Webhook node in the n8n editor.", wf.Name), true)
	}
	if !wf.Active {
		return statusCmd(fmt.Sprintf(
			"%q is inactive, and n8n only answers production webhooks for active workflows. Press space to activate it, or w to send a test webhook.", wf.Name), true)
	}
	trg := triggers[0]
	req := api.WebhookRequest{Method: trg.Method, Path: trg.Path}
	if trg.Method != http.MethodGet && trg.Method != http.MethodHead {
		req.Body = "{}"
	}
	name := wf.Name
	return tea.Batch(
		statusCmd(fmt.Sprintf("Triggering %q (%s %s)…", name, trg.Method, trg.Path), false),
		d.s.do(func(ctx context.Context, c Client) tea.Msg {
			resp, err := c.TriggerWebhook(ctx, req)
			return workflowTriggeredMsg{name: name, resp: resp, err: err}
		}),
	)
}

func (d *dashboardScreen) applyFilter() {
	selected, _ := d.SelectedWorkflow()
	f := strings.TrimSpace(d.filter.Value())
	sort.SliceStable(d.all, func(i, j int) bool {
		if d.sortName {
			return strings.ToLower(d.all[i].Name) < strings.ToLower(d.all[j].Name)
		}
		return d.all[i].UpdatedAt.After(d.all[j].UpdatedAt)
	})
	d.visible = d.visible[:0]
	for _, w := range d.all {
		if w.Matches(f) && (d.status == 0 || d.status == 1 && w.Active || d.status == 2 && !w.Active) {
			d.visible = append(d.visible, w)
		}
	}
	d.rebuild()
	for i, w := range d.visible {
		if w.ID == selected.ID {
			d.table.SetCursor(i)
			break
		}
	}
	d.syncPreview()
}

func (d *dashboardScreen) rebuild() {
	now := d.s.Now()
	rows := make([][]string, len(d.visible))
	for i, w := range d.visible {
		status := activeLabel(w.Active)
		if d.pending[w.ID] {
			status = "… working"
		}
		trigger := "—"
		if trg := w.WebhookTriggers(); len(trg) > 0 {
			trigger = "webhook"
		}
		rows[i] = []string{status, w.Name, w.TagNames(", "), trigger, components.RelTime(w.UpdatedAt, now), w.ID}
	}
	d.table.SetRows(rows)
}

func (d *dashboardScreen) View() string {
	p := splitPanes(d.width, d.height, 55)
	var summary string
	switch {
	case d.filterOn || d.filter.Value() != "":
		summary = d.filter.View()
	case d.err != nil:
		summary = styleError.Render("✖ Couldn't load workflows · r retry · ! details")
	case !d.loaded:
		summary = styleMuted.Render("Loading workflows…")
	default:
		active := 0
		for _, w := range d.all {
			if w.Active {
				active++
			}
		}
		summary = styleMuted.Render(fmt.Sprintf("%d workflows · %d active", len(d.all), active))
		if d.loading {
			summary += styleMuted.Render(" · refreshing…")
		}
	}
	order := "updated"
	if d.sortName {
		order = "name"
	}
	filters := styleMuted.Render(fmt.Sprintf("%s · sort: %s", []string{"all states", "active only", "inactive only"}[d.status], order))
	d.table.Empty = "No workflows yet. Create one in the n8n editor, then press r."
	if !d.loaded {
		d.table.Empty = "Connecting to " + d.s.Instance.Name + "…"
		if d.err != nil {
			d.table.Empty = api.Explain(d.err).Summary
		}
	} else if d.filter.Value() != "" || d.status != 0 {
		d.table.Empty = "Nothing matches. esc clears the filter, f shows all states."
	}
	position := fmt.Sprintf("%d/%d · tab preview", d.table.Cursor()+1, len(d.visible))
	left := panel("Workflows", position, line(summary, p.lw-4)+"\n"+line(filters, p.lw-4)+"\n"+d.table.View(), p.lw, p.lh, d.focus == 0)
	right := panel("Workflow details", fmt.Sprintf("%.0f%% · tab list", d.preview.ScrollPercent()*100), d.preview.View(), p.rw, p.rh, d.focus == 1)
	return p.join(left, right, d.focus)
}

func (d *dashboardScreen) syncPreview() {
	wf, _ := d.SelectedWorkflow()
	offset := d.preview.YOffset
	d.preview.SetContent(wrap(d.detailView(), d.preview.Width))
	if wf.ID != d.shownID {
		d.preview.GotoTop()
	} else {
		d.preview.SetYOffset(offset)
	}
	d.shownID = wf.ID
}

func (d *dashboardScreen) detailView() string {
	wf, ok := d.SelectedWorkflow()
	if !ok {
		if d.err != nil && !d.loaded {
			return styleError.Render("Can't reach n8n") + "\n\n" + describeError(d.err) + "\n\n" +
				styleMuted.Render("r try again · 6 Health (checks each part of the connection) · 7 Settings · ! full error")
		}
		return styleMuted.Render("Select a workflow to see its details here.\n\n" +
			"↑/↓ move · / search · enter show its executions\n" +
			"space turn on/off · t run it · ? every shortcut")
	}
	lines := []string{
		styleBold.Render(wf.Name),
		activeLabel(wf.Active), "",
		styleLabel.Render("ID") + wf.ID,
		styleLabel.Render("Tags") + wf.TagNames(", "),
		styleLabel.Render("Created") + components.Timestamp(wf.CreatedAt),
		styleLabel.Render("Updated") + components.Timestamp(wf.UpdatedAt),
		"", styleSection.UnsetMarginTop().Render("Webhook triggers"),
	}
	if trg := wf.WebhookTriggers(); len(trg) > 0 {
		for _, t := range trg {
			url := d.s.Client.WebhookURL(api.WebhookRequest{Path: t.Path})
			lines = append(lines, styleBold.Render(t.NodeName), t.Method+" "+url)
		}
		if !wf.Active {
			lines = append(lines, styleWarn.Render("Inactive · space to activate, w for a test webhook"))
		}
	} else {
		lines = append(lines, styleMuted.Render("None. Add a Webhook trigger node in n8n to run this workflow from n8n-tui."))
	}
	lines = append(lines, "", styleSection.UnsetMarginTop().Render(fmt.Sprintf("Nodes (%d)", len(wf.Nodes))))
	for _, n := range wf.Nodes {
		label := n.Name + styleMuted.Render(" · "+strings.TrimPrefix(n.Type, "n8n-nodes-base."))
		if n.Disabled {
			label += styleMuted.Render(" (disabled)")
		}
		lines = append(lines, "• "+label)
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
