package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/models"
)

// Session is the state shared by every screen for the connected instance.
// It is only mutated from the Bubble Tea update loop.
type Session struct {
	Config   *config.Config
	Instance config.Instance
	Client   Client
	// gen increments on every instance switch so responses from the
	// previous instance can be discarded.
	gen int

	workflows map[string]models.Workflow
	now       func() time.Time
}

// Client is the subset of *api.Client the TUI uses (an interface so tests can
// substitute a fake).
type Client interface {
	ListWorkflows(ctx context.Context) ([]models.Workflow, error)
	SetWorkflowActive(ctx context.Context, id string, active bool) (*models.Workflow, error)
	DeleteWorkflow(ctx context.Context, id string) error
	ListExecutions(ctx context.Context, f api.ExecutionFilter) (models.Page[models.Execution], error)
	GetExecution(ctx context.Context, id string, includeData bool) (*models.Execution, error)
	DeleteExecution(ctx context.Context, id string) error
	RetryExecution(ctx context.Context, id string, loadWorkflow bool) (*models.Execution, error)
	ListCredentials(ctx context.Context) ([]models.Credential, error)
	ListVariables(ctx context.Context) ([]models.Variable, error)
	TriggerWebhook(ctx context.Context, req api.WebhookRequest) (*models.WebhookResponse, error)
	WebhookURL(req api.WebhookRequest) string
	Health(ctx context.Context) models.Health
}

// ClientFactory builds a client for an instance.
type ClientFactory func(config.Instance, time.Duration) Client

// DefaultClientFactory builds real HTTP clients.
func DefaultClientFactory(in config.Instance, timeout time.Duration) Client {
	return api.New(in, timeout)
}

func (s *Session) Now() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// WorkflowName resolves a workflow ID to its name (falls back to the ID).
func (s *Session) WorkflowName(id string) string {
	if w, ok := s.workflows[id]; ok {
		return w.Name
	}
	if id == "" {
		return "—"
	}
	return "#" + id
}

// Workflow returns a cached workflow.
func (s *Session) Workflow(id string) (models.Workflow, bool) {
	w, ok := s.workflows[id]
	return w, ok
}

// Workflows returns cached workflows sorted by name.
func (s *Session) Workflows() []models.Workflow {
	out := make([]models.Workflow, 0, len(s.workflows))
	for _, w := range s.workflows {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Session) setWorkflows(ws []models.Workflow) {
	s.workflows = make(map[string]models.Workflow, len(ws))
	for _, w := range ws {
		s.workflows[w.ID] = w
	}
}

// envelope tags an async result with the session generation it belongs to.
type envelope struct {
	gen int
	msg tea.Msg
}

// do runs f asynchronously with a timeout and tags the result.
func (s *Session) do(f func(ctx context.Context, c Client) tea.Msg) tea.Cmd {
	gen, client := s.gen, s.Client
	timeout := s.Config.Timeout
	if timeout <= 0 {
		timeout = config.DefaultTimeout
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return envelope{gen: gen, msg: f(ctx, client)}
	}
}

// ---- messages ---------------------------------------------------------------

type (
	tickMsg struct{}

	statusMsg struct {
		text  string
		isErr bool
		// detail is extra text shown by "!" (raw error, how to fix it).
		detail string
	}
	clearStatusMsg struct{ id int }

	confirmMsg struct {
		prompt string
		onYes  tea.Cmd
	}

	// navigation
	openExecutionsMsg struct{ workflowID string }
	openExecutionMsg  struct{ id string }
	openWebhookMsg    struct{ trigger *webhookPreset }
	switchInstanceMsg struct{ name string }

	// data
	workflowsMsg struct {
		workflows []models.Workflow
		err       error
	}
	workflowUpdatedMsg struct {
		workflow *models.Workflow
		err      error
	}
	workflowDeletedMsg struct {
		id, name string
		err      error
	}
	workflowTriggeredMsg struct {
		name string
		resp *models.WebhookResponse
		err  error
	}
)

type webhookPreset struct {
	workflow string
	method   string
	path     string
}

func statusCmd(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text, isErr: isErr} }
}

// errStatus reports a failed action in plain language. The full error and a
// suggested fix are available with "!".
func errStatus(action string, err error) tea.Cmd {
	p := api.Explain(err)
	msg := statusMsg{text: action + " failed: " + p.Summary, isErr: true, detail: errorDetail(err)}
	return func() tea.Msg { return msg }
}

// describeError is a one-paragraph explanation for inline display.
func describeError(err error) string {
	p := api.Explain(err)
	if p.Hint == "" {
		return p.Summary
	}
	return p.Summary + ". " + p.Hint
}

func errorDetail(err error) string {
	p := api.Explain(err)
	var b strings.Builder
	if p.Hint != "" {
		b.WriteString("How to fix it:\n" + p.Hint + "\n\n")
	}
	b.WriteString("Technical details:\n" + err.Error() + "\n\n")
	b.WriteString("Still stuck? Press 6 to open the Health view, which checks the connection, database and API key one by one.")
	return b.String()
}

func send(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }
