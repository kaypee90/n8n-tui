package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kaypee90/n8n-tui/internal/models"
)

// WebhookRequest describes a webhook call.
type WebhookRequest struct {
	Method string
	// Path is the webhook path as configured on the Webhook node, without the
	// /webhook/ prefix.
	Path string
	// Test targets /webhook-test/ (the editor must be listening) instead of
	// the production /webhook/ endpoint (the workflow must be active).
	Test    bool
	Body    string
	Headers map[string]string
}

// WebhookURL returns the full URL a request will be sent to.
func (c *Client) WebhookURL(req WebhookRequest) string {
	prefix := "/webhook/"
	if req.Test {
		prefix = "/webhook-test/"
	}
	return c.instance.WebhookBaseURL() + prefix + strings.TrimPrefix(req.Path, "/")
}

// TriggerWebhook calls an n8n webhook. Non-2xx responses are returned as a
// normal response rather than an error so the caller can show them.
func (c *Client) TriggerWebhook(ctx context.Context, req WebhookRequest) (*models.WebhookResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodPost
	}
	if strings.TrimSpace(req.Path) == "" {
		return nil, fmt.Errorf("webhook path is required")
	}
	target := c.WebhookURL(req)

	r := c.root.R().SetContext(ctx).SetHeader("Accept", "*/*")
	for k, v := range req.Headers {
		r.SetHeader(k, v)
	}
	if req.Body != "" && method != http.MethodGet && method != http.MethodHead {
		if _, ok := req.Headers["Content-Type"]; !ok {
			r.SetHeader("Content-Type", "application/json")
		}
		r.SetBody(req.Body)
	}

	start := time.Now()
	resp, err := r.Execute(method, target)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, target, unwrapTransport(err))
	}
	return &models.WebhookResponse{
		URL:        target,
		Method:     method,
		StatusCode: resp.StatusCode(),
		Status:     resp.Status(),
		Headers:    resp.Header(),
		Body:       resp.Body(),
		Duration:   time.Since(start),
	}, nil
}
