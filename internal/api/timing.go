package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Timing: where agents' time goes, GET /v1/timing. Every chat turn is timed
// as it ends — how long the model took, the tools, and the rest — and so is
// every tool call in it and every heavy command (a test run, a build) an
// agent runs. Durations are milliseconds.

// TimingQuery says which timings to read. Every field is optional: Project
// "" is every project, Agent "" every agent (an agent name alone, every
// project's agent of that name), Since zero since the oldest kept, and Top
// how many of the slowest tools, calls and commands to list (10 when 0).
type TimingQuery struct {
	Project string
	Agent   string
	Since   time.Time
	Top     int
}

func (q TimingQuery) values() url.Values {
	v := url.Values{}
	if q.Project != "" {
		v.Set("project", q.Project)
	}
	if q.Agent != "" {
		v.Set("agent", q.Agent)
	}
	if !q.Since.IsZero() {
		v.Set("since", q.Since.UTC().Format(time.RFC3339))
	}
	if q.Top > 0 {
		v.Set("top", strconv.Itoa(q.Top))
	}
	return v
}

// Timing reads where agents' time went.
func (c *Client) Timing(ctx context.Context, q TimingQuery) (TimingReport, error) {
	var out TimingReport
	path := "/v1/timing"
	if v := q.values(); len(v) > 0 {
		path += "?" + v.Encode()
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// TimingTotals adds up chat turns and heavy commands.
type TimingTotals struct {
	Turns int64 `json:"turns"`
	// Answered is how many of the turns the model answered at all:
	// FirstTokenMS averages over them.
	Answered int64 `json:"answered"`
	// TurnMS is the turns' wall time, from the message to the end.
	TurnMS int64 `json:"turnMS"`
	// StartMS is the part of it from the message to the prompt reaching the
	// AI tool: its adapter starting, or carrying on after a restart.
	StartMS int64 `json:"startMS"`
	// ModelMS is the time waiting on the model, from each request (the
	// prompt, a tool's result) to its last token, over ModelCalls
	// requests. FirstTokenMS is the wait for each turn's first token, and
	// TTFTMS for each request's.
	ModelMS      int64 `json:"modelMS"`
	ModelCalls   int64 `json:"modelCalls"`
	FirstTokenMS int64 `json:"firstTokenMS"`
	TTFTMS       int64 `json:"ttftMS"`
	// ToolMS is the time at least one of a turn's own tool calls ran, over
	// ToolCalls calls: calls in parallel count once, and a subagent's own
	// are inside its Agent call.
	ToolMS    int64 `json:"toolMS"`
	ToolCalls int64 `json:"toolCalls"`
	// Commands are the heavy commands run, in CommandMS, of which Failed
	// exited non-zero. They run inside tool calls or a terminal, so their
	// time isn't apart from ToolMS.
	Commands       int64 `json:"commands"`
	CommandMS      int64 `json:"commandMS"`
	FailedCommands int64 `json:"failedCommands"`
}

// AgentTiming is one agent's timings added up.
type AgentTiming struct {
	Ref string `json:"ref"`
	TimingTotals
	LastAt time.Time `json:"lastAt"`
}

// ToolTiming is one tool's calls added up, by who made them.
type ToolTiming struct {
	Name string `json:"name"`
	// Subagent made these calls, "" for the main loop.
	Subagent string `json:"subagent,omitempty"`
	Calls    int64  `json:"calls"`
	Failed   int64  `json:"failed"`
	TotalMS  int64  `json:"totalMS"`
	MaxMS    int64  `json:"maxMS"`
}

// TimedCall is one tool call.
type TimedCall struct {
	Agent    string    `json:"agent"` // its ref
	Turn     string    `json:"turn"`
	Name     string    `json:"name"`
	Title    string    `json:"title,omitempty"`
	Subagent string    `json:"subagent,omitempty"`
	Status   string    `json:"status"` // completed, failed or stopped
	At       time.Time `json:"at"`
	MS       int64     `json:"ms"`
}

// TimedCommand is one heavy command.
type TimedCommand struct {
	Agent   string `json:"agent"` // its ref
	Command string `json:"command"`
	// Jobs is the worker count the daemon gave the command
	// (HeavyStart.Workers), 0 when none (a daemon that gave none).
	Jobs int `json:"jobs"`
	// ExitCode is -1 when the command didn't say.
	ExitCode int       `json:"exitCode"`
	At       time.Time `json:"at"`
	MS       int64     `json:"ms"`
}

// TimingReport is where agents' time went: in total, by agent (busiest
// first), by tool (the most time first), and the slowest tool calls and
// heavy commands.
type TimingReport struct {
	Since *time.Time `json:"since,omitempty"`
	Until time.Time  `json:"until"`
	TimingTotals
	Agents       []AgentTiming  `json:"agents"`
	Tools        []ToolTiming   `json:"tools"`
	SlowCalls    []TimedCall    `json:"slowCalls"`
	SlowCommands []TimedCommand `json:"slowCommands"`
}

// HeavyEnd is what a heavy command says as it ends (DELETE
// /v1/self/heavy/{key}), for its timing. A body-less DELETE, from an
// agentbox before this, is timed from when the daemon heard it start.
type HeavyEnd struct {
	Command    string `json:"command,omitempty"`
	Jobs       int    `json:"jobs,omitempty"` // the HeavyStart.Workers the daemon gave it, 0 when none
	ExitCode   int    `json:"exitCode"`
	DurationMS int64  `json:"durationMS"`
}
