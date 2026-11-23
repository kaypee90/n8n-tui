// Package models contains the data types exchanged with the n8n public API (v1).
package models

import (
	"strings"
	"time"
)

// Tag is a workflow tag.
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Node is a single node inside a workflow definition.
type Node struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	TypeVersion float64        `json:"typeVersion"`
	Disabled    bool           `json:"disabled,omitempty"`
	WebhookID   string         `json:"webhookId,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Workflow is an n8n workflow as returned by GET /workflows.
type Workflow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Tags      []Tag     `json:"tags"`
	Nodes     []Node    `json:"nodes,omitempty"`
}

// Page is a cursor-paginated list response from the n8n public API.
type Page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"nextCursor"`
}

// TagNames returns the workflow's tag names joined by sep.
func (w Workflow) TagNames(sep string) string {
	names := make([]string, 0, len(w.Tags))
	for _, t := range w.Tags {
		names = append(names, t.Name)
	}
	return strings.Join(names, sep)
}

// WebhookTrigger describes how a workflow can be started over HTTP.
type WebhookTrigger struct {
	NodeName string
	Method   string
	Path     string
}

const webhookNodeType = "n8n-nodes-base.webhook"

// WebhookTriggers returns every enabled Webhook trigger node in the workflow.
//
// The n8n public API has no "run workflow" endpoint, so a Webhook node is the
// only way to start a workflow remotely.
func (w Workflow) WebhookTriggers() []WebhookTrigger {
	var out []WebhookTrigger
	for _, n := range w.Nodes {
		if n.Type != webhookNodeType || n.Disabled {
			continue
		}
		path, _ := n.Parameters["path"].(string)
		if path == "" {
			path = n.WebhookID
		}
		if path == "" {
			continue
		}
		method, _ := n.Parameters["httpMethod"].(string)
		if method == "" {
			method = "GET" // n8n's default for the Webhook node
		}
		out = append(out, WebhookTrigger{
			NodeName: n.Name,
			Method:   strings.ToUpper(method),
			Path:     strings.TrimPrefix(path, "/"),
		})
	}
	return out
}

// Matches reports whether the workflow matches a free-text filter
// (case-insensitive, against name, ID and tags).
func (w Workflow) Matches(filter string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	if strings.Contains(strings.ToLower(w.Name), f) || strings.Contains(strings.ToLower(w.ID), f) {
		return true
	}
	for _, t := range w.Tags {
		if strings.Contains(strings.ToLower(t.Name), f) {
			return true
		}
	}
	return false
}
