// Package tui implements the n8n-tui Bubble Tea application.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

// screen is one view of the application.
type screen interface {
	// Init is called the first time the screen is shown.
	Init() tea.Cmd
	// Update receives key messages while the screen is active and every
	// data message regardless of which screen is active.
	Update(msg tea.Msg) tea.Cmd
	View() string
	SetSize(w, h int)
	// Refresh reloads data; auto is true for the periodic refresh tick.
	Refresh(auto bool) tea.Cmd
	// Keys lists screen-specific key bindings for the footer and help.
	Keys() []keyHelp
	// Capturing reports whether a text input has focus, in which case
	// global single-letter shortcuts are disabled.
	Capturing() bool
}

// workflowSelector is implemented by screens with a selected workflow so
// global actions (e, w) can act on it.
type workflowSelector interface {
	SelectedWorkflow() (models.Workflow, bool)
}

type keyHelp struct{ key, desc string }

type screenID int

const (
	scrDashboard screenID = iota
	scrExecutions
	scrDetail
	scrWebhook
	scrLogs
	scrCredentials
	scrHealth
	scrSettings
)

var tabs = []struct {
	id    screenID
	key   string
	title string
}{
	{scrDashboard, "1", "Workflows"},
	{scrExecutions, "2", "Executions"},
	{scrWebhook, "3", "Webhook"},
	{scrLogs, "4", "Logs"},
	{scrCredentials, "5", "Credentials"},
	{scrHealth, "6", "Health"},
	{scrSettings, "7", "Settings"},
}

var globalKeys = []keyHelp{
	{"1-7", "switch view"},
	{"[ / ]", "previous / next view"},
	{"ctrl+p", "search actions and views"},
	{"↑/↓ · j/k", "move selection / scroll"},
	{"g/G · home/end", "first / last row"},
	{"pgup/pgdown", "previous / next page"},
	{"e", "executions (of selected workflow)"},
	{"w", "webhook trigger"},
	{"l", "live log stream"},
	{"c", "credentials & variables"},
	{"h", "instance health"},
	{"s", "settings / switch instance"},
	{"r", "refresh"},
	{"esc", "back"},
	{"?", "toggle help"},
	{"!", "show last message"},
	{"q / ctrl+c", "quit"},
}

const (
	headerHeight      = 3
	footerHeight      = 2
	maxHistory        = 32
	statusTTL         = 5 * time.Second
	minTerminalWidth  = 40
	minTerminalHeight = 12
)

// App is the root Bubble Tea model.
type App struct {
	session *Session
	factory ClientFactory

	screens map[screenID]screen
	inited  map[screenID]bool
	active  screenID
	history []screenID

	width, height int

	status    string
	statusErr bool
	statusID  int

	confirm     components.Confirm
	showHelp    bool
	messageHelp bool
	help        viewport.Model
	lastMessage string
	menu        *actionMenu
}

// New creates the application connected to the named instance ("" for the
// configured default).
func New(cfg *config.Config, instance string, factory ClientFactory) (*App, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}
	in := cfg.Default()
	if instance != "" {
		var ok bool
		if in, ok = cfg.Instance(instance); !ok {
			return nil, fmt.Errorf("instance %q is not configured", instance)
		}
	}
	a := &App{factory: factory, help: viewport.New(0, 0)}
	a.connect(cfg, in, 0)
	return a, nil
}

func (a *App) connect(cfg *config.Config, in config.Instance, gen int) {
	a.session = &Session{
		Config:   cfg,
		Instance: in,
		Client:   a.factory(in, cfg.Timeout),
		gen:      gen,
	}
	s := a.session
	a.screens = map[screenID]screen{
		scrDashboard:   newDashboard(s),
		scrExecutions:  newExecutions(s),
		scrWebhook:     newWebhook(s),
		scrLogs:        newLogs(s),
		scrCredentials: newCredentials(s),
		scrHealth:      newHealth(s),
		scrSettings:    newSettings(s),
	}
	a.inited = map[screenID]bool{}
	a.history = nil
	a.active = scrDashboard
	a.status, a.lastMessage = "", ""
	a.statusID++
	a.showHelp, a.menu = false, nil
	a.confirm = components.Confirm{}
	a.resize()
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.initActive(), a.tick()}
	if len(a.session.Config.Warnings) > 0 {
		cmds = append(cmds, statusCmd("⚠ "+strings.Join(a.session.Config.Warnings, "; "), true))
	}
	return tea.Batch(cmds...)
}

func (a *App) tick() tea.Cmd {
	return tea.Tick(a.session.Config.RefreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) initActive() tea.Cmd {
	if a.inited[a.active] {
		return nil
	}
	a.inited[a.active] = true
	return a.screens[a.active].Init()
}

func (a *App) current() screen { return a.screens[a.active] }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return a, a.update(msg)
}

func (a *App) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resize()
		return nil

	case envelope:
		if msg.gen != a.session.gen {
			return nil // response from a previous instance
		}
		return a.update(msg.msg)

	case tickMsg:
		return tea.Batch(a.tick(), a.current().Refresh(true))

	case statusMsg:
		a.statusID++
		a.status, a.statusErr = msg.text, msg.isErr
		a.lastMessage = msg.text
		if msg.isErr {
			return nil // failures stay visible until replaced or dismissed
		}
		id := a.statusID
		return tea.Tick(statusTTL, func(time.Time) tea.Msg { return clearStatusMsg{id: id} })

	case clearStatusMsg:
		if msg.id == a.statusID {
			a.status = ""
		}
		return nil

	case confirmMsg:
		a.confirm.Open(msg.prompt, msg.onYes)
		return nil

	case openExecutionsMsg:
		ex := a.screens[scrExecutions].(*executionsScreen)
		cmd := ex.SetWorkflowFilter(msg.workflowID)
		wasInited := a.inited[scrExecutions]
		nav := a.navigate(scrExecutions)
		if wasInited {
			return tea.Batch(nav, cmd)
		}
		return nav // Init loads with the filter applied

	case openExecutionMsg:
		a.screens[scrDetail] = newExecutionDetail(a.session, msg.id)
		a.screens[scrDetail].SetSize(a.bodySize())
		a.inited[scrDetail] = false
		if a.active == scrDetail {
			return a.initActive() // retry replaces the inspector, not its history entry
		}
		return a.navigate(scrDetail)

	case openWebhookMsg:
		if msg.trigger != nil {
			a.screens[scrWebhook].(*webhookScreen).Preset(*msg.trigger)
		}
		return a.navigate(scrWebhook)

	case switchInstanceMsg:
		in, ok := a.session.Config.Instance(msg.name)
		if !ok {
			return statusCmd("unknown instance "+msg.name, true)
		}
		a.connect(a.session.Config, in, a.session.gen+1)
		return tea.Batch(a.initActive(), statusCmd("Switched to "+in.Name+" ("+in.BaseURL()+")", false))

	case workflowsMsg:
		if msg.err == nil {
			a.session.setWorkflows(msg.workflows)
		}
		return a.broadcast(msg)
	case workflowUpdatedMsg, workflowDeletedMsg, workflowTriggeredMsg,
		executionsMsg, executionDeletedMsg, executionRetriedMsg, executionDetailMsg,
		webhookResultMsg, credentialsMsg, healthMsg, logTickMsg, logPollMsg:
		// API results and background polling must reach their owning screens
		// even while a modal or another screen is active.
		return a.broadcast(msg)

	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	// Widget messages (including Bubbles' private clipboard messages) are
	// input, not shared data. Deliver them only to the visible input owner.
	if a.terminalTooSmall() || a.confirm.Active() || a.showHelp {
		return nil
	}
	if a.menu != nil {
		return a.updateMenu(msg)
	}
	return a.current().Update(msg)
}

func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range a.screens {
		cmds = append(cmds, s.Update(msg))
	}
	return tea.Batch(cmds...)
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	if a.terminalTooSmall() {
		if k == "q" {
			return tea.Quit
		}
		return nil
	}
	if a.confirm.Active() {
		return a.confirm.Update(msg)
	}
	if a.menu != nil {
		return a.updateMenu(msg)
	}
	if a.showHelp {
		switch k {
		case "up", "down", "j", "k", "pgup", "pgdown", "ctrl+u", "ctrl+d", "ctrl+b", "ctrl+f", "home", "end", "g", "G":
			if k == "g" || k == "home" {
				a.help.GotoTop()
			} else if k == "G" || k == "end" {
				a.help.GotoBottom()
			} else {
				a.help, _ = a.help.Update(msg)
			}
		default:
			a.showHelp = false
		}
		return nil
	}
	if k == "ctrl+p" {
		return a.openMenu()
	}

	cur := a.current()
	if cur.Capturing() {
		return cur.Update(msg)
	}

	switch k {
	case "q":
		return tea.Quit
	case "?":
		a.openHelp(false)
		return nil
	case "!":
		a.openHelp(true)
		return nil
	case "esc":
		if local, ok := cur.(interface{ Back() bool }); ok && local.Back() {
			return nil
		}
		if a.status != "" {
			a.status = ""
		}
		return a.back()
	case "[", "]":
		return a.cycleView(k == "]")
	case "r":
		if cmd := cur.Refresh(false); cmd != nil {
			return tea.Batch(cmd, statusCmd("Refreshing…", false))
		}
		return nil
	case "e":
		if sel, ok := cur.(workflowSelector); ok {
			if wf, ok := sel.SelectedWorkflow(); ok {
				return send(openExecutionsMsg{workflowID: wf.ID})
			}
		}
		return a.navigate(scrExecutions)
	case "w":
		if sel, ok := cur.(workflowSelector); ok {
			if wf, ok := sel.SelectedWorkflow(); ok {
				return send(openWebhookMsg{trigger: presetFor(wf)})
			}
		}
		return a.navigate(scrWebhook)
	case "l":
		return a.navigate(scrLogs)
	case "c":
		return a.navigate(scrCredentials)
	case "h":
		return a.navigate(scrHealth)
	case "s":
		return a.navigate(scrSettings)
	}
	for _, t := range tabs {
		if k == t.key {
			return a.navigate(t.id)
		}
	}
	return cur.Update(msg)
}

func presetFor(wf models.Workflow) *webhookPreset {
	p := &webhookPreset{workflow: wf.Name}
	if trg := wf.WebhookTriggers(); len(trg) > 0 {
		p.method, p.path = trg[0].Method, trg[0].Path
	}
	return p
}

func (a *App) navigate(id screenID) tea.Cmd {
	if id == a.active {
		return nil
	}
	a.history = append(a.history, a.active)
	if len(a.history) > maxHistory {
		a.history = a.history[1:]
	}
	a.active = id
	return a.initActive()
}

func (a *App) back() tea.Cmd {
	if len(a.history) == 0 {
		return nil
	}
	a.active = a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	return a.initActive()
}

func (a *App) bodySize() (int, int) {
	return max(a.width, 20), max(a.height-headerHeight-footerHeight, 3)
}

func (a *App) terminalTooSmall() bool {
	return a.width < minTerminalWidth || a.height < minTerminalHeight
}

func (a *App) resize() {
	w, h := a.bodySize()
	for _, s := range a.screens {
		s.SetSize(w, h)
	}
	a.help.Width, a.help.Height = max(w-4, 1), max(h-2, 1)
	if a.showHelp {
		a.help.SetContent(a.helpContent())
	}
	a.sizeMenu()
}

// View implements tea.Model.
func (a *App) View() string {
	if a.width == 0 {
		return "Loading n8n-tui…"
	}
	if a.terminalTooSmall() {
		return block(fmt.Sprintf("n8n-tui\nTerminal too small.\nResize to at least %d × %d.\nq / ctrl+c quit", minTerminalWidth, minTerminalHeight), a.width, a.height)
	}
	w, h := a.bodySize()

	var body string
	switch {
	case a.confirm.Active():
		body = a.confirm.View(w, h)
	case a.menu != nil:
		body = a.menuView()
	case a.showHelp:
		title := "Keyboard help · " + a.screenTitle()
		if a.messageHelp {
			title = "Last message"
		}
		body = panel(title, "↑/↓ scroll · esc close", a.help.View(), w, h, true)
	default:
		body = a.current().View()
	}
	body = block(body, w, h)

	return lipgloss.JoinVertical(lipgloss.Left, a.headerView(), body, a.footerView())
}

func (a *App) headerView() string {
	in := a.session.Instance
	title := styleTitle.Render("⚡ n8n-tui") + styleMuted.Render("  ·  ") +
		styleBold.Render(in.Name) + styleMuted.Render("  "+in.BaseURL())

	activeTab := a.active
	if activeTab == scrDetail {
		activeTab = scrExecutions
	}
	var parts []string
	for _, t := range tabs {
		label := t.key + " " + t.title
		if t.id == activeTab {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	tabLine := strings.Join(parts, "")
	if lipgloss.Width(tabLine) > a.width {
		parts = nil
		for _, t := range tabs {
			label := t.key
			st := styleTab
			if t.id == activeTab {
				label += " " + t.title
				st = styleTabActive
			}
			parts = append(parts, st.Render(label))
		}
		tabLine = strings.Join(parts, "")
	}
	rule := styleMuted.Render(strings.Repeat("─", max(a.width, 0)))
	return lipgloss.JoinVertical(lipgloss.Left,
		line(title, a.width), line(tabLine, a.width), rule)
}

func (a *App) footerView() string {
	keys := a.current().Keys()
	quit := "q"
	if a.current().Capturing() {
		quit = "ctrl+c"
	}
	base := []keyHelp{{"ctrl+p", "actions"}, {"?", "help"}, {quit, "quit"}}
	switch {
	case a.confirm.Active():
		keys, base = []keyHelp{{"y", "confirm"}, {"enter/n/esc", "cancel"}}, nil
	case a.menu != nil:
		keys, base = []keyHelp{{"↑/↓", "select"}, {"enter", "run"}, {"esc", "close"}}, nil
	case a.showHelp:
		keys, base = []keyHelp{{"↑/↓", "scroll"}, {"pgup/pgdown", "page"}, {"esc", "close"}}, nil
	case a.current().Capturing():
		base = []keyHelp{{"ctrl+p", "actions"}, {"ctrl+c", "quit"}}
	}
	render := func(k keyHelp) string { return styleKey.Render(k.key) + " " + styleMuted.Render(k.desc) }
	var tail []string
	for _, k := range base {
		tail = append(tail, render(k))
	}
	hints := strings.Join(tail, "  ")
	var head []string
	for _, k := range keys {
		candidate := strings.Join(append(append([]string(nil), head...), render(k), hints), "  ")
		if lipgloss.Width(candidate) <= a.width {
			head = append(head, render(k))
		}
	}
	hints = strings.TrimSpace(strings.Join(append(head, hints), "  "))
	status := styleMuted.Render(a.screenTitle() + " · poll " + a.session.Config.RefreshInterval.String() + " · [ ] views")
	if a.status != "" {
		st := styleSuccess
		if a.statusErr {
			st = styleError
		}
		status = st.Render(line(a.status, max(a.width-12, 1))) + styleMuted.Render(" · ! details")
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		block(status, a.width, 1), block(hints, a.width, 1))
}

func (a *App) openHelp(message bool) {
	a.showHelp, a.messageHelp = true, message
	a.help.SetContent(a.helpContent())
	a.help.GotoTop()
}

func (a *App) helpContent() string {
	if a.messageHelp {
		text := a.lastMessage
		if text == "" {
			text = "No messages yet."
		}
		return wrap(text, a.help.Width)
	}
	render := func(title string, keys []keyHelp) string {
		var b strings.Builder
		b.WriteString(styleSection.Render(title) + "\n")
		for _, k := range keys {
			b.WriteString(wrap(styleKey.Render(k.key)+"  "+k.desc, a.help.Width) + "\n")
		}
		return b.String()
	}
	return render("This view", a.current().Keys()) + "\n" + render("Global", globalKeys)
}

func (a *App) screenTitle() string {
	if a.active == scrDetail {
		return "Execution inspector"
	}
	for _, t := range tabs {
		if t.id == a.active {
			return t.title
		}
	}
	return "n8n-tui"
}

func (a *App) cycleView(forward bool) tea.Cmd {
	id := a.active
	if id == scrDetail {
		id = scrExecutions
	}
	for i, t := range tabs {
		if t.id == id {
			delta := len(tabs) - 1
			if forward {
				delta = 1
			}
			return a.navigate(tabs[(i+delta)%len(tabs)].id)
		}
	}
	return nil
}
