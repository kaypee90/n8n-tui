package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/kaypee90/n8n-tui/internal/models"
)

// ListWorkflows returns all workflows, following pagination up to a safety cap.
// Node definitions are included so webhook triggers can be discovered.
func (c *Client) ListWorkflows(ctx context.Context) ([]models.Workflow, error) {
	var all []models.Workflow
	cursor := ""
	for {
		q := map[string]string{"limit": strconv.Itoa(pageSize)}
		if cursor != "" {
			q["cursor"] = cursor
		}
		var page models.Page[models.Workflow]
		if err := do(ctx, c.api, http.MethodGet, "/workflows", q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == "" || len(all) >= maxListResult {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// GetWorkflow returns a single workflow including its nodes.
func (c *Client) GetWorkflow(ctx context.Context, id string) (*models.Workflow, error) {
	var w models.Workflow
	if err := do(ctx, c.api, http.MethodGet, "/workflows/"+url.PathEscape(id), nil, nil, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

// ActivateWorkflow enables a workflow.
func (c *Client) ActivateWorkflow(ctx context.Context, id string) (*models.Workflow, error) {
	return c.workflowAction(ctx, id, "activate")
}

// DeactivateWorkflow disables a workflow.
func (c *Client) DeactivateWorkflow(ctx context.Context, id string) (*models.Workflow, error) {
	return c.workflowAction(ctx, id, "deactivate")
}

// SetWorkflowActive activates or deactivates a workflow.
func (c *Client) SetWorkflowActive(ctx context.Context, id string, active bool) (*models.Workflow, error) {
	if active {
		return c.ActivateWorkflow(ctx, id)
	}
	return c.DeactivateWorkflow(ctx, id)
}

func (c *Client) workflowAction(ctx context.Context, id, action string) (*models.Workflow, error) {
	var w models.Workflow
	path := "/workflows/" + url.PathEscape(id) + "/" + action
	if err := do(ctx, c.api, http.MethodPost, path, nil, nil, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

// DeleteWorkflow permanently deletes a workflow.
func (c *Client) DeleteWorkflow(ctx context.Context, id string) error {
	return do(ctx, c.api, http.MethodDelete, "/workflows/"+url.PathEscape(id), nil, nil, nil)
}
