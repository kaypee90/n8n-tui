package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/kaypee90/n8n-tui/internal/api"
	"github.com/kaypee90/n8n-tui/internal/models"
	"github.com/kaypee90/n8n-tui/internal/tui/components"
)

var webhookMethods = []string{http.MethodPost, http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead}

const (
	fieldMethod = iota
	fieldMode
	fieldPath
	fieldHeaders
	fieldBody
	fieldCount
)

type webhookResultMsg struct {
	resp *models.WebhookResponse
	err  error
}

type webhookScreen struct {
	s *Session

	method   int
	path     textinput.Model
	test     bool
	headers  textinput.Model
	body     textarea.Model
	response viewport.Model
	focus    int  // selected field
	editing  bool // typing into the selected text field
	workflow string
	sending  bool
	presetIx int

	width, height int
}

func newWebhook(s *Session) *webhookScreen {
	p := textinput.New()
	p.Prompt = ""
	p.Placeholder = "my-webhook-path"
	p.CharLimit = 512

	h := textinput.New()
	h.Prompt = ""
	h.Placeholder = "optional, e.g. X-Token: abc; X-Env: dev"
	h.CharLimit = 2048

	b := textarea.New()
	b.Placeholder = `{"hello": "world"}`
	b.ShowLineNumbers = true
	b.CharLimit = 0
	b.SetValue("{\n  \n}")

	w := &webhookScreen{s: s, path: p, headers: h, body: b, response: viewport.New(0, 0), focus: fieldPath, presetIx: -1}
	w.response.SetContent(styleMuted.Render(webhookHint))
	return w
}

const webhookHint = `Send an HTTP request to a workflow's Webhook trigger node.

How to use this view:
  1. Press ctrl+n to pick a workflow's webhook (or select Path and type one).
  2. ↑/↓ selects a field, enter edits it, esc stops editing.
  3. Press ctrl+s to send. The response appears here.

Modes:
  • production (/webhook/…) only works while the workflow is active.
  • test (/webhook-test/…) only works while "Listen for test event" is
    running in the n8n editor.`

func (w *webhookScreen) Init() tea.Cmd { return nil }

func (w *webhookScreen) Refresh(bool) tea.Cmd { return nil }

// Capturing is true only while typing, so view shortcuts (1-7, q, ...) keep
// working whenever the user is just moving between fields.
func (w *webhookScreen) Capturing() bool { return w.editing }

func (w *webhookScreen) About() string {
	return "Send a test request to a workflow's Webhook trigger and see n8n's response."
}

func (w *webhookScreen) Keys() []keyHelp {
	if w.editing {
		k := []keyHelp{{"esc", "stop editing"}, {"ctrl+s", "send"}, {"tab", "next field"}}
		if w.focus == fieldBody {
			k = append(k, keyHelp{"enter", "new line"})
		}
		return k
	}
	k := []keyHelp{{"ctrl+s", "send"}, {"↑/↓", "select field"}}
	if isTextField(w.focus) {
		k = append(k, keyHelp{"enter", "edit " + strings.ToLower(fieldNames[w.focus])})
	} else {
		k = append(k, keyHelp{"←/→", "change " + strings.ToLower(fieldNames[w.focus])})
	}
	return append(k, keyHelp{"ctrl+n", "next workflow webhook"}, keyHelp{"pgup/pgdown", "scroll response"})
}

var fieldNames = [fieldCount]string{"Method", "Mode", "Path", "Headers", "Body"}

func isTextField(f int) bool { return f == fieldPath || f == fieldHeaders || f == fieldBody }

func (w *webhookScreen) SetSize(width, height int) {
	w.width, w.height = width, height
	w.path.Width = max(width-30, 10)
	w.headers.Width = max(width-12, 10)
	w.body.SetWidth(max(width, 20))
	bodyH := max((height-8)/2, 3)
	w.body.SetHeight(bodyH)
	w.response.Width = width
	w.response.Height = max(height-8-bodyH, 2)
}

// Preset changes the webhook target without changing the user's selected mode.
func (w *webhookScreen) Preset(p webhookPreset) {
	w.workflow = p.workflow
	w.path.SetValue(p.path)
	w.method = 0
	w.response.GotoTop()
	w.response.SetContent(styleMuted.Render(webhookHint))
	if p.method != "" {
		for i, m := range webhookMethods {
			if m == p.method {
				w.method = i
			}
		}
	}
	if p.path == "" {
		w.response.SetContent(styleWarn.Render(wrap(fmt.Sprintf(
			"%q has no Webhook trigger node, so it can't be called over HTTP. Add a Webhook node in n8n, or press enter to type a path manually.", p.workflow), w.width)) + "\n\n" + styleMuted.Render(webhookHint))
		w.setFocus(fieldPath, false)
	} else {
		w.response.SetContent(styleSuccess.Render(fmt.Sprintf("Ready to call %q. Press ctrl+s to send, or enter to edit the body first.", p.workflow)) +
			"\n\n" + styleMuted.Render(webhookHint))
		w.setFocus(fieldBody, false)
	}
}

// setFocus selects field f; edit starts typing into it (text fields only).
func (w *webhookScreen) setFocus(f int, edit bool) tea.Cmd {
	w.focus = f
	w.editing = edit && isTextField(f)
	w.path.Blur()
	w.headers.Blur()
	w.body.Blur()
	if !w.editing {
		return nil
	}
	switch f {
	case fieldPath:
		return w.path.Focus()
	case fieldHeaders:
		return w.headers.Focus()
	default:
		return w.body.Focus()
	}
}

func (w *webhookScreen) request() api.WebhookRequest {
	return api.WebhookRequest{
		Method:  webhookMethods[w.method],
		Path:    strings.TrimSpace(w.path.Value()),
		Test:    w.test,
		Body:    strings.TrimSpace(w.body.Value()),
		Headers: parseHeaders(w.headers.Value()),
	}
}

func parseHeaders(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			out[http.CanonicalHeaderKey(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func (w *webhookScreen) send() tea.Cmd {
	if w.sending {
		return nil
	}
	req := w.request()
	if req.Path == "" {
		return statusCmd("Enter a webhook path first (select Path and press enter), or press ctrl+n to pick a workflow", true)
	}
	_, customCT := req.Headers["Content-Type"]
	if req.Body != "" && !customCT && !json.Valid([]byte(req.Body)) {
		return statusCmd("Body is not valid JSON (set a Content-Type header to send other formats)", true)
	}
	w.sending = true
	w.response.SetContent(styleMuted.Render(fmt.Sprintf("Sending %s %s …", req.Method, w.s.Client.WebhookURL(req))))
	return w.s.do(func(ctx context.Context, c Client) tea.Msg {
		resp, err := c.TriggerWebhook(ctx, req)
		return webhookResultMsg{resp: resp, err: err}
	})
}

// cyclePreset fills the form with the next webhook trigger found in the
// cached workflow list.
func (w *webhookScreen) cyclePreset() tea.Cmd {
	type item struct {
		wf  string
		trg models.WebhookTrigger
	}
	var items []item
	for _, wf := range w.s.Workflows() {
		for _, t := range wf.WebhookTriggers() {
			items = append(items, item{wf.Name, t})
		}
	}
	if len(items) == 0 {
		return statusCmd("No workflow has a Webhook trigger node. Add one in n8n, or type a path manually.", true)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].wf < items[j].wf })
	w.presetIx = (w.presetIx + 1) % len(items)
	it := items[w.presetIx]
	w.Preset(webhookPreset{workflow: it.wf, method: it.trg.Method, path: it.trg.Path})
	return statusCmd(fmt.Sprintf("Webhook %d of %d: %s (%s node) · ctrl+n for the next one", w.presetIx+1, len(items), it.wf, it.trg.NodeName), false)
}

func (w *webhookScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case webhookResultMsg:
		w.sending = false
		if msg.err != nil {
			w.response.SetContent(styleError.Render(wrap("✖ "+describeError(msg.err), w.width)))
			return errStatus("Webhook", msg.err)
		}
		w.response.SetContent(renderWebhookResponse(msg.resp, w.width))
		w.response.GotoTop()
		ok := msg.resp.StatusCode < 300
		return statusCmd(fmt.Sprintf("Webhook → %s in %s", msg.resp.Status, components.Duration(msg.resp.Duration)), !ok)

	case tea.KeyMsg:
		return w.handleKey(msg)
	}
	// Cursor blink and paste messages belong to the field being edited.
	if !w.editing {
		return nil
	}
	var cmd tea.Cmd
	switch w.focus {
	case fieldPath:
		w.path, cmd = w.path.Update(msg)
	case fieldHeaders:
		w.headers, cmd = w.headers.Update(msg)
	case fieldBody:
		w.body, cmd = w.body.Update(msg)
	}
	return cmd
}

func (w *webhookScreen) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "ctrl+s":
		return w.send()
	case "ctrl+n":
		return w.cyclePreset()
	case "tab":
		return w.setFocus((w.focus+1)%fieldCount, w.editing)
	case "shift+tab":
		return w.setFocus((w.focus-1+fieldCount)%fieldCount, w.editing)
	}
	if w.editing {
		return w.editKey(msg)
	}

	switch k {
	case "up", "k":
		return w.setFocus(max(w.focus-1, 0), false)
	case "down", "j":
		return w.setFocus(min(w.focus+1, fieldCount-1), false)
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		w.response, cmd = w.response.Update(msg)
		return cmd
	}
	switch w.focus {
	case fieldMethod:
		switch k {
		case "left":
			w.method = (w.method - 1 + len(webhookMethods)) % len(webhookMethods)
		case "right", " ", "enter":
			w.method = (w.method + 1) % len(webhookMethods)
		}
	case fieldMode:
		switch k {
		case "left", "right", " ", "enter":
			w.test = !w.test
		}
	default:
		if k == "enter" {
			return w.setFocus(w.focus, true)
		}
	}
	return nil
}

func (w *webhookScreen) editKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "esc" {
		return w.setFocus(w.focus, false)
	}
	var cmd tea.Cmd
	switch w.focus {
	case fieldPath, fieldHeaders:
		if msg.String() == "enter" {
			return w.setFocus(w.focus, false)
		}
		if w.focus == fieldPath {
			w.path, cmd = w.path.Update(msg)
		} else {
			w.headers, cmd = w.headers.Update(msg)
		}
	case fieldBody:
		w.body, cmd = w.body.Update(msg)
	}
	return cmd
}

func renderWebhookResponse(r *models.WebhookResponse, width int) string {
	var b strings.Builder
	st := styleSuccess
	if r.StatusCode >= 400 {
		st = styleError
	} else if r.StatusCode >= 300 {
		st = styleWarn
	}
	b.WriteString(st.Bold(true).Render(r.Status) + styleMuted.Render(fmt.Sprintf("  ·  %s %s  ·  %s", r.Method, r.URL, components.Duration(r.Duration))) + "\n")
	if ct := r.Headers["Content-Type"]; len(ct) > 0 {
		b.WriteString(styleMuted.Render("Content-Type: "+ct[0]) + "\n")
	}
	if r.StatusCode == http.StatusNotFound {
		b.WriteString(styleWarn.Render(wrap("n8n returned 404: the webhook is not registered. Production webhooks need an active workflow; test webhooks need the editor to be listening.", width)) + "\n")
	}
	b.WriteString("\n")
	if len(r.Body) == 0 {
		b.WriteString(styleMuted.Render("(empty body)"))
		return b.String()
	}
	var buf bytes.Buffer
	if json.Indent(&buf, r.Body, "", "  ") == nil {
		b.WriteString(buf.String())
	} else {
		body := string(r.Body)
		if len(body) > maxOutputBytes {
			body = body[:maxOutputBytes] + "\n…"
		}
		b.WriteString(body)
	}
	return b.String()
}

func (w *webhookScreen) View() string {
	label := func(f int, s string) string {
		switch {
		case w.focus == f && w.editing:
			return styleKey.Render("✎ " + s)
		case w.focus == f:
			return styleKey.Render("▸ " + s)
		}
		return styleMuted.Render("  " + s)
	}

	methods := make([]string, len(webhookMethods))
	for i, m := range webhookMethods {
		if i == w.method {
			methods[i] = styleTabActive.Render(m)
		} else {
			methods[i] = styleMuted.Render(m)
		}
	}
	prod, test := "(•) production", "( ) test"
	if w.test {
		prod, test = "( ) production", "(•) test"
	}

	prefix := "/webhook/"
	if w.test {
		prefix = "/webhook-test/"
	}

	var lines []string
	title := "Webhook trigger"
	if w.workflow != "" {
		title += styleMuted.Render(" · ") + w.workflow
	}
	lines = append(lines, styleBold.Render(title))
	lines = append(lines, label(fieldMethod, "Method  ")+" "+strings.Join(methods, " ")+"    "+label(fieldMode, "Mode ")+" "+prod+"  "+test)
	lines = append(lines, label(fieldPath, "Path    ")+" "+styleMuted.Render(prefix)+w.path.View())
	lines = append(lines, styleMuted.Render("           → "+w.s.Client.WebhookURL(w.request())))
	lines = append(lines, label(fieldHeaders, "Headers ")+" "+w.headers.View())
	bodyLabel := label(fieldBody, "Body (JSON)")
	switch {
	case w.focus == fieldBody && w.editing:
		bodyLabel += styleMuted.Render("  editing · esc to stop")
	case w.focus == fieldBody:
		bodyLabel += styleMuted.Render("  enter to edit")
	}
	lines = append(lines, bodyLabel)
	lines = append(lines, w.body.View())
	rule := "── Response "
	if w.sending {
		rule += "(sending…) "
	}
	lines = append(lines, styleMuted.Render(rule+strings.Repeat("─", max(w.width-len(rule), 0))))
	lines = append(lines, w.response.View())
	return strings.Join(lines, "\n")
}
