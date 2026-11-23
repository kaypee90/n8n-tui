package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/kaypee90/n8n-tui/internal/models"
)

// Health probes the instance. It never returns an error: every probe result
// is reported in the returned struct so partial information can be shown.
func (c *Client) Health(ctx context.Context) models.Health {
	h := models.Health{CheckedAt: time.Now()}
	var wg sync.WaitGroup
	var mu sync.Mutex
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}

	run(func() {
		start := time.Now()
		resp, err := c.root.R().SetContext(ctx).Get("/healthz")
		mu.Lock()
		defer mu.Unlock()
		h.Latency = time.Since(start)
		switch {
		case err != nil:
			h.HealthError = unwrapTransport(err).Error()
		case resp.StatusCode() != http.StatusOK:
			h.HealthError = resp.Status()
		default:
			h.Healthy = true
		}
	})

	run(func() {
		// Returns 200 only when the DB is connected and migrated (and Redis
		// in queue mode).
		resp, err := c.root.R().SetContext(ctx).Get("/healthz/readiness")
		mu.Lock()
		defer mu.Unlock()
		if err == nil && resp.StatusCode() != http.StatusNotFound {
			h.ReadyKnown = true
			h.Ready = resp.StatusCode() == http.StatusOK
		}
	})

	run(func() {
		// /rest/settings is the editor's bootstrap endpoint; it is not part
		// of the public API and may be locked down, so failures are ignored.
		resp, err := c.root.R().SetContext(ctx).Get("/rest/settings")
		if err != nil || resp.IsError() {
			return
		}
		var s struct {
			Data struct {
				VersionCli    string `json:"versionCli"`
				ExecutionMode string `json:"executionMode"`
			} `json:"data"`
		}
		if json.Unmarshal(resp.Body(), &s) == nil {
			mu.Lock()
			h.Version = s.Data.VersionCli
			h.ExecutionMode = s.Data.ExecutionMode
			mu.Unlock()
		}
	})

	run(func() {
		var page struct {
			Data []json.RawMessage `json:"data"`
		}
		err := do(ctx, c.api, http.MethodGet, "/workflows", map[string]string{"limit": "1"}, nil, &page)
		mu.Lock()
		defer mu.Unlock()
		h.APIReachable = err == nil
		if err != nil {
			h.APIError = err.Error()
		}
	})

	for _, st := range []struct {
		status models.ExecutionStatus
		count  *int
		more   *bool
	}{
		{models.StatusRunning, &h.Running, &h.MoreRunning},
		{models.StatusWaiting, &h.Waiting, &h.MoreWaiting},
	} {
		run(func() {
			page, err := c.ListExecutions(ctx, ExecutionFilter{Status: st.status, Limit: 100})
			if err != nil {
				return
			}
			mu.Lock()
			*st.count = len(page.Data)
			*st.more = page.NextCursor != ""
			mu.Unlock()
		})
	}

	wg.Wait()
	return h
}
