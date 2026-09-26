package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The token ledger (D83): what every chat spent, turn by turn — its agent's
// turns, the work its session did by itself between them, and the hidden
// prompts that compact and consolidate a project's chat. It is kept after the
// agent is gone, because "who spent my limit" is usually asked afterwards.
//
// Only chats are in it. A Claude Code started by hand in an agent's terminal
// talks to its provider directly, and AgentBox never sees what it spends.

// What a ledger line's tokens were spent on.
const (
	TokensTurn          = "turn"
	TokensBackground    = "background"
	TokensCompaction    = "compaction"
	TokensConsolidation = "consolidation"
)

// TokenCounts is spend in four counters that don't overlap, and the AI tool's
// own estimate of what they cost.
type TokenCounts struct {
	Input      int64 `json:"input"` // not read from the cache
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"` // all four
	// CostUSD is the tool's estimate at API prices. A subscription isn't
	// billed per token, but this weighs the four counters the way the
	// provider does, so it is the fairest single number to compare by.
	CostUSD float64 `json:"costUSD"`
}

// ModelTokens is one model's share of an agent's spend. Tokens add up by
// model; cost only roughly does, since a turn's whole cost is counted against
// the model that did most of it.
type ModelTokens struct {
	Model string `json:"model"`
	TokenCounts
	// AvgTPS is output tokens per second of generation, averaged over every
	// turn this model answered that timed itself; 0 when none did.
	AvgTPS float64 `json:"avgTPS,omitempty"`
}

// AgentTokens is one agent's spend over a report's stretch of time.
type AgentTokens struct {
	Project string `json:"project"`
	Agent   string `json:"agent"`
	Ref     string `json:"ref"`
	AI      string `json:"ai"`
	// Title is the agent's, while it exists.
	Title string `json:"title,omitempty"`
	// Exists is false once the agent has been retired or removed: its lines
	// stay in the ledger.
	Exists bool `json:"exists"`
	// Turns counts turns, background results and hidden prompts alike.
	Turns int64 `json:"turns"`
	// MaxContext is the fullest its context was seen when a turn ended: what
	// each model call of that turn was carrying.
	MaxContext int64     `json:"maxContext"`
	LastAt     time.Time `json:"lastAt"`
	TokenCounts
	// AvgTPS is output tokens per second of generation, averaged over every
	// turn of this agent's that timed itself; 0 when none did.
	AvgTPS float64       `json:"avgTPS,omitempty"`
	Models []ModelTokens `json:"models"` // busiest first
}

// TokenBucket is the ledger summed over one stretch of a report.
type TokenBucket struct {
	Start time.Time `json:"start"`
	TokenCounts
	// AvgTPS is TokenReport's own field, for this stretch alone.
	AvgTPS float64 `json:"avgTPS,omitempty"`
}

// TokenReport is the ledger summed over a stretch of time, for every project,
// one project, or one agent.
type TokenReport struct {
	Since *time.Time `json:"since,omitempty"` // nil for since the ledger began
	Until time.Time  `json:"until"`
	TokenCounts
	// AvgTPS is output tokens per second of generation, averaged over every
	// turn this report covers that timed itself; 0 when none did.
	AvgTPS float64       `json:"avgTPS,omitempty"`
	Agents []AgentTokens `json:"agents"` // most expensive first
	// Buckets is the spend over time, BucketSeconds each, oldest first. A
	// stretch nothing was spent in has no bucket.
	Buckets       []TokenBucket `json:"buckets"`
	BucketSeconds int64         `json:"bucketSeconds"`
}

// TokenTurn is one line of the ledger: what one model spent in one turn.
type TokenTurn struct {
	Project string    `json:"project"`
	Agent   string    `json:"agent"`
	AI      string    `json:"ai"`
	Session string    `json:"session,omitempty"`
	Turn    string    `json:"turn,omitempty"`
	Kind    string    `json:"kind"`
	Model   string    `json:"model,omitempty"`
	At      time.Time `json:"at"`
	// Context is how full the session's context was when the turn ended.
	Context int64 `json:"context"`
	// GenerationMS is how long this turn took to generate; 0 when nothing
	// timed it.
	GenerationMS int64 `json:"generationMS,omitempty"`
	TokenCounts
}

// TokenQuery says which part of the ledger to read. Every field is optional:
// Project "" is every project, Agent "" every agent of it, and Since zero is
// since the ledger began.
type TokenQuery struct {
	Project string
	Agent   string
	Since   time.Time
}

func (q TokenQuery) values() url.Values {
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
	return v
}

// Tokens sums the ledger.
func (c *Client) Tokens(ctx context.Context, q TokenQuery) (TokenReport, error) {
	var out TokenReport
	path := "/v1/tokens"
	if v := q.values(); len(v) > 0 {
		path += "?" + v.Encode()
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// TokenTurns lists the ledger itself, newest first, at most limit lines.
func (c *Client) TokenTurns(ctx context.Context, q TokenQuery, limit int) ([]TokenTurn, error) {
	var out []TokenTurn
	v := q.values()
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/tokens/turns"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
