package acp

import "encoding/json"

// What a turn spent, as the adapters report it (D83).
//
// Three shapes arrive, and none is complete on its own:
//
//   - PromptResponse.usage, ACP's own (unstable) field: the turn's tokens.
//     claude-agent-acp fills it from the SDK's per-turn main-loop usage, so a
//     subagent the turn started is not in it.
//   - PromptResponse._meta.quota, which claude-agent-acp and codex-acp both
//     send in codex-acp's shape: the same turn split by model. claude-agent-acp
//     builds it from the SDK's modelUsage, which does count subagents.
//   - usage_update's cost, sent after every model result — the ones a turn
//     makes and the ones a session makes by itself between turns (a background
//     task finishing wakes it). It is a running total for the adapter process,
//     not a per-result figure: "read the latest result rather than summing".
//
// So a turn's tokens are read from the quota when there is one, from usage
// when there isn't, and its cost is how far the running total moved.

// Usage is ACP's usage field on a prompt response.
type Usage struct {
	TotalTokens       int64 `json:"totalTokens"`
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	ThoughtTokens     int64 `json:"thoughtTokens,omitempty"`
	CachedReadTokens  int64 `json:"cachedReadTokens,omitempty"`
	CachedWriteTokens int64 `json:"cachedWriteTokens,omitempty"`
}

// PromptMeta is the part of a prompt response's _meta AgentBox reads.
type PromptMeta struct {
	Quota *Quota `json:"quota,omitempty"`
}

// Quota is a turn's spend, whole and split by the models that did the work.
type Quota struct {
	TokenCount *TokenCount  `json:"token_count,omitempty"`
	ModelUsage []ModelUsage `json:"model_usage,omitempty"`
}

type ModelUsage struct {
	Model      string     `json:"model"`
	TokenCount TokenCount `json:"token_count"`
}

// TokenCount is one counter set in codex-acp's shape. CachedInputTokens is
// cache reads; CachedWriteTokens is Claude's cache writes, which codex has no
// slot for. Reasoning is billed inside the output tokens by both vendors, so
// it is never added on top.
type TokenCount struct {
	TotalTokens           int64 `json:"totalTokens"`
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	CachedWriteTokens     int64 `json:"cachedWriteTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	// gemini is a counter set in Gemini CLI's shape (geminiByModel).
	gemini bool
}

// UnmarshalJSON reads codex-acp's camelCase counters, and the two snake_case
// ones Gemini CLI sends in the same place: {"input_tokens", "output_tokens"}.
func (t *TokenCount) UnmarshalJSON(data []byte) error {
	type plain TokenCount
	var camel plain
	if err := json.Unmarshal(data, &camel); err != nil {
		return err
	}
	*t = TokenCount(camel)
	var snake struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
	}
	if json.Unmarshal(data, &snake) == nil && (snake.Input != nil || snake.Output != nil) {
		t.gemini = true
		if snake.Input != nil {
			t.InputTokens = *snake.Input
		}
		if snake.Output != nil {
			t.OutputTokens = *snake.Output
		}
	}
	return nil
}

// Cost is usage_update's running total of what the session has cost.
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Spent is a turn's tokens in four counters that don't overlap: input that
// wasn't read from the cache, output, cache reads and cache writes.
type Spent struct {
	Input, Output, CacheRead, CacheWrite int64
}

// Total is every token the counters hold.
func (s Spent) Total() int64 { return s.Input + s.Output + s.CacheRead + s.CacheWrite }

func (s Spent) plus(o Spent) Spent {
	return Spent{s.Input + o.Input, s.Output + o.Output, s.CacheRead + o.CacheRead, s.CacheWrite + o.CacheWrite}
}

// Spent reads the counter set without double counting. The vendors disagree on
// what input means: Anthropic's excludes the tokens read from the cache and
// OpenAI's includes them. The total says which one this is — when it is
// smaller than the four counters added up, the cache reads were already
// inside the input, and are taken back out.
func (t TokenCount) Spent() Spent {
	s := Spent{Input: t.InputTokens, Output: t.OutputTokens, CacheRead: t.CachedInputTokens, CacheWrite: t.CachedWriteTokens}
	return withoutCachedInput(s, t.TotalTokens)
}

// Spent reads ACP's usage field the same way as TokenCount.Spent.
func (u Usage) Spent() Spent {
	s := Spent{Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CachedReadTokens, CacheWrite: u.CachedWriteTokens}
	return withoutCachedInput(s, u.TotalTokens)
}

func withoutCachedInput(s Spent, total int64) Spent {
	if s.CacheRead > 0 && s.Input >= s.CacheRead && total > 0 && total < s.Total() {
		s.Input -= s.CacheRead
	}
	return s
}

// ByModel is what a turn spent, per model, from the richest shape the
// response carries: the quota's per-model split, which counts subagents,
// else the usage field under the name fallback (the model the session was
// on), else nothing at all. A model the split names twice is added up.
func (r PromptResponse) ByModel(fallback string) map[string]Spent {
	out := map[string]Spent{}
	if r.Meta != nil && r.Meta.Quota != nil && len(r.Meta.Quota.ModelUsage) > 0 && r.Meta.Quota.ModelUsage[0].TokenCount.gemini {
		return r.geminiByModel()
	}
	if r.Meta != nil && r.Meta.Quota != nil && len(r.Meta.Quota.ModelUsage) > 0 {
		for _, m := range r.Meta.Quota.ModelUsage {
			if s := m.TokenCount.Spent(); s.Total() > 0 {
				out[m.Model] = out[m.Model].plus(s)
			}
		}
		return out
	}
	if r.Meta != nil && r.Meta.Quota != nil && r.Meta.Quota.TokenCount != nil {
		if s := r.Meta.Quota.TokenCount.Spent(); s.Total() > 0 {
			out[fallback] = s
		}
		return out
	}
	if r.Usage != nil {
		if s := r.Usage.Spent(); s.Total() > 0 {
			out[fallback] = s
		}
	}
	return out
}

// geminiByModel reads Gemini CLI's split, which counts less than the others':
// each model's input includes what was read from the cache, its output leaves
// out the thinking (which Google bills as output), and the cache reads and
// thinking are only in the turn's usage field, for every model at once. They
// go to the model that read the most input, the one the turn was on; a turn
// Gemini CLI routed through a small model too ("auto") gives that one its own
// input and output alone.
func (r PromptResponse) geminiByModel() map[string]Spent {
	out := map[string]Spent{}
	main := ""
	for _, m := range r.Meta.Quota.ModelUsage {
		s := out[m.Model]
		s.Input += m.TokenCount.InputTokens
		s.Output += m.TokenCount.OutputTokens
		out[m.Model] = s
		if main == "" || s.Input > out[main].Input {
			main = m.Model
		}
	}
	if r.Usage != nil {
		s := out[main]
		cached := min(r.Usage.CachedReadTokens, s.Input)
		s.Input -= cached
		s.CacheRead += cached
		s.Output += r.Usage.ThoughtTokens
		out[main] = s
	}
	for model, s := range out {
		if s.Total() == 0 {
			delete(out, model)
		}
	}
	return out
}

// RateLimit is claude-agent-acp's _meta["_claude/rateLimit"]: what Anthropic
// said about the account's usage limits in the response that carried them
// (D85). Utilization is 0 to 1 and times are Unix seconds. Captured live:
//
//	{"status": "allowed", "resetsAt": 1790092800, "rateLimitType": "five_hour",
//	 "overageStatus": "rejected", "isUsingOverage": false,
//	 "unifiedWindows": {"five_hour": {"utilization": 0.28, "resetsAt": 1790092800},
//	                    "seven_day": {"utilization": 0.42, "resetsAt": 1790308800}}}
type RateLimit struct {
	Status         string                 `json:"status"`
	ResetsAt       int64                  `json:"resetsAt,omitempty"`
	RateLimitType  string                 `json:"rateLimitType,omitempty"`
	OverageStatus  string                 `json:"overageStatus,omitempty"`
	IsUsingOverage bool                   `json:"isUsingOverage,omitempty"`
	UnifiedWindows map[string]LimitWindow `json:"unifiedWindows,omitempty"`
}

// LimitWindow is one of the account's limits: how much of it is used, and
// when it starts again.
type LimitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}
