package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaypee90/n8n-tui/internal/config"
	"github.com/kaypee90/n8n-tui/internal/models"
)

type recorded struct {
	Method, Path, RawQuery, APIKey, Body string
	User, Pass                           string
	HasBasic                             bool
}

type fakeN8N struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
}

func newFake(t *testing.T, h http.HandlerFunc) *fakeN8N {
	f := &fakeN8N{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u, p, ok := r.BasicAuth()
		f.mu.Lock()
		f.requests = append(f.requests, recorded{
			Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
			APIKey: r.Header.Get("X-N8N-API-KEY"), Body: string(body),
			User: u, Pass: p, HasBasic: ok,
		})
		f.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeN8N) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func client(f *fakeN8N, mod ...func(*config.Instance)) *Client {
	in := config.Instance{Name: "t", URL: f.URL, APIKey: "secret-key"}
	for _, m := range mod {
		m(&in)
	}
	return New(in, 2*time.Second)
}

func TestListWorkflowsPaginatesAndAuthenticates(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/workflows", r.URL.Path)
		if r.URL.Query().Get("cursor") == "" {
			writeJSON(w, 200, map[string]any{"data": []map[string]any{{"id": "1", "name": "A", "active": true}}, "nextCursor": "c2"})
			return
		}
		assert.Equal(t, "c2", r.URL.Query().Get("cursor"))
		writeJSON(w, 200, map[string]any{"data": []map[string]any{{"id": "2", "name": "B", "tags": []map[string]string{{"id": "t", "name": "prod"}}}}, "nextCursor": nil})
	})

	ws, err := client(f).ListWorkflows(context.Background())
	require.NoError(t, err)
	require.Len(t, ws, 2)
	assert.True(t, ws[0].Active)
	assert.Equal(t, "prod", ws[1].TagNames(","))
	assert.Len(t, f.requests, 2)
	assert.Equal(t, "secret-key", f.last().APIKey)
	assert.False(t, f.last().HasBasic)
}

func TestBasicAuth(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"data": []any{}})
	})
	_, err := client(f, func(in *config.Instance) {
		in.APIKey, in.Username, in.Password = "", "admin", "pw"
	}).ListWorkflows(context.Background())
	require.NoError(t, err)
	r := f.last()
	assert.True(t, r.HasBasic)
	assert.Equal(t, "admin", r.User)
	assert.Equal(t, "pw", r.Pass)
	assert.Empty(t, r.APIKey)
}

func TestTrailingAPIPathInURLIsTolerated(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"data": []any{}})
	})
	_, err := client(f, func(in *config.Instance) { in.URL += "/api/v1/" }).ListWorkflows(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/workflows", f.last().Path)
}

func TestWorkflowActions(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/w1/activate":
			writeJSON(w, 200, map[string]any{"id": "w1", "active": true})
		case "/api/v1/workflows/w1/deactivate":
			writeJSON(w, 200, map[string]any{"id": "w1", "active": false})
		case "/api/v1/workflows/w1":
			writeJSON(w, 200, map[string]any{"id": "w1"})
		default:
			writeJSON(w, 404, map[string]string{"message": "not found"})
		}
	})
	c := client(f)
	ctx := context.Background()

	wf, err := c.SetWorkflowActive(ctx, "w1", true)
	require.NoError(t, err)
	assert.True(t, wf.Active)
	assert.Equal(t, http.MethodPost, f.last().Method)

	wf, err = c.SetWorkflowActive(ctx, "w1", false)
	require.NoError(t, err)
	assert.False(t, wf.Active)

	require.NoError(t, c.DeleteWorkflow(ctx, "w1"))
	assert.Equal(t, http.MethodDelete, f.last().Method)

	_, err = c.ActivateWorkflow(ctx, "missing")
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
	assert.Contains(t, err.Error(), "not found")
}

func TestErrorsCarryN8NMessage(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 401, map[string]string{"message": "'X-N8N-API-KEY' header required"})
	})
	_, err := client(f).ListWorkflows(context.Background())
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
	assert.False(t, IsUnsupported(err))
	assert.Contains(t, err.Error(), "X-N8N-API-KEY")
}

func TestListExecutionsQuery(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"data":       []map[string]any{{"id": 7, "workflowId": "w1", "status": "success", "finished": true}},
			"nextCursor": "next",
		})
	})
	page, err := client(f).ListExecutions(context.Background(), ExecutionFilter{
		WorkflowID: "w1", Status: models.StatusError, Limit: 999, Cursor: "abc",
	})
	require.NoError(t, err)
	assert.Equal(t, "next", page.NextCursor)
	assert.Equal(t, "7", page.Data[0].ID)

	q := f.last().RawQuery
	assert.Contains(t, q, "workflowId=w1")
	assert.Contains(t, q, "status=error")
	assert.Contains(t, q, "limit=250", "limit is capped at the API maximum")
	assert.Contains(t, q, "cursor=abc")
	assert.NotContains(t, q, "includeData")
}

func TestGetExecutionWithData(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/executions/42", r.URL.Path)
		assert.Equal(t, "true", r.URL.Query().Get("includeData"))
		_, _ = io.WriteString(w, `{"id":"42","status":"success","data":{"resultData":{"runData":{"Start":[{"startTime":1,"executionTime":2}]}}}}`)
	})
	ex, err := client(f).GetExecution(context.Background(), "42", true)
	require.NoError(t, err)
	assert.Len(t, ex.NodeRuns(), 1)
}

func TestRetryAndDeleteExecution(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/executions/5/retry" {
			writeJSON(w, 200, map[string]any{"id": 6, "retryOf": 5})
			return
		}
		w.WriteHeader(204)
	})
	c := client(f)
	ex, err := c.RetryExecution(context.Background(), "5", true)
	require.NoError(t, err)
	assert.Equal(t, "6", ex.ID)
	assert.Equal(t, "5", ex.RetryOf)
	assert.JSONEq(t, `{"loadWorkflow":true}`, f.last().Body)

	require.NoError(t, c.DeleteExecution(context.Background(), "5"))
}

func TestCredentialsAndVariables(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/credentials":
			writeJSON(w, 200, map[string]any{"data": []map[string]any{{"id": "c1", "name": "Slack", "type": "slackApi"}}})
		default:
			writeJSON(w, 403, map[string]string{"message": "Your license does not allow for feat:variables"})
		}
	})
	c := client(f)
	creds, err := c.ListCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "slackApi", creds[0].Type)

	_, err = c.ListVariables(context.Background())
	require.Error(t, err)
	assert.True(t, IsUnsupported(err))
}

func TestTriggerWebhook(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "yes", r.Header.Get("X-Custom"))
		writeJSON(w, 200, map[string]string{"message": "Workflow was started"})
	})
	c := client(f)
	req := WebhookRequest{Method: "post", Path: "/orders/new", Body: `{"a":1}`, Headers: map[string]string{"X-Custom": "yes"}}
	assert.Equal(t, f.URL+"/webhook/orders/new", c.WebhookURL(req))

	resp, err := c.TriggerWebhook(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "POST", resp.Method)
	assert.Contains(t, string(resp.Body), "started")

	r := f.last()
	assert.Equal(t, "/webhook/orders/new", r.Path)
	assert.Equal(t, `{"a":1}`, r.Body)
	assert.Empty(t, r.APIKey, "the API key must not leak to webhook endpoints")

	req.Test = true
	assert.Equal(t, f.URL+"/webhook-test/orders/new", c.WebhookURL(req))

	_, err = c.TriggerWebhook(context.Background(), WebhookRequest{})
	assert.Error(t, err)
}

func TestWebhookNon2xxIsNotAnError(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"message": "The requested webhook is not registered."})
	})
	resp, err := client(f).TriggerWebhook(context.Background(), WebhookRequest{Method: "GET", Path: "x", Body: "ignored"})
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
	assert.Empty(t, f.last().Body, "GET requests carry no body")
}

func TestWebhookURLOverride(t *testing.T) {
	c := New(config.Instance{URL: "https://n8n.example.com", WebhookURL: "https://hooks.example.com/"}, 0)
	assert.Equal(t, "https://hooks.example.com/webhook/p", c.WebhookURL(WebhookRequest{Path: "p"}))
}

func TestHealth(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			writeJSON(w, 200, map[string]string{"status": "ok"})
		case "/healthz/readiness":
			writeJSON(w, 503, map[string]string{"status": "error"})
		case "/rest/settings":
			writeJSON(w, 200, map[string]any{"data": map[string]any{"versionCli": "1.80.0", "executionMode": "queue"}})
		case "/api/v1/workflows":
			writeJSON(w, 200, map[string]any{"data": []any{}})
		case "/api/v1/executions":
			if r.URL.Query().Get("status") == "running" {
				writeJSON(w, 200, map[string]any{"data": []map[string]any{{"id": "1"}, {"id": "2"}}, "nextCursor": "more"})
				return
			}
			writeJSON(w, 200, map[string]any{"data": []map[string]any{{"id": "3"}}})
		}
	})
	h := client(f).Health(context.Background())
	assert.True(t, h.Healthy)
	assert.True(t, h.ReadyKnown)
	assert.False(t, h.Ready)
	assert.True(t, h.APIReachable)
	assert.Equal(t, "1.80.0", h.Version)
	assert.Equal(t, "queue", h.ExecutionMode)
	assert.Equal(t, 2, h.Running)
	assert.True(t, h.MoreRunning)
	assert.Equal(t, 1, h.Waiting)
	assert.False(t, h.MoreWaiting)
}

func TestHealthUnreachable(t *testing.T) {
	c := New(config.Instance{URL: "http://127.0.0.1:1"}, 500*time.Millisecond)
	h := c.Health(context.Background())
	assert.False(t, h.Healthy)
	assert.False(t, h.APIReachable)
	assert.NotEmpty(t, h.APIError)
}
