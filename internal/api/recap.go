package api

import "time"

// Recap is GET /v1/recap: the user's week with AgentBox, the seven days up
// to and including today in their time zone (?offset=, minutes east of UTC),
// or ?weeks= weeks before that. Work is counted in seconds of agents
// generating, from the token ledger: two agents working the same hour count
// two hours. The page and its share cards are drawn from it.
type Recap struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Agents is how many workers (not leads) worked in the week or were
	// created in it.
	Agents int `json:"agents"`
	// PRsOpened is how many of those agents have a pull request, and
	// PRsMerged how many of those are merged.
	PRsOpened int `json:"prsOpened"`
	PRsMerged int `json:"prsMerged"`
	// Insertions, Deletions and Files are what those agents' branches
	// changed, as the Work page measures each.
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
	Files      int `json:"files"`
	// WorkSeconds is every turn's generating time, and AwaySeconds the part
	// of it while the user was away: no message to a lead and no verdict
	// within RecapPresence of it.
	WorkSeconds int64 `json:"workSeconds"`
	AwaySeconds int64 `json:"awaySeconds"`
	// Days are the week's seven days, oldest first.
	Days []RecapDay `json:"days"`
	// Hours is the week's work by the hour of the day it was done in, 24
	// entries from midnight.
	Hours []int64 `json:"hours"`
	// Projects is how many projects had work, and Busiest the one with the
	// most; nil in a week with none.
	Projects int           `json:"projects"`
	Busiest  *RecapProject `json:"busiest,omitempty"`
	// Longest is the agent that worked longest in the week.
	Longest *RecapAgent `json:"longest,omitempty"`
	// Models is the week's tokens by model, most first, every chat's (leads'
	// too), and Tokens their sum.
	Models []RecapModel `json:"models"`
	Tokens int64        `json:"tokens"`
	// CostUSD is what the week cost at API prices: what the AI tools
	// reported, and for turns whose tool reported none (Codex, Cursor), an
	// estimate from internal/pricing, of which EstimatedUSD is the part.
	// UnpricedTokens is the tokens of those turns spent on models with no
	// price, which CostUSD leaves out.
	CostUSD        float64 `json:"costUsd"`
	EstimatedUSD   float64 `json:"estimatedUsd"`
	UnpricedTokens int64   `json:"unpricedTokens"`
	// Streak is how many days in a row, up to today (or yesterday, when
	// today has no work yet), agents worked; BestStreak the longest run in
	// the last year.
	Streak     int `json:"streak"`
	BestStreak int `json:"bestStreak"`
}

// RecapPresence is how long after the user sent a message or gave a verdict
// they count as there: agents' work outside every such stretch is "while you
// were away".
const RecapPresence = 30 * time.Minute

// RecapDay is one day of a Recap: Date is its local date, 2006-01-02.
type RecapDay struct {
	Date        string `json:"date"`
	WorkSeconds int64  `json:"workSeconds"`
	AwaySeconds int64  `json:"awaySeconds"`
	Agents      int    `json:"agents"`
}

// RecapProject is a project's share of a Recap.
type RecapProject struct {
	Project     string `json:"project"`
	WorkSeconds int64  `json:"workSeconds"`
	Agents      int    `json:"agents"`
}

// RecapAgent is one agent's part in a Recap.
type RecapAgent struct {
	Project     string `json:"project"`
	Agent       string `json:"agent"`
	Title       string `json:"title,omitempty"`
	AI          string `json:"ai,omitempty"`
	WorkSeconds int64  `json:"workSeconds"`
}

// RecapModel is one model's tokens in a Recap, and their cost as Recap's;
// Model is "" when the AI tool didn't name it. A turn's reported cost is
// shared among its models by what each would cost at list price.
type RecapModel struct {
	Model          string  `json:"model"`
	Tokens         int64   `json:"tokens"`
	Output         int64   `json:"output"`
	CostUSD        float64 `json:"costUsd"`
	EstimatedUSD   float64 `json:"estimatedUsd"`
	UnpricedTokens int64   `json:"unpricedTokens"`
}
