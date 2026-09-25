package api

import (
	"context"
	"net/http"
	"time"
)

// ClaudeLimit is what Anthropic last said about one Claude account's usage
// limits (D85): the five-hour window, the weekly ones, and when each starts
// again. Claude Code learns it from the responses it gets and claude-agent-acp
// relays it on a chat's usage_update, so it is only as fresh as the account's
// last chat: At says when that was.
type ClaudeLimit struct {
	Account string    `json:"account"`
	Default bool      `json:"default"`
	At      time.Time `json:"at"`
	// Status is Anthropic's word for the account then: allowed,
	// allowed_warning when a limit is close, or rejected when one is spent.
	Status string `json:"status"`
	// UsingOverage says requests were being billed past the plan's limits.
	UsingOverage bool                `json:"usingOverage,omitempty"`
	Windows      []ClaudeLimitWindow `json:"windows"`
}

// ClaudeLimitWindow is one of an account's limits.
type ClaudeLimitWindow struct {
	// Name is Anthropic's: five_hour, seven_day, seven_day_opus,
	// seven_day_sonnet, or one added since.
	Name  string `json:"name"`
	Label string `json:"label"` // how to say it: "5-hour", "Weekly", "Weekly (Opus)"
	// Utilization is how much of it was used, 0 to 1, as of the reading. Once
	// ResetsAt has passed the window has started again, and the reading says
	// nothing about it.
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// ClaudeLimits lists the newest reading for each stored Claude account that
// has had one.
func (c *Client) ClaudeLimits(ctx context.Context) ([]ClaudeLimit, error) {
	var out []ClaudeLimit
	return out, c.do(ctx, http.MethodGet, "/v1/limits", nil, &out)
}

// LeadAccount is one of this machine's Claude Code accounts, as a project's
// chat needs it to spread its agents: never a token, only which one is the
// machine's default or this project's own, how many of this project's running
// agents already use it, and its latest usage reading, if it has had one
// (D88).
type LeadAccount struct {
	Name string `json:"name"`
	// Default is whether this is the machine's default account, used when
	// nothing else says otherwise.
	Default bool `json:"default"`
	// Project is whether this is the account this project's agents use when
	// they don't choose one of their own.
	Project bool `json:"project"`
	// Agents is how many of this project's running Claude Code agents hold
	// this account's token right now.
	Agents int `json:"agents"`
	// Limit is its latest reading, or nil when it has never had one.
	Limit *ClaudeLimit `json:"limit,omitempty"`
}
