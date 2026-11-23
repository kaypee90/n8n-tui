package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const executionFixture = `{
  "id": 1234,
  "finished": false,
  "mode": "webhook",
  "startedAt": "2025-01-02T10:00:00.000Z",
  "stoppedAt": "2025-01-02T10:00:02.500Z",
  "workflowId": "wf1",
  "status": "error",
  "data": {
    "resultData": {
      "lastNodeExecuted": "HTTP Request",
      "error": {"message": "Request failed with status code 500", "node": {"name": "HTTP Request"}},
      "runData": {
        "HTTP Request": [{
          "startTime": 1735812001000,
          "executionTime": 1200,
          "executionStatus": "error",
          "error": {"message": "Request failed with status code 500", "description": "upstream down"}
        }],
        "Webhook": [{
          "startTime": 1735812000000,
          "executionTime": 3,
          "executionStatus": "success",
          "data": {"main": [[{"json": {"body": {"a": 1}}}, {"json": {"body": {"a": 2}}}]]}
        }],
        "Loop": [
          {"startTime": 1735812000500, "executionTime": 1, "data": {"main": [[{"json": {"i": 0}}], []]}},
          {"startTime": 1735812000600, "executionTime": 1, "data": {"main": [[{"json": {"i": 1}}]]}}
        ]
      }
    }
  }
}`

func TestExecutionDecodingAndNodeRuns(t *testing.T) {
	var ex Execution
	require.NoError(t, json.Unmarshal([]byte(executionFixture), &ex))

	assert.Equal(t, "1234", ex.ID, "numeric ids are normalised to strings")
	assert.Equal(t, "wf1", ex.WorkflowID)
	assert.Equal(t, StatusError, ex.EffectiveStatus())
	assert.Equal(t, 2500*time.Millisecond, ex.Duration(time.Now()))

	runs := ex.NodeRuns()
	require.Len(t, runs, 4)
	names := []string{runs[0].Node, runs[1].Node, runs[2].Node, runs[3].Node}
	assert.Equal(t, []string{"Webhook", "Loop", "Loop", "HTTP Request"}, names, "ordered by start time")

	assert.Equal(t, 2, runs[0].Items)
	assert.JSONEq(t, `{"body":{"a":1}}`, string(runs[0].Output[0]))
	assert.Equal(t, StatusSuccess, runs[1].Status, "status defaults to success without error")
	assert.Equal(t, 1, runs[2].RunIndex)

	failed := runs[3]
	assert.Equal(t, StatusError, failed.Status)
	require.NotNil(t, failed.Error)
	assert.Equal(t, "upstream down", failed.Error.Description)
	assert.Equal(t, 1200*time.Millisecond, failed.Duration)
}

func TestEffectiveStatusFallbacks(t *testing.T) {
	now := time.Now()
	assert.Equal(t, StatusRunning, Execution{StartedAt: &now}.EffectiveStatus())
	assert.Equal(t, StatusSuccess, Execution{StoppedAt: &now, Finished: true}.EffectiveStatus())
	assert.Equal(t, StatusError, Execution{StoppedAt: &now}.EffectiveStatus())
	assert.Equal(t, StatusWaiting, Execution{WaitTill: &now}.EffectiveStatus())
	assert.True(t, StatusCanceled.IsTerminal())
	assert.False(t, StatusWaiting.IsTerminal())
}

func TestDurationOfRunningExecution(t *testing.T) {
	start := time.Now().Add(-3 * time.Second)
	ex := Execution{StartedAt: &start}
	assert.InDelta(t, 3*time.Second, ex.Duration(time.Now()), float64(100*time.Millisecond))
	assert.Zero(t, Execution{}.Duration(time.Now()))
}

func TestWebhookTriggers(t *testing.T) {
	w := Workflow{Nodes: []Node{
		{Name: "Hook", Type: "n8n-nodes-base.webhook", Parameters: map[string]any{"path": "/orders", "httpMethod": "post"}},
		{Name: "Default", Type: "n8n-nodes-base.webhook", WebhookID: "abc-123"},
		{Name: "Off", Type: "n8n-nodes-base.webhook", Disabled: true, Parameters: map[string]any{"path": "x"}},
		{Name: "Cron", Type: "n8n-nodes-base.scheduleTrigger"},
	}}
	trg := w.WebhookTriggers()
	require.Len(t, trg, 2)
	assert.Equal(t, WebhookTrigger{NodeName: "Hook", Method: "POST", Path: "orders"}, trg[0])
	assert.Equal(t, WebhookTrigger{NodeName: "Default", Method: "GET", Path: "abc-123"}, trg[1])
}

func TestWorkflowMatchesAndTags(t *testing.T) {
	w := Workflow{ID: "AbC", Name: "Sync Orders", Tags: []Tag{{Name: "billing"}, {Name: "prod"}}}
	assert.True(t, w.Matches(""))
	assert.True(t, w.Matches("orders"))
	assert.True(t, w.Matches("BILL"))
	assert.True(t, w.Matches("abc"))
	assert.False(t, w.Matches("crm"))
	assert.Equal(t, "billing, prod", w.TagNames(", "))
}
