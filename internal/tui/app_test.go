package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/models"
)

// ---- fake client -------------------------------------------------------------

type fakeClient struct {
	mu sync.Mutex

	instance   config.Instance
	workflows  []models.Workflow
	executions []models.Execution
	details    map[string]*models.Execution
	webhook    *models.WebhookResponse

	toggled    []string
	deleted    []string
	execQuery  []api.ExecutionFilter
	gotExec    []string
	hooks      []api.WebhookRequest
	credErr    error
	retried    []string
	deletedExe []string
}

func (f *fakeClient) record(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *fakeClient) ListWorkflows(context.Context) ([]models.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]models.Workflow(nil), f.workflows...), nil
}

func (f *fakeClient) SetWorkflowActive(_ context.Context, id string, active bool) (*models.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toggled = append(f.toggled, id)
	for i := range f.workflows {
		if f.workflows[i].ID == id {
			f.workflows[i].Active = active
			w := f.workflows[i]
			return &w, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeClient) DeleteWorkflow(_ context.Context, id string) error {
	f.record(func() { f.deleted = append(f.deleted, id) })
	return nil
}

func (f *fakeClient) ListExecutions(_ context.Context, flt api.ExecutionFilter) (models.Page[models.Execution], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execQuery = append(f.execQuery, flt)
	var out []models.Execution
	for _, e := range f.executions {
		if flt.WorkflowID != "" && e.WorkflowID != flt.WorkflowID {
			continue
		}
		if flt.Status != "" && e.EffectiveStatus() != flt.Status {
			continue
		}
		out = append(out, e)
	}
	return models.Page[models.Execution]{Data: out}, nil
}

func (f *fakeClient) GetExecution(_ context.Context, id string, _ bool) (*models.Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotExec = append(f.gotExec, id)
	if d, ok := f.details[id]; ok {
		return d, nil
	}
	for _, e := range f.executions {
		if e.ID == id {
			return &e, nil
		}
	}
	return nil, &api.Error{StatusCode: 404}
}

func (f *fakeClient) DeleteExecution(_ context.Context, id string) error {
	f.record(func() { f.deletedExe = append(f.deletedExe, id) })
	return nil
}

func (f *fakeClient) RetryExecution(_ context.Context, id string, _ bool) (*models.Execution, error) {
	f.record(func() { f.retried = append(f.retried, id) })
	return &models.Execution{ID: "new-" + id}, nil
}

func (f *fakeClient) ListCredentials(context.Context) ([]models.Credential, error) {
	if f.credErr != nil {
		return nil, f.credErr
	}
	return []models.Credential{{ID: "c1", Name: "Slack Bot", Type: "slackApi"}}, nil
}

func (f *fakeClient) ListVariables(context.Context) ([]models.Variable, error) {
	return []models.Variable{{Key: "API_TOKEN", Value: "super-secret-token-value"}}, nil
}

func (f *fakeClient) TriggerWebhook(_ context.Context, req api.WebhookRequest) (*models.WebhookResponse, error) {
	f.record(func() { f.hooks = append(f.hooks, req) })
	if f.webhook != nil {
		return f.webhook, nil
	}
	return &models.WebhookResponse{StatusCode: 200, Status: "200 OK", Body: []byte(`{"ok":true}`)}, nil
}

func (f *fakeClient) WebhookURL(req api.WebhookRequest) string {
	return "http://n8n.test/webhook/" + req.Path
}

func (f *fakeClient) Health(context.Context) models.Health {
	return models.Health{Healthy: true, ReadyKnown: true, Ready: true, APIReachable: true, Version: "1.90.0", ExecutionMode: "regular"}
}

// ---- harness -------------------------------------------------------------------

type harness struct {
	t       *testing.T
	app     *App
	clients map[string]*fakeClient
	msgs    []tea.Msg
}

func ptime(t time.Time) *time.Time { return &t }

func newHarness(t *testing.T) *harness {
	t.Helper()
	start := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	cfg := &config.Config{
		Instances: []config.Instance{
			{Name: "prod", URL: "http://n8n.test", APIKey: "k"},
			{Name: "staging", URL: "http://staging.test", APIKey: "k2"},
		},
		DefaultInstance: "prod",
		RefreshInterval: time.Hour, // ticks never fire during tests
		Timeout:         time.Second,
	}
	h := &harness{t: t, clients: map[string]*fakeClient{}}
	factory := func(in config.Instance, _ time.Duration) Client {
		fc := &fakeClient{
			instance: in,
			workflows: []models.Workflow{
				{ID: "w1", Name: "Order Sync", Active: false, UpdatedAt: start, Tags: []models.Tag{{Name: "billing"}}},
				{ID: "w2", Name: "Lead Intake", Active: true, UpdatedAt: start.Add(time.Hour), Nodes: []models.Node{
					{Name: "Webhook", Type: "n8n-nodes-base.webhook", Parameters: map[string]any{"path": "leads", "httpMethod": "POST"}},
				}},
			},
			executions: []models.Execution{
				{ID: "101", WorkflowID: "w2", Status: models.StatusError, Mode: "webhook", StartedAt: ptime(start), StoppedAt: ptime(start.Add(2 * time.Second))},
				{ID: "100", WorkflowID: "w1", Status: models.StatusSuccess, Mode: "trigger", StartedAt: ptime(start), StoppedAt: ptime(start.Add(time.Second))},
			},
			details: map[string]*models.Execution{},
		}
		ex := &models.Execution{ID: "101", WorkflowID: "w2", Status: models.StatusError, Data: &models.ExecutionData{}}
		ex.Data.ResultData.RunData = map[string][]models.TaskData{
			"Webhook":      {{StartTime: 1, ExecutionTime: 1}},
			"HTTP Request": {{StartTime: 2, ExecutionTime: 5, Error: &models.ExecutionError{Message: "ECONNREFUSED"}}},
		}
		fc.details["101"] = ex
		h.clients[in.Name] = fc
		return fc
	}
	app, err := New(cfg, "", factory)
	require.NoError(t, err)
	h.app = app
	h.dispatch(tea.WindowSizeMsg{Width: 140, Height: 40})
	h.run(app.Init())
	return h
}

func (h *harness) client() *fakeClient { return h.clients[h.app.session.Instance.Name] }

// run executes a command tree synchronously, feeding results back into the
// app. Commands that block (timers) are abandoned after a short wait.
func (h *harness) run(cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 500; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(100 * time.Millisecond):
			continue // tea.Tick and friends
		}
		switch m := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, m...)
		default:
			h.msgs = append(h.msgs, msg)
			queue = append(queue, h.app.update(msg))
		}
	}
}

func (h *harness) dispatch(msg tea.Msg) { h.run(h.app.update(msg)) }

func (h *harness) keys(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		case "ctrl+s":
			msg = tea.KeyMsg{Type: tea.KeyCtrlS}
		case "ctrl+a":
			msg = tea.KeyMsg{Type: tea.KeyCtrlA}
		case "ctrl+k":
			msg = tea.KeyMsg{Type: tea.KeyCtrlK}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		h.dispatch(msg)
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.dispatch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (h *harness) quit() bool {
	for _, m := range h.msgs {
		if _, ok := m.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

// ---- tests ---------------------------------------------------------------------

func TestDashboardLoadsWorkflows(t *testing.T) {
	h := newHarness(t)
	view := h.app.View()
	assert.Contains(t, view, "Order Sync")
	assert.Contains(t, view, "Lead Intake")
	assert.Contains(t, view, "2 workflows · 1 active")
	assert.Contains(t, view, "prod")

	d := h.app.screens[scrDashboard].(*dashboardScreen)
	wf, ok := d.SelectedWorkflow()
	require.True(t, ok)
	assert.Equal(t, "w2", wf.ID, "most recently updated first")
	assert.Equal(t, "Lead Intake", h.app.session.WorkflowName("w2"))
}

func TestToggleWorkflow(t *testing.T) {
	h := newHarness(t)
	h.keys("down", "space")
	assert.Equal(t, []string{"w1"}, h.client().toggled)
	assert.Contains(t, h.app.status, `"Order Sync" activated`)
	d := h.app.screens[scrDashboard].(*dashboardScreen)
	assert.True(t, d.all[1].Active)
}

func TestDeleteWorkflowRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	h.keys("d")
	require.True(t, h.app.confirm.Active())
	assert.Contains(t, h.app.View(), `Permanently delete workflow "Lead Intake"?`)
	h.keys("n")
	assert.False(t, h.app.confirm.Active())
	assert.Empty(t, h.client().deleted)

	h.keys("d", "enter") // enter cancels too
	assert.Empty(t, h.client().deleted)

	h.keys("d", "y")
	assert.Equal(t, []string{"w2"}, h.client().deleted)
	d := h.app.screens[scrDashboard].(*dashboardScreen)
	require.Len(t, d.all, 1)
	assert.Equal(t, "w1", d.all[0].ID)
	assert.Contains(t, h.app.status, `Deleted workflow "Lead Intake"`)
}

func TestFilterCapturesKeys(t *testing.T) {
	h := newHarness(t)
	h.keys("/")
	h.typeText("order q") // "q" must not quit while typing
	assert.False(t, h.quit())
	h.keys("esc")
	h.keys("/")
	h.typeText("bill")
	h.keys("enter")
	d := h.app.screens[scrDashboard].(*dashboardScreen)
	require.Len(t, d.visible, 1)
	assert.Equal(t, "w1", d.visible[0].ID)

	h.keys("q")
	assert.True(t, h.quit())
}

func TestTriggerWorkflow(t *testing.T) {
	h := newHarness(t)
	h.keys("t") // Lead Intake: active with a POST webhook
	require.Len(t, h.client().hooks, 1)
	assert.Equal(t, api.WebhookRequest{Method: "POST", Path: "leads", Body: "{}"}, h.client().hooks[0])
	assert.Contains(t, h.app.status, "200 OK")

	h.keys("down", "t") // Order Sync: no webhook node
	assert.Len(t, h.client().hooks, 1)
	assert.True(t, h.app.statusErr)
	assert.Contains(t, h.app.status, "no Webhook trigger")
}

func TestExecutionsForSelectedWorkflowAndInspector(t *testing.T) {
	h := newHarness(t)
	h.keys("e")
	assert.Equal(t, scrExecutions, h.app.active)
	q := h.client().execQuery
	require.NotEmpty(t, q)
	assert.Equal(t, "w2", q[len(q)-1].WorkflowID)

	view := h.app.View()
	assert.Contains(t, view, "workflow: Lead Intake")
	assert.Contains(t, view, "101")
	assert.NotContains(t, view, "100 ", "other workflows are filtered out")

	h.keys("enter")
	require.Equal(t, scrDetail, h.app.active)
	assert.Equal(t, []string{"101"}, h.client().gotExec)
	view = h.app.View()
	assert.Contains(t, view, "Execution #101")
	assert.Contains(t, view, "HTTP Request")
	assert.Contains(t, view, "ECONNREFUSED", "first failing node is selected")

	h.keys("R")
	assert.Equal(t, []string{"101"}, h.client().retried)
	assert.Equal(t, scrDetail, h.app.active)
	assert.Equal(t, "new-101", h.app.screens[scrDetail].(*executionDetailScreen).id,
		"the inspector follows the retry")

	h.keys("esc")
	assert.Equal(t, scrExecutions, h.app.active)

	h.keys("a")
	q = h.client().execQuery
	assert.Equal(t, "", q[len(q)-1].WorkflowID)
	assert.Contains(t, h.app.View(), "all workflows")

	h.keys("f")
	q = h.client().execQuery
	assert.Equal(t, models.StatusSuccess, q[len(q)-1].Status)
}

func TestDeleteExecution(t *testing.T) {
	h := newHarness(t)
	h.keys("2", "d", "y")
	assert.Equal(t, []string{"101"}, h.client().deletedExe)
}

func TestWebhookView(t *testing.T) {
	h := newHarness(t)
	h.keys("w") // preset from selected workflow (Lead Intake)
	require.Equal(t, scrWebhook, h.app.active)
	ws := h.app.screens[scrWebhook].(*webhookScreen)
	assert.Equal(t, "leads", ws.path.Value())
	assert.True(t, ws.Capturing())
	assert.Equal(t, fieldBody, ws.focus, "preset focuses the payload editor, even on first visit")

	// Invalid JSON is rejected before sending.
	ws.body.SetValue(`{"broken":`)
	h.keys("ctrl+s")
	assert.Empty(t, h.client().hooks)
	assert.Contains(t, h.app.status, "not valid JSON")

	ws.body.SetValue(`{"name":"Ada"}`)
	h.keys("ctrl+s")
	require.Len(t, h.client().hooks, 1)
	assert.Equal(t, `{"name":"Ada"}`, h.client().hooks[0].Body)
	assert.False(t, h.client().hooks[0].Test)
	assert.Contains(t, h.app.View(), `"ok": true`)

	// "q" is typed into the editor rather than quitting.
	h.typeText("q")
	assert.False(t, h.quit())

	h.keys("esc")
	assert.False(t, ws.Capturing())
	h.keys("esc")
	assert.Equal(t, scrDashboard, h.app.active)
}

func TestParseHeaders(t *testing.T) {
	assert.Equal(t, map[string]string{"X-Token": "abc", "Content-Type": "text/plain"},
		parseHeaders("x-token: abc; content-type:text/plain; junk"))
}

func TestLogsStreamEmitsNewExecutions(t *testing.T) {
	h := newHarness(t)
	h.keys("l")
	logs := h.app.screens[scrLogs].(*logsScreen)
	assert.True(t, logs.baseline)
	assert.NotContains(t, strings.Join(logs.lines, "\n"), "#101", "existing finished executions are not replayed")

	now := time.Now()
	h.client().record(func() {
		h.client().executions = append([]models.Execution{
			{ID: "102", WorkflowID: "w1", Status: models.StatusRunning, Mode: "manual", StartedAt: &now},
		}, h.client().executions...)
	})
	h.dispatch(envelope{gen: h.app.session.gen, msg: logTickMsg{}})
	out := strings.Join(logs.lines, "\n")
	assert.Contains(t, out, "#102")
	assert.Contains(t, out, "Order Sync")
	assert.Contains(t, out, "started")

	h.client().record(func() {
		h.client().executions[0].Status = models.StatusError
		h.client().executions[0].StoppedAt = ptime(now.Add(time.Second))
		ex := &models.Execution{ID: "102", WorkflowID: "w1", Status: models.StatusError, StartedAt: &now, StoppedAt: ptime(now.Add(time.Second)), Data: &models.ExecutionData{}}
		ex.Data.ResultData.RunData = map[string][]models.TaskData{
			"Code": {{StartTime: now.UnixMilli(), ExecutionTime: 3, Error: &models.ExecutionError{Message: "boom"}}},
		}
		h.client().details["102"] = ex
	})
	h.dispatch(envelope{gen: h.app.session.gen, msg: logTickMsg{}})
	out = strings.Join(logs.lines, "\n")
	assert.Contains(t, out, "Code")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "error in")

	h.keys("p")
	assert.True(t, logs.paused)
}

func TestCredentialsMaskedByDefault(t *testing.T) {
	h := newHarness(t)
	h.keys("c")
	view := h.app.View()
	assert.Contains(t, view, "Slack Bot")
	assert.Contains(t, view, "API_TOKEN")
	assert.NotContains(t, view, "super-secret-token-value")
	h.keys("v")
	assert.Contains(t, h.app.View(), "super-secret-token-value")
}

func TestCredentialsUnsupported(t *testing.T) {
	h := newHarness(t)
	h.client().credErr = &api.Error{StatusCode: 404}
	h.keys("c")
	assert.Contains(t, h.app.View(), "not available on this instance")
}

func TestHealthView(t *testing.T) {
	h := newHarness(t)
	h.keys("h")
	view := h.app.View()
	assert.Contains(t, view, "1.90.0")
	assert.Contains(t, view, "connected & migrated")
	assert.Contains(t, view, "regular")
}

func TestSwitchInstanceDropsStaleResponses(t *testing.T) {
	h := newHarness(t)
	oldGen := h.app.session.gen
	h.keys("s")
	view := h.app.View()
	assert.Contains(t, view, "staging")
	assert.NotContains(t, view, "k2", "api keys are never displayed")

	h.keys("down", "enter")
	assert.Equal(t, "staging", h.app.session.Instance.Name)
	assert.Equal(t, scrDashboard, h.app.active)
	assert.Contains(t, h.app.View(), "http://staging.test")

	// A late response from the old instance must be ignored.
	h.dispatch(envelope{gen: oldGen, msg: workflowsMsg{workflows: []models.Workflow{{ID: "zz", Name: "Ghost"}}}})
	assert.NotContains(t, h.app.View(), "Ghost")
}

func TestHelpAndNavigation(t *testing.T) {
	h := newHarness(t)
	h.keys("?")
	assert.Contains(t, h.app.View(), "toggle help")
	h.keys("x") // any key closes
	assert.False(t, h.app.showHelp)

	h.keys("4", "5", "6", "esc")
	assert.Equal(t, scrCredentials, h.app.active)
	h.keys("esc", "esc", "esc")
	assert.Equal(t, scrDashboard, h.app.active)
}

func TestNewUnknownInstance(t *testing.T) {
	cfg := &config.Config{Instances: []config.Instance{{Name: "a", URL: "http://a"}}, RefreshInterval: time.Second}
	_, err := New(cfg, "nope", nil)
	assert.Error(t, err)
}
