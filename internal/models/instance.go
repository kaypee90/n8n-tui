package models

import "time"

// Credential is the metadata n8n exposes for a credential. Secret data is
// never returned by the API.
type Credential struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Variable is an n8n environment variable (GET /variables).
type Variable struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// Health summarises the state of an n8n instance.
type Health struct {
	Healthy       bool // GET /healthz
	HealthError   string
	Ready         bool // GET /healthz/readiness (DB connected + migrated)
	ReadyKnown    bool // readiness endpoint responded at all
	APIReachable  bool // authenticated public API call succeeded
	APIError      string
	Version       string        // from /rest/settings when exposed
	ExecutionMode string        // "regular" or "queue"
	Latency       time.Duration // round trip of the health check
	Running       int           // running executions (first page)
	Waiting       int           // waiting executions (first page)
	MoreRunning   bool
	MoreWaiting   bool
	CheckedAt     time.Time
}

// WebhookResponse is the result of calling a webhook.
type WebhookResponse struct {
	URL        string
	Method     string
	StatusCode int
	Status     string
	Headers    map[string][]string
	Body       []byte
	Duration   time.Duration
}
