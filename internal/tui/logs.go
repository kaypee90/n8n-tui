package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

const (
	logPollLimit       = 30
	maxLogLines        = 2000
	maxDetailFetches   = 8
	logTimestampFormat = "15:04:05"
)

type (
	logTickMsg struct{}
	logPollMsg struct {
		execs   []models.Execution
		details map[string]*models.Execution
		err     error
	}
)

// logsScreen streams execution events by polling GET /executions and the
// details of in-flight executions. n8n's public API has no push channel, so
// polling at refresh_interval is the documented mitigation.
type logsScreen struct {
	s *Session

	view     viewport.Model
	lines    []string
	follow   bool
	paused   bool
	started  bool
	polling  bool
	baseline bool
	lastErr  string

	seen     map[string]models.ExecutionStatus
	nodeSeen map[string]map[string]bool

	width, height int
}

func newLogs(s *Session) *logsScreen {
	return &logsScreen{
		s:        s,
		view:     viewport.New(0, 0),
		follow:   true,
		seen:     map[string]models.ExecutionStatus{},
		nodeSeen: map[string]map[string]bool{},
	}
}

func (l *logsScreen) Init() tea.Cmd {
	l.started = true
	l.appendLine(styleMuted.Render(fmt.Sprintf("— streaming executions from %s every %s —",
		l.s.Instance.Name, l.s.Config.RefreshInterval)))
	return l.poll()
}

// Refresh: the log stream has its own polling loop that keeps running while
// other views are shown, so only manual refreshes trigger an extra poll.
func (l *logsScreen) Refresh(auto bool) tea.Cmd {
	if auto {
		return nil
	}
	return l.poll()
}

func (l *logsScreen) Capturing() bool { return false }

func (l *logsScreen) Keys() []keyHelp {
	pause := "pause"
	if l.paused {
		pause = "resume"
	}
	return []keyHelp{{"p", pause}, {"x", "clear"}, {"↑/↓", "scroll"}, {"G", "follow"}}
}

func (l *logsScreen) SetSize(w, h int) {
	l.width, l.height = w, h
	l.view.Width = w
	l.view.Height = max(h-1, 1)
	l.render()
}

func (l *logsScreen) scheduleTick() tea.Cmd {
	gen := l.s.gen
	return tea.Tick(l.s.Config.RefreshInterval, func(time.Time) tea.Msg {
		return envelope{gen: gen, msg: logTickMsg{}}
	})
}

func (l *logsScreen) poll() tea.Cmd {
	if l.polling {
		return nil
	}
	l.polling = true
	// Snapshot state for the command goroutine.
	known := make(map[string]bool, len(l.seen))
	var track []string
	for id, st := range l.seen {
		known[id] = true
		if !st.IsTerminal() {
			track = append(track, id)
		}
	}
	first := !l.baseline
	return l.s.do(func(ctx context.Context, c Client) tea.Msg {
		page, err := c.ListExecutions(ctx, api.ExecutionFilter{Limit: logPollLimit})
		if err != nil {
			return logPollMsg{err: err}
		}
		// Fetch node data for in-flight executions, for ones in flight on the
		// previous poll (to emit their final node runs), and for executions
		// that appeared since the previous poll.
		want := map[string]bool{}
		for _, id := range track {
			want[id] = true
		}
		for _, ex := range page.Data {
			if !ex.EffectiveStatus().IsTerminal() || (!first && !known[ex.ID]) {
				want[ex.ID] = true
			}
		}
		return logPollMsg{execs: page.Data, details: fetchDetails(ctx, c, want, page.Data)}
	})
}

func fetchDetails(ctx context.Context, c Client, want map[string]bool, order []models.Execution) map[string]*models.Execution {
	// Prefer the newest executions when over budget.
	var ids []string
	for _, ex := range order {
		if want[ex.ID] {
			ids = append(ids, ex.ID)
			delete(want, ex.ID)
		}
	}
	for id := range want { // tracked executions that fell off the page
		ids = append(ids, id)
	}
	if len(ids) > maxDetailFetches {
		ids = ids[:maxDetailFetches]
	}

	out := make(map[string]*models.Execution, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ex, err := c.GetExecution(ctx, id, true)
			if err != nil {
				return
			}
			mu.Lock()
			out[id] = ex
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (l *logsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logTickMsg:
		if l.paused {
			return l.scheduleTick()
		}
		return l.poll()

	case logPollMsg:
		l.polling = false
		next := l.scheduleTick()
		if msg.err != nil {
			if e := msg.err.Error(); e != l.lastErr {
				l.lastErr = e
				l.appendLine(l.stamp(l.s.Now()) + styleError.Render("✖ poll failed: "+e))
				l.render()
			}
			return next
		}
		if l.lastErr != "" {
			l.lastErr = ""
			l.appendLine(l.stamp(l.s.Now()) + styleSuccess.Render("✔ connection restored"))
		}
		l.ingest(msg.execs, msg.details)
		l.render()
		return next

	case tea.KeyMsg:
		switch msg.String() {
		case "p":
			l.paused = !l.paused
			if l.paused {
				l.appendLine(styleMuted.Render("— paused —"))
			} else {
				l.appendLine(styleMuted.Render("— resumed —"))
			}
			l.render()
			return nil
		case "x":
			l.lines = nil
			l.render()
			return nil
		case "G", "end":
			l.follow = true
			l.view.GotoBottom()
			return nil
		}
		var cmd tea.Cmd
		l.view, cmd = l.view.Update(msg)
		l.follow = l.view.AtBottom()
		return cmd
	}
	return nil
}

// ingest diffs a poll result against what has been seen and emits events.
func (l *logsScreen) ingest(execs []models.Execution, details map[string]*models.Execution) {
	current := make(map[string]bool, len(execs))
	for i := len(execs) - 1; i >= 0; i-- { // oldest first
		ex := execs[i]
		current[ex.ID] = true
		if d, ok := details[ex.ID]; ok && d != nil {
			ex.Data = d.Data
			if d.Status != "" {
				ex.Status = d.Status
			}
			ex.StoppedAt = d.StoppedAt
		}
		st := ex.EffectiveStatus()
		prev, known := l.seen[ex.ID]
		l.seen[ex.ID] = st

		if !l.baseline {
			if !st.IsTerminal() {
				l.emitStarted(ex, "already running")
				l.emitNodes(ex)
			} else {
				l.markNodesSeen(ex)
			}
			continue
		}
		switch {
		case !known:
			l.emitStarted(ex, "started")
			l.emitNodes(ex)
			if st.IsTerminal() || st == models.StatusWaiting {
				l.emitStatus(ex)
			}
		case prev != st:
			l.emitNodes(ex)
			l.emitStatus(ex)
		case !st.IsTerminal():
			l.emitNodes(ex)
		}
	}
	// Tracked executions that dropped off the first page but were fetched.
	for id, d := range details {
		if current[id] || d == nil {
			continue
		}
		if prev, ok := l.seen[id]; ok && prev != d.EffectiveStatus() {
			l.emitNodes(*d)
			l.emitStatus(*d)
		}
		l.seen[id] = d.EffectiveStatus()
		current[id] = !d.EffectiveStatus().IsTerminal()
	}
	// Forget executions that are no longer listed and are finished.
	for id, st := range l.seen {
		if !current[id] && st.IsTerminal() {
			delete(l.seen, id)
			delete(l.nodeSeen, id)
		}
	}
	l.baseline = true
}

func (l *logsScreen) stamp(t time.Time) string {
	return styleMuted.Render(t.Local().Format(logTimestampFormat) + "  ")
}

func (l *logsScreen) execLabel(ex models.Execution) string {
	return styleMuted.Render("#"+ex.ID) + " " + styleBold.Render(l.s.WorkflowName(ex.WorkflowID))
}

func (l *logsScreen) emitStarted(ex models.Execution, verb string) {
	t := l.s.Now()
	if ex.StartedAt != nil {
		t = *ex.StartedAt
	}
	l.appendLine(l.stamp(t) + styleInfo.Render("▶ ") + l.execLabel(ex) + styleMuted.Render(fmt.Sprintf("  %s (%s)", verb, ex.Mode)))
}

func (l *logsScreen) emitStatus(ex models.Execution) {
	st := ex.EffectiveStatus()
	t := l.s.Now()
	if ex.StoppedAt != nil {
		t = *ex.StoppedAt
	}
	icon, style := statusIcon(st)
	text := fmt.Sprintf("%s %s", icon, st)
	if st.IsTerminal() {
		text += " in " + components.Duration(ex.Duration(l.s.Now()))
	}
	line := l.stamp(t) + style.Render(text) + "  " + l.execLabel(ex)
	if ex.Data != nil && ex.Data.ResultData.Error != nil {
		line += "  " + styleError.Render(truncate(ex.Data.ResultData.Error.Message, 160))
	}
	l.appendLine(line)
}

func (l *logsScreen) markNodesSeen(ex models.Execution) {
	for _, r := range ex.NodeRuns() {
		l.nodeKeys(ex.ID)[nodeKey(r)] = true
	}
}

func (l *logsScreen) nodeKeys(id string) map[string]bool {
	m, ok := l.nodeSeen[id]
	if !ok {
		m = map[string]bool{}
		l.nodeSeen[id] = m
	}
	return m
}

func nodeKey(r models.NodeRun) string { return fmt.Sprintf("%s#%d", r.Node, r.RunIndex) }

func (l *logsScreen) emitNodes(ex models.Execution) {
	seen := l.nodeKeys(ex.ID)
	for _, r := range ex.NodeRuns() {
		k := nodeKey(r)
		if seen[k] {
			continue
		}
		seen[k] = true
		icon, style := statusIcon(r.Status)
		line := l.stamp(r.StartedAt) + styleMuted.Render("   ├ ") + style.Render(icon) + " " + r.Node +
			styleMuted.Render(fmt.Sprintf("  %s · %d item(s)", components.Duration(r.Duration), r.Items)) +
			styleMuted.Render("  #"+ex.ID)
		if r.Error != nil {
			line += "  " + styleError.Render(truncate(r.Error.Message, 160))
		}
		l.appendLine(line)
	}
}

func (l *logsScreen) appendLine(s string) {
	l.lines = append(l.lines, s)
	if over := len(l.lines) - maxLogLines; over > 0 {
		l.lines = append([]string(nil), l.lines[over:]...)
	}
}

func (l *logsScreen) render() {
	l.view.SetContent(strings.Join(l.lines, "\n"))
	if l.follow {
		l.view.GotoBottom()
	}
}

func (l *logsScreen) View() string {
	state := styleSuccess.Render("● live")
	if l.paused {
		state = styleWarn.Render("⏸ paused")
	} else if l.lastErr != "" {
		state = styleError.Render("✖ disconnected")
	}
	header := state + styleMuted.Render(fmt.Sprintf("  ·  %d tracked  ·  %d lines", l.inFlight(), len(l.lines)))
	if !l.follow {
		header += styleMuted.Render("  ·  scrolled (G to follow)")
	}
	return header + "\n" + l.view.View()
}

func (l *logsScreen) inFlight() int {
	n := 0
	for _, st := range l.seen {
		if !st.IsTerminal() {
			n++
		}
	}
	return n
}
