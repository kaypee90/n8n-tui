package models

import (
	"encoding/json"
	"sort"
	"time"
)

// ExecutionStatus mirrors n8n's execution status values.
type ExecutionStatus string

const (
	StatusSuccess  ExecutionStatus = "success"
	StatusError    ExecutionStatus = "error"
	StatusCrashed  ExecutionStatus = "crashed"
	StatusRunning  ExecutionStatus = "running"
	StatusWaiting  ExecutionStatus = "waiting"
	StatusCanceled ExecutionStatus = "canceled"
	StatusNew      ExecutionStatus = "new"
	StatusUnknown  ExecutionStatus = "unknown"
)

// FilterableStatuses are the values accepted by GET /executions?status=.
var FilterableStatuses = []ExecutionStatus{StatusSuccess, StatusError, StatusRunning, StatusWaiting, StatusCanceled}

// IsTerminal reports whether the status will not change any more.
func (s ExecutionStatus) IsTerminal() bool {
	switch s {
	case StatusSuccess, StatusError, StatusCrashed, StatusCanceled:
		return true
	}
	return false
}

// Execution is a single workflow run.
type Execution struct {
	ID             string          `json:"id"`
	Finished       bool            `json:"finished"`
	Mode           string          `json:"mode"`
	RetryOf        string          `json:"retryOf,omitempty"`
	RetrySuccessID string          `json:"retrySuccessId,omitempty"`
	StartedAt      *time.Time      `json:"startedAt"`
	StoppedAt      *time.Time      `json:"stoppedAt"`
	WaitTill       *time.Time      `json:"waitTill,omitempty"`
	WorkflowID     string          `json:"workflowId"`
	Status         ExecutionStatus `json:"status"`
	Data           *ExecutionData  `json:"data,omitempty"`
}

// UnmarshalJSON accepts numeric or string IDs (n8n has returned both).
func (e *Execution) UnmarshalJSON(b []byte) error {
	type alias Execution
	aux := struct {
		ID         json.RawMessage `json:"id"`
		WorkflowID json.RawMessage `json:"workflowId"`
		RetryOf    json.RawMessage `json:"retryOf"`
		*alias
	}{alias: (*alias)(e)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	e.ID = flexString(aux.ID)
	e.WorkflowID = flexString(aux.WorkflowID)
	e.RetryOf = flexString(aux.RetryOf)
	return nil
}

func flexString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return string(raw)
}

// EffectiveStatus derives a status for older n8n versions that omit it.
func (e Execution) EffectiveStatus() ExecutionStatus {
	if e.Status != "" {
		return e.Status
	}
	switch {
	case e.WaitTill != nil:
		return StatusWaiting
	case e.StoppedAt == nil:
		return StatusRunning
	case e.Finished:
		return StatusSuccess
	default:
		return StatusError
	}
}

// Duration returns how long the execution ran. For a running execution it is
// measured against now.
func (e Execution) Duration(now time.Time) time.Duration {
	if e.StartedAt == nil {
		return 0
	}
	end := now
	if e.StoppedAt != nil {
		end = *e.StoppedAt
	}
	if end.Before(*e.StartedAt) {
		return 0
	}
	return end.Sub(*e.StartedAt)
}

// ExecutionData is the payload returned with includeData=true.
type ExecutionData struct {
	ResultData struct {
		RunData          map[string][]TaskData `json:"runData"`
		LastNodeExecuted string                `json:"lastNodeExecuted"`
		Error            *ExecutionError       `json:"error,omitempty"`
	} `json:"resultData"`
}

// TaskData is one run of one node.
type TaskData struct {
	StartTime       int64                                     `json:"startTime"` // unix millis
	ExecutionTime   int64                                     `json:"executionTime"`
	ExecutionStatus string                                    `json:"executionStatus,omitempty"`
	Data            map[string][][]map[string]json.RawMessage `json:"data,omitempty"`
	Error           *ExecutionError                           `json:"error,omitempty"`
}

// ExecutionError is an error recorded by n8n for a node or execution.
type ExecutionError struct {
	Message     string `json:"message"`
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	Stack       string `json:"stack,omitempty"`
	Node        *struct {
		Name string `json:"name"`
	} `json:"node,omitempty"`
}

// NodeRun is a flattened, display-friendly view of a node's run.
type NodeRun struct {
	Node      string
	RunIndex  int
	StartedAt time.Time
	Duration  time.Duration
	Status    ExecutionStatus
	Items     int
	Output    []json.RawMessage // the "json" field of every output item
	Error     *ExecutionError
}

// NodeRuns returns every node run ordered by start time.
func (e Execution) NodeRuns() []NodeRun {
	if e.Data == nil {
		return nil
	}
	var runs []NodeRun
	for name, tasks := range e.Data.ResultData.RunData {
		for i, t := range tasks {
			runs = append(runs, t.toNodeRun(name, i))
		}
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.Before(runs[j].StartedAt)
		}
		if runs[i].Node != runs[j].Node {
			return runs[i].Node < runs[j].Node
		}
		return runs[i].RunIndex < runs[j].RunIndex
	})
	return runs
}

func (t TaskData) toNodeRun(name string, idx int) NodeRun {
	r := NodeRun{
		Node:      name,
		RunIndex:  idx,
		StartedAt: time.UnixMilli(t.StartTime),
		Duration:  time.Duration(t.ExecutionTime) * time.Millisecond,
		Error:     t.Error,
	}
	switch {
	case t.ExecutionStatus != "":
		r.Status = ExecutionStatus(t.ExecutionStatus)
	case t.Error != nil:
		r.Status = StatusError
	default:
		r.Status = StatusSuccess
	}
	// Data is keyed by connection type ("main", "ai_tool", ...), then output
	// index, then item.
	keys := make([]string, 0, len(t.Data))
	for k := range t.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, output := range t.Data[k] {
			for _, item := range output {
				r.Items++
				if j, ok := item["json"]; ok {
					r.Output = append(r.Output, j)
				}
			}
		}
	}
	return r
}
