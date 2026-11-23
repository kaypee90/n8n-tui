package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/kaypee90/n8n-tui/internal/models"
)

// ExecutionFilter narrows GET /executions.
type ExecutionFilter struct {
	WorkflowID  string
	Status      models.ExecutionStatus
	Limit       int
	Cursor      string
	IncludeData bool
}

func (f ExecutionFilter) query() map[string]string {
	q := map[string]string{}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > pageSize {
		limit = pageSize
	}
	q["limit"] = strconv.Itoa(limit)
	if f.WorkflowID != "" {
		q["workflowId"] = f.WorkflowID
	}
	if f.Status != "" {
		q["status"] = string(f.Status)
	}
	if f.Cursor != "" {
		q["cursor"] = f.Cursor
	}
	if f.IncludeData {
		q["includeData"] = "true"
	}
	return q
}

// ListExecutions returns a single page of executions, newest first.
func (c *Client) ListExecutions(ctx context.Context, f ExecutionFilter) (models.Page[models.Execution], error) {
	var page models.Page[models.Execution]
	err := do(ctx, c.api, http.MethodGet, "/executions", f.query(), nil, &page)
	return page, err
}

// GetExecution returns a single execution, optionally with node run data.
func (c *Client) GetExecution(ctx context.Context, id string, includeData bool) (*models.Execution, error) {
	var e models.Execution
	q := map[string]string{}
	if includeData {
		q["includeData"] = "true"
	}
	if err := do(ctx, c.api, http.MethodGet, "/executions/"+url.PathEscape(id), q, nil, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// DeleteExecution deletes an execution.
func (c *Client) DeleteExecution(ctx context.Context, id string) error {
	return do(ctx, c.api, http.MethodDelete, "/executions/"+url.PathEscape(id), nil, nil, nil)
}

// RetryExecution retries a failed execution (n8n >= 1.83).
func (c *Client) RetryExecution(ctx context.Context, id string, loadWorkflow bool) (*models.Execution, error) {
	var e models.Execution
	body := map[string]bool{"loadWorkflow": loadWorkflow}
	if err := do(ctx, c.api, http.MethodPost, "/executions/"+url.PathEscape(id)+"/retry", nil, body, &e); err != nil {
		return nil, err
	}
	return &e, nil
}
