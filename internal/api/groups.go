package api

import "time"

// Groups: one task run on several agents at once, perhaps each on another AI
// tool or model, compared side by side when they finish so one can be kept
// (internal/state/groups.go). The one kept goes on to review and its pull
// request as any agent does; the others are retired, their branches kept.

// FanOutRequest runs one task on several agents, one per variant, as a group.
type FanOutRequest struct {
	Project string `json:"project"`
	// Title names the group and each of its agents, which get a letter after
	// it: "Login page · A".
	Title string `json:"title"`
	Task  string `json:"task"`
	// Branch is the slug every agent's branch starts from, each getting its
	// letter appended ("feat-login-a"); empty makes one from Title.
	Branch string `json:"branch,omitempty"`
	From   string `json:"from,omitempty"`
	// Variants is one entry per agent, at least two and at most six.
	Variants []FanOutVariant `json:"variants"`
	// Judge asks the project's chat to compare the agents' work and
	// recommend one once they have all finished.
	Judge bool `json:"judge,omitempty"`
}

// FanOutVariant is what one agent of a group runs: CreateAgentRequest's
// fields of the same names, for that agent alone.
type FanOutVariant struct {
	AI               string            `json:"ai"`
	Model            *string           `json:"model,omitempty"`
	Effort           *string           `json:"effort,omitempty"`
	ContextWindow    *string           `json:"contextWindow,omitempty"`
	ClaudeAccount    string            `json:"claudeAccount,omitempty"`
	CodexAccount     string            `json:"codexAccount,omitempty"`
	OpenCodeAccounts map[string]string `json:"openCodeAccounts,omitempty"`
	CursorAccount    string            `json:"cursorAccount,omitempty"`
}

// FanOutResult is the group made, and the jobs making its agents.
type FanOutResult struct {
	Group Group `json:"group"`
	Jobs  []Job `json:"jobs"`
}

// Group is one task given to several agents.
type Group struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Title   string `json:"title"`
	Task    string `json:"task"`
	// ByLead says the project's chat made it (fan_out).
	ByLead bool `json:"byLead,omitempty"`
	// Kept is the agent the user kept, once they have.
	Kept string `json:"kept,omitempty"`
	// Recommended is the agent the project's chat recommends, and
	// Recommendation why; Judging says it was asked and hasn't answered.
	Recommended    string `json:"recommended,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
	Judging        bool   `json:"judging,omitempty"`
	// Judge says the chat will be asked to judge as soon as every agent
	// has finished.
	Judge     bool       `json:"judge,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	SettledAt *time.Time `json:"settledAt,omitempty"`
	// Finished is how many members have finished, or couldn't be made.
	Finished int           `json:"finished"`
	Members  []GroupMember `json:"members"`
}

// GroupMember is one of a group's agents. The fields after Error are only
// filled in GET /v1/groups/{id}, which compares them.
type GroupMember struct {
	// Label is its letter, A, B, C…, in the order the group asked for them.
	Label  string `json:"label"`
	AI     string `json:"ai"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Job makes the agent; Agent is its name and Ref its ref once it is
	// made. Error is why it couldn't be.
	Job        string     `json:"job,omitempty"`
	Agent      string     `json:"agent,omitempty"`
	Ref        string     `json:"ref,omitempty"`
	Error      string     `json:"error,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// StartedAt is when its agent was made, so StartedAt to FinishedAt is how
	// long it took.
	StartedAt *time.Time `json:"startedAt,omitempty"`

	// Title and Branch are its agent's. State is its machine's state, or
	// "creating" while its job runs, "failed" when that failed, and
	// "archived" once its machine was destroyed. Busy says its AI tool is
	// working.
	Title  string `json:"title,omitempty"`
	Branch string `json:"branch,omitempty"`
	State  string `json:"state,omitempty"`
	Busy   bool   `json:"busy,omitempty"`
	// Changes is its diff against the commit it started from, while its
	// worktree exists.
	Changes *AgentChanges `json:"changes,omitempty"`
	// PR is its pull request with its checks, if it opened one.
	PR *PullRequest `json:"pr,omitempty"`
	// Tokens and CostUSD are what its turns cost, from the token ledger.
	Tokens  int64   `json:"tokens,omitempty"`
	CostUSD float64 `json:"costUsd,omitempty"`
	// Media is how many items it kept: recordings, screenshots and the rest.
	Media int `json:"media,omitempty"`
	// Report is the status (done, partial, blocked or failed) and summary of
	// its latest report.
	ReportStatus  string `json:"reportStatus,omitempty"`
	ReportSummary string `json:"reportSummary,omitempty"`
}

// KeepRequest names the agent of a group to keep. Every other one is retired,
// its machine stopped and its branch kept.
type KeepRequest struct {
	Agent string `json:"agent"`
}

// RecommendRequest is the project's chat's pick of a group's agents.
type RecommendRequest struct {
	Agent string `json:"agent"`
	Why   string `json:"why"`
}
