// Package api is a client for the n8n public REST API (v1).
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/kaypee90/n8n-tui/internal/config"
)

const (
	apiPrefix     = "/api/v1"
	apiKeyHeader  = "X-N8N-API-KEY"
	userAgent     = "n8n-tui"
	pageSize      = 250 // n8n's maximum page size
	maxListResult = 5000
)

// Client talks to a single n8n instance.
type Client struct {
	instance config.Instance
	api      *resty.Client // rooted at <url>/api/v1, authenticated
	root     *resty.Client // rooted at <url>, for /healthz, /rest/settings, webhooks
}

// New returns a client for the given instance.
func New(in config.Instance, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = config.DefaultTimeout
	}
	mk := func(base string) *resty.Client {
		c := resty.New().
			SetBaseURL(base).
			SetTimeout(timeout).
			SetHeader("User-Agent", userAgent).
			SetHeader("Accept", "application/json")
		if in.Username != "" {
			c.SetBasicAuth(in.Username, in.Password)
		}
		if in.InsecureSkipVerify {
			c.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // opt-in via config
		}
		return c
	}

	api := mk(in.BaseURL() + apiPrefix)
	if in.APIKey != "" {
		api.SetHeader(apiKeyHeader, in.APIKey)
	}
	return &Client{instance: in, api: api, root: mk(in.BaseURL())}
}

// Instance returns the instance this client is connected to.
func (c *Client) Instance() config.Instance { return c.instance }

// Error is a non-2xx response from n8n.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from n8n.
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsUnsupported reports whether err indicates the endpoint or feature is not
// available on this instance (missing endpoint, unlicensed feature, ...).
func IsUnsupported(err error) bool {
	return statusIs(err, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusForbidden)
}

// IsUnauthorized reports whether err is an authentication failure.
func IsUnauthorized(err error) bool { return statusIs(err, http.StatusUnauthorized) }

func statusIs(err error, codes ...int) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	for _, c := range codes {
		if e.StatusCode == c {
			return true
		}
	}
	return false
}

// do executes a request and decodes a JSON response into out (may be nil).
func do(ctx context.Context, rc *resty.Client, method, path string, query map[string]string, body, out any) error {
	req := rc.R().SetContext(ctx)
	if len(query) > 0 {
		req.SetQueryParams(query)
	}
	if body != nil {
		req.SetHeader("Content-Type", "application/json").SetBody(body)
	}
	resp, err := req.Execute(method, path)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, unwrapTransport(err))
	}
	if resp.IsError() {
		return &Error{
			StatusCode: resp.StatusCode(),
			Method:     method,
			Path:       path,
			Message:    errorMessage(resp.Body()),
		}
	}
	if out == nil || len(resp.Body()) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Body(), out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
	}
	return nil
}

func unwrapTransport(err error) error {
	// resty wraps url.Error; keep the message short for the status bar.
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("request timed out")
	}
	return err
}

// errorMessage extracts n8n's {"message": "..."} error body when present.
func errorMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if strings.HasPrefix(s, "<") { // HTML error page from a proxy
		return ""
	}
	return s
}
