package api

import (
	"context"
	"net/http"
	"time"
)

// GitHubUsage is what each GitHub token was used for since the daemon
// started, and what GitHub last said is left of it.
type GitHubUsage struct {
	Accounts []GitHubAccountUsage `json:"accounts"`
}

// GitHubAccountUsage is one token's requests. The token shows only as a
// fingerprint; Account names the stored account it is, when one is.
type GitHubAccountUsage struct {
	Token   string            `json:"token"`
	Account string            `json:"account,omitempty"`
	Login   string            `json:"login,omitempty"`
	Since   time.Time         `json:"since"`
	Rates   []GitHubRate      `json:"rates"`
	Calls   []GitHubCallCount `json:"calls"`
}

// GitHubRate is what GitHub last said of one of a token's hourly budgets:
// core (REST requests) or graphql (points). For core, Ours is what AgentBox's
// own requests made of Used since the last reset, and Others the rest: the
// agents' own gh, and anything else using the token.
type GitHubRate struct {
	Resource    string     `json:"resource"`
	Limit       int        `json:"limit"`
	Remaining   int        `json:"remaining"`
	Used        int        `json:"used"`
	Reset       time.Time  `json:"reset"`
	At          time.Time  `json:"at"`
	Ours        int        `json:"ours,omitempty"`
	Others      int        `json:"others,omitempty"`
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

// GitHubCallCount is how many requests one part of AgentBox made of one
// endpoint. Counted is what GitHub held against the budget: Sent less the
// 304s. Shared reads joined one in flight, and Held ones were not sent
// because the budget was spent or nearly.
type GitHubCallCount struct {
	Caller      string    `json:"caller"`
	Method      string    `json:"method"`
	Endpoint    string    `json:"endpoint"`
	Resource    string    `json:"resource"`
	Sent        int       `json:"sent"`
	Counted     int       `json:"counted"`
	NotModified int       `json:"notModified,omitempty"`
	Shared      int       `json:"shared,omitempty"`
	Held        int       `json:"held,omitempty"`
	Failed      int       `json:"failed,omitempty"`
	Last        time.Time `json:"last"`
}

// GitHubUsage reads what each GitHub token was used for.
func (c *Client) GitHubUsage(ctx context.Context) (GitHubUsage, error) {
	var out GitHubUsage
	return out, c.do(ctx, http.MethodGet, "/v1/github/usage", nil, &out)
}
