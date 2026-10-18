package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Diagnostics: how long the daemon's work takes, where it stalled, and what
// the VM and its agents used over the last hour. Everything stays on the
// machine: spans and stalls are NDJSON files next to state.db (package trace),
// the resource history a ring in the daemon's memory. Nothing is exported.

// TraceSpan is one finished piece of work: an API request, a job, an Incus
// call, a git command, an ACP turn, a SQLite transaction, or one the desktop
// app sent. It is also a line of the span files, as written.
type TraceSpan struct {
	ID     string `json:"id,omitempty"` // always set in a file; a client may leave it out
	Parent string `json:"parent,omitempty"`
	Name   string `json:"name"`
	// Source is who measured it: "app" for the desktop app's spans, empty for
	// the daemon's own.
	Source     string         `json:"source,omitempty"`
	Start      time.Time      `json:"start"`
	DurationMS float64        `json:"durationMs"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	Error      string         `json:"error,omitempty"`
	// Level is "warn" for a stall, which is kept in the stall file as well.
	Level string `json:"level,omitempty"`
}

// SpanLevelWarn marks a stall.
const SpanLevelWarn = "warn"

// SpanSourceApp is the Source of the spans the desktop app sends.
const SpanSourceApp = "app"

// SpanStats is how long the spans of one name took.
type SpanStats struct {
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	Errors  int     `json:"errors"`
	P50MS   float64 `json:"p50Ms"`
	P95MS   float64 `json:"p95Ms"`
	MaxMS   float64 `json:"maxMs"`
	TotalMS float64 `json:"totalMs"`
}

// DiagnosticsSpans is GET /v1/diagnostics/spans: the spans that ended since
// Since, per name, busiest first (by total time), with the slowest and the
// latest failures among them.
type DiagnosticsSpans struct {
	Since    time.Time   `json:"since"`
	Stats    []SpanStats `json:"stats"`
	Slowest  []TraceSpan `json:"slowest"`
	Failures []TraceSpan `json:"failures"`
	// Dropped is how many spans this daemon couldn't write since it started,
	// because they came faster than the file took them.
	Dropped int64 `json:"dropped"`
}

// DiagnosticsStalls is GET /v1/diagnostics/stalls: newest first.
type DiagnosticsStalls struct {
	Since  time.Time   `json:"since"`
	Stalls []TraceSpan `json:"stalls"`
}

// VMResourceSample is what the VM used at one sample of the usage loop.
type VMResourceSample struct {
	At        time.Time     `json:"at"`
	CPU       float64       `json:"cpu"` // percent of all cores
	Cores     int           `json:"cores"`
	MemUsed   int64         `json:"memUsed"`
	MemTotal  int64         `json:"memTotal"`
	DiskRead  int64         `json:"diskRead"`
	DiskWrite int64         `json:"diskWrite"`
	Pressure  *HostPressure `json:"pressure,omitempty"`
}

// AgentResourceSample is what one agent used at one sample.
type AgentResourceSample struct {
	At        time.Time `json:"at"`
	State     string    `json:"state"`
	CPU       float64   `json:"cpu"` // percent; 100 is one full core
	Memory    int64     `json:"memory"`
	Processes int64     `json:"processes"`
	DiskRead  int64     `json:"diskRead"`
	DiskWrite int64     `json:"diskWrite"`
}

// AgentResources is one agent's samples, oldest first.
type AgentResources struct {
	Ref     string                `json:"ref"`
	Samples []AgentResourceSample `json:"samples"`
}

// DiagnosticsResources is GET /v1/diagnostics/resources: the usage loop's
// samples in the window, oldest first, kept for an hour.
type DiagnosticsResources struct {
	Since           time.Time          `json:"since"`
	IntervalSeconds int                `json:"intervalSeconds"`
	VM              []VMResourceSample `json:"vm"`
	Agents          []AgentResources   `json:"agents"`
}

// ClientSpansRequest is POST /v1/diagnostics/client-spans: spans the desktop
// app measured (long tasks, request round trips), stored with Source "app".
// At most MaxClientSpans a batch.
type ClientSpansRequest struct {
	Spans []TraceSpan `json:"spans"`
}

// MaxClientSpans is the most spans one ClientSpansRequest may carry.
const MaxClientSpans = 500

// DiagnosticsSpans asks for the span summary over the last since (e.g. 30m).
func (c *Client) DiagnosticsSpans(ctx context.Context, since time.Duration) (DiagnosticsSpans, error) {
	var out DiagnosticsSpans
	err := c.do(ctx, http.MethodGet, "/v1/diagnostics/spans?since="+url.QueryEscape(since.String()), nil, &out)
	return out, err
}

// DiagnosticsStalls asks for the stalls over the last since.
func (c *Client) DiagnosticsStalls(ctx context.Context, since time.Duration) (DiagnosticsStalls, error) {
	var out DiagnosticsStalls
	err := c.do(ctx, http.MethodGet, "/v1/diagnostics/stalls?since="+url.QueryEscape(since.String()), nil, &out)
	return out, err
}

// DiagnosticsResources asks for the resource samples over the last window.
func (c *Client) DiagnosticsResources(ctx context.Context, window time.Duration) (DiagnosticsResources, error) {
	var out DiagnosticsResources
	err := c.do(ctx, http.MethodGet, "/v1/diagnostics/resources?window="+url.QueryEscape(window.String()), nil, &out)
	return out, err
}

// SendClientSpans stores spans measured by a client.
func (c *Client) SendClientSpans(ctx context.Context, spans []TraceSpan) error {
	return c.do(ctx, http.MethodPost, "/v1/diagnostics/client-spans", ClientSpansRequest{Spans: spans}, nil)
}
