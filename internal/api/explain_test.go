package api

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kaypee90/n8n-tui/internal/config"
)

func TestExplainStatusCodes(t *testing.T) {
	cases := []struct {
		err      error
		summary  string
		hintPart string
	}{
		{&Error{StatusCode: 401, Path: "/workflows"}, "rejected the API key", "Settings → n8n API"},
		{&Error{StatusCode: 403, Path: "/credentials"}, "not allowed", "scopes"},
		{&Error{StatusCode: 404, Path: "/workflows"}, "public API was not found", "root URL"},
		{&Error{StatusCode: 404, Path: "/executions/9"}, "Not found", "refresh"},
		{&Error{StatusCode: 502, Path: "/workflows", Message: "Bad Gateway"}, "server error (502) (Bad Gateway)", "server logs"},
		{fmt.Errorf("GET /workflows: %w", errors.New("request timed out")), "did not respond in time", "timeout"},
		{errors.New("x509: certificate signed by unknown authority"), "not trusted", "insecure_skip_verify"},
	}
	for _, c := range cases {
		p := Explain(c.err)
		assert.Contains(t, p.Summary, c.summary, c.err.Error())
		assert.Contains(t, p.Hint, c.hintPart, c.err.Error())
	}
	assert.Equal(t, Problem{Summary: "weird"}, Explain(errors.New("weird")))
	assert.Equal(t, Problem{}, Explain(nil))
}

func TestExplainRealTransportErrors(t *testing.T) {
	// A closed port gives "connection refused".
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := New(config.Instance{Name: "x", URL: url, APIKey: "k"}, time.Second)
	_, err := c.ListWorkflows(context.Background())
	assert.Contains(t, Explain(err).Summary, "Connection refused")

	c = New(config.Instance{Name: "x", URL: "http://does-not-exist.invalid", APIKey: "k"}, 2*time.Second)
	_, err = c.ListWorkflows(context.Background())
	assert.Contains(t, Explain(err).Summary, "could not be found")
}

// opaqueErr hides the wrapped error's text, so only typed matching can find it.
type opaqueErr struct{ err error }

func (o opaqueErr) Error() string { return "handshake failed" }
func (o opaqueErr) Unwrap() error { return o.err }

func TestExplainUnknownAuthorityByType(t *testing.T) {
	err := opaqueErr{x509.UnknownAuthorityError{}}
	assert.Contains(t, Explain(err).Summary, "TLS certificate")
}
