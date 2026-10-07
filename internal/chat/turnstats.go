package chat

import (
	"slices"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// TurnStats is what the anonymous usage stats learn about one turn: the
// chat's settings as its tool names them, never anything said in it. The
// daemon maps every name onto a fixed list before it is kept
// (internal/daemon/usageevents.go).
type TurnStats struct {
	Model  string // the model option's value, "" for none
	Effort string // the thought_level option's, "" for none
	Mode   string // the mode option's, "" for none
	// Window is the context window in tokens: where a Claude Code or Codex
	// chat compacts, else the model's own as the tool last reported it; 0
	// when neither is known.
	Window int64
	// State is how it ended: completed, cancelled or failed, as
	// api.ChatTurnResult.State.
	State    string
	Duration time.Duration
	// The tokens it spent, across every model it ran, its subagents too.
	Input, Cached, CacheCreation, Output, Reasoning int64
	Subagents                                       bool // whether it started any subagent
}

// turnStats is what TurnFinished is told about t, which ended in state with
// res (nil when it failed). Caller holds c.mu.
func (c *conversation) turnStats(t *turn, res *acp.PromptResponse, state string) TurnStats {
	st := TurnStats{
		Model:     optionValueOf(c.session.Options, "model"),
		Effort:    optionOfCategory(c.session.Options, "thought_level"),
		Mode:      optionOfCategory(c.session.Options, "mode"),
		State:     state,
		Subagents: t.subagents,
	}
	if !t.startedAt.IsZero() {
		st.Duration = time.Since(t.startedAt)
	}
	if c.agent.AI == "claude" || c.agent.AI == "codex" {
		st.Window = c.compactWindow()
	}
	if st.Window == 0 {
		st.Window = c.session.ContextSize
	}
	if res != nil {
		for _, spent := range res.ByModel(st.Model) {
			st.Input += spent.Input
			st.Cached += spent.CacheRead
			st.CacheCreation += spent.CacheWrite
			st.Output += spent.Output
		}
		st.Reasoning = reasoningTokens(*res)
	}
	return st
}

// reasoningTokens is how many of a turn's output tokens were reasoning, from
// the same shapes ByModel reads, where the tool says.
func reasoningTokens(r acp.PromptResponse) int64 {
	if r.Meta != nil && r.Meta.Quota != nil {
		if len(r.Meta.Quota.ModelUsage) > 0 {
			var n int64
			for _, m := range r.Meta.Quota.ModelUsage {
				n += m.TokenCount.ReasoningOutputTokens
			}
			return n
		}
		if r.Meta.Quota.TokenCount != nil {
			return r.Meta.Quota.TokenCount.ReasoningOutputTokens
		}
	}
	if r.Usage != nil {
		return r.Usage.ThoughtTokens
	}
	return 0
}

// optionOfCategory is the value of the option of category, "" for none.
func optionOfCategory(options []api.ChatOption, category string) string {
	i := slices.IndexFunc(options, func(o api.ChatOption) bool { return o.Category == category })
	if i < 0 {
		return ""
	}
	return options[i].Value
}
