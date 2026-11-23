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
	fieldNone = iota - 1
	fieldMethod
	fieldPath
	fieldMode
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
	focus    int
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
	h.Placeholder = "X-Header: value; Other: value"
	h.CharLimit = 2048

	b := textarea.New()
	b.Placeholder = `{"hello": "world"}`
	b.ShowLineNumbers = true
	b.CharLimit = 0
	b.SetValue("{\n  \n}")

	w := &webhookScreen{s: s, path: p, headers: h, body: b, response: viewport.New(0, 0), focus: fieldNone, presetIx: -1}
	w.response.SetContent(styleMuted.Render(webhookHint))
	return w
}

const webhookHint = `Send an HTTP request to an n8n Webhook node.

• production (/webhook/…) requires the workflow to be active.
• test (/webhook-test/…) requires "Listen for test event" in the n8n editor.

ctrl+s sends · ctrl+n cycles webhook triggers of known workflows · tab moves between fields`

// Init focuses the path field, unless a preset (applied just before the
// first activation) already chose a field.
func (w *webhookScreen) Init() tea.Cmd {
	if w.focus == fieldNone {
		return w.setFocus(fieldPath)
	}
	return w.setFocus(w.focus)
}

func (w *webhookScreen) Refresh(bool) tea.Cmd { return nil }

func (w *webhookScreen) Capturing() bool { return w.focus != fieldNone }

func (w *webhookScreen) Keys() []keyHelp {
	if w.focus == fieldNone {
		return []keyHelp{{"enter/tab", "edit"}, {"ctrl+s", "send"}, {"ctrl+n", "next preset"}}
	}
	k := []keyHelp{{"ctrl+s", "send"}, {"tab", "next field"}, {"ctrl+n", "next preset"}, {"esc", "stop editing"}}
	if w.focus == fieldMethod || w.focus == fieldMode {
		k = append([]keyHelp{{"←/→", "change"}}, k...)
	}
	return k
}

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
		w.response.SetContent(styleWarn.Render(fmt.Sprintf(
			"%q has no Webhook trigger node; enter a path manually.", p.workflow)) + "\n\n" + styleMuted.Render(webhookHint))
	}
	if p.path == "" {
		w.setFocus(fieldPath)
	} else {
		w.setFocus(fieldBody)
	}
}

func (w *webhookScreen) setFocus(f int) tea.Cmd {
	w.focus = f
	w.path.Blur()
	w.headers.Blur()
	w.body.Blur()
	switch f {
	case fieldPath:
		return w.path.Focus()
	case fieldHeaders:
		return w.headers.Focus()
	case fieldBody:
		return w.body.Focus()
	}
	return nil
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
		return statusCmd("Webhook path is required", true)
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
		return statusCmd("No workflows with Webhook triggers found", true)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].wf < items[j].wf })
	w.presetIx = (w.presetIx + 1) % len(items)
	it := items[w.presetIx]
	w.Preset(webhookPreset{workflow: it.wf, method: it.trg.Method, path: it.trg.Path})
	return statusCmd(fmt.Sprintf("Preset %d/%d: %s (%s)", w.presetIx+1, len(items), it.wf, it.trg.NodeName), false)
}

func (w *webhookScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case webhookResultMsg:
		w.sending = false
		if msg.err != nil {
			w.response.SetContent(styleError.Render("✖ " + msg.err.Error()))
			return errStatus("Webhook", msg.err)
		}
		w.response.SetContent(renderWebhookResponse(msg.resp, w.width))
		w.response.GotoTop()
		ok := msg.resp.StatusCode < 300
		return statusCmd(fmt.Sprintf("Webhook → %s in %s", msg.resp.Status, components.Duration(msg.resp.Duration)), !ok)

	case tea.KeyMsg:
		return w.handleKey(msg)
	}
	// Cursor blink and paste messages belong to the focused editor too.
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
	switch msg.String() {
	case "ctrl+s":
		return w.send()
	case "ctrl+n":
		return w.cyclePreset()
	case "esc":
		return w.setFocus(fieldNone)
	case "tab":
		return w.setFocus((w.focus + 1) % fieldCount)
	case "shift+tab":
		return w.setFocus((w.focus - 1 + fieldCount) % fieldCount)
	}

	switch w.focus {
	case fieldNone:
		if msg.String() == "enter" {
			return w.setFocus(fieldPath)
		}
		var cmd tea.Cmd
		w.response, cmd = w.response.Update(msg)
		return cmd
	case fieldMethod:
		switch msg.String() {
		case "left", "h":
			w.method = (w.method - 1 + len(webhookMethods)) % len(webhookMethods)
		case "right", "l", " ", "enter":
			w.method = (w.method + 1) % len(webhookMethods)
		}
	case fieldMode:
		switch msg.String() {
		case "left", "right", "h", "l", " ", "enter":
			w.test = !w.test
		}
	case fieldPath:
		if msg.String() == "enter" {
			return w.setFocus(fieldBody)
		}
		var cmd tea.Cmd
		w.path, cmd = w.path.Update(msg)
		return cmd
	case fieldHeaders:
		if msg.String() == "enter" {
			return w.setFocus(fieldBody)
		}
		var cmd tea.Cmd
		w.headers, cmd = w.headers.Update(msg)
		return cmd
	case fieldBody:
		var cmd tea.Cmd
		w.body, cmd = w.body.Update(msg)
		return cmd
	}
	return nil
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
		if w.focus == f {
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
	lines = append(lines, label(fieldBody, "Body (JSON)"))
	lines = append(lines, w.body.View())
	rule := "── Response "
	if w.sending {
		rule += "(sending…) "
	}
	lines = append(lines, styleMuted.Render(rule+strings.Repeat("─", max(w.width-len(rule), 0))))
	lines = append(lines, w.response.View())
	return strings.Join(lines, "\n")
}
