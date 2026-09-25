package acp

import (
	"encoding/json"
	"testing"
)

// TestSpentNeverCountsTheCacheTwice: Anthropic's input excludes cache reads and
// OpenAI's includes them, and the total says which one a counter set is.
func TestSpentNeverCountsTheCacheTwice(t *testing.T) {
	// claude-agent-acp: the total is all four counters added up.
	claude := TokenCount{InputTokens: 10, CachedInputTokens: 90_000, CachedWriteTokens: 2_000, OutputTokens: 500, TotalTokens: 92_510}
	if got := claude.Spent(); got != (Spent{Input: 10, Output: 500, CacheRead: 90_000, CacheWrite: 2_000}) {
		t.Errorf("claude = %+v", got)
	}
	// codex-acp: input already holds the cached part, so the total is input
	// plus output.
	codex := TokenCount{InputTokens: 50_000, CachedInputTokens: 40_000, OutputTokens: 700, TotalTokens: 50_700}
	if got := codex.Spent(); got != (Spent{Input: 10_000, Output: 700, CacheRead: 40_000}) {
		t.Errorf("codex = %+v", got)
	}
	if got := codex.Spent().Total(); got != 50_700 {
		t.Errorf("codex total = %d, want the tool's own 50700", got)
	}
	// With no total to go by, the counters are taken as they are.
	bare := Usage{InputTokens: 10, OutputTokens: 5, CachedReadTokens: 100}
	if got := bare.Spent(); got != (Spent{Input: 10, Output: 5, CacheRead: 100}) {
		t.Errorf("bare = %+v", got)
	}
}

// TestByModelPrefersTheQuota decodes a prompt response exactly as
// claude-agent-acp 0.76.0 sent it for a one-line turn on Haiku (captured live),
// and reads the per-model split: it counts a side call — the session's title —
// that the main loop's usage field leaves out.
func TestByModelPrefersTheQuota(t *testing.T) {
	var res PromptResponse
	raw := `{"stopReason": "end_turn", "usage": {"inputTokens": 10, "outputTokens": 46, "cachedReadTokens": 13615, "cachedWriteTokens": 6960, "totalTokens": 20631}, "_meta": {"quota": {"token_count": {"totalTokens": 20631, "inputTokens": 10, "cachedInputTokens": 13615, "cachedWriteTokens": 6960, "outputTokens": 46, "reasoningOutputTokens": 0}, "model_usage": [{"model": "claude-haiku-4-5-20251001", "token_count": {"totalTokens": 21537, "inputTokens": 907, "cachedInputTokens": 13615, "cachedWriteTokens": 6960, "outputTokens": 55, "reasoningOutputTokens": 0}}]}}}`
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	haiku := res.ByModel("haiku")["claude-haiku-4-5-20251001"]
	if haiku != (Spent{Input: 907, Output: 55, CacheRead: 13_615, CacheWrite: 6_960}) || haiku.Total() != 21_537 {
		t.Errorf("ByModel = %+v, want the quota's split, side call included", res.ByModel("haiku"))
	}

	// The update that follows the result carries the running cost.
	var n SessionNotification
	if err := json.Unmarshal([]byte(`{"sessionId":"s","update":{"sessionUpdate": "usage_update", "used": 20631, "size": 200000, "cost": {"amount": 0.0164635, "currency": "USD"}, "_meta": {"_claude/origin": {"kind": "human"}}}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Update.Cost == nil || n.Update.Cost.Amount != 0.0164635 || n.Update.Used != 20631 {
		t.Errorf("usage_update = %+v", n.Update)
	}

	// Several models, one of which spent nothing, as a turn with a subagent
	// can report them.
	raw = `{"stopReason":"end_turn","_meta":{"quota":{"model_usage":[
			{"model":"claude-sonnet-5","token_count":{"totalTokens":92510,"inputTokens":10,"cachedInputTokens":90000,"cachedWriteTokens":2000,"outputTokens":500}},
			{"model":"claude-haiku-4-5","token_count":{"totalTokens":21105,"inputTokens":5,"cachedInputTokens":20000,"cachedWriteTokens":1000,"outputTokens":100}},
			{"model":"claude-idle","token_count":{"totalTokens":0}}]}}}`
	res = PromptResponse{}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	got := res.ByModel("sonnet")
	if len(got) != 2 || got["claude-haiku-4-5"].CacheRead != 20_000 || got["claude-sonnet-5"].Output != 500 {
		t.Errorf("ByModel = %+v, want sonnet and haiku from the quota, and nothing for a model that spent nothing", got)
	}

	// Without a quota, the usage field under the session's model.
	plain := PromptResponse{Usage: &Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7}}
	if got := plain.ByModel("gpt-5"); got["gpt-5"] != (Spent{Input: 3, Output: 4}) {
		t.Errorf("ByModel(no quota) = %+v", got)
	}
	// And nothing when the response says nothing.
	if got := (PromptResponse{}).ByModel("x"); len(got) != 0 {
		t.Errorf("ByModel(nothing) = %+v", got)
	}
}

// TestRateLimitDecodes reads the usage_update claude-agent-acp 0.76.0 sent
// with the account's limits on it, captured live.
func TestRateLimitDecodes(t *testing.T) {
	var n SessionNotification
	raw := `{"sessionId":"s","update":{"sessionUpdate": "usage_update", "used": 20589, "size": 200000, "_meta": {"_claude/rateLimit": {"status": "allowed", "resetsAt": 1790092800, "rateLimitType": "five_hour", "overageStatus": "rejected", "overageDisabledReason": "org_level_disabled", "isUsingOverage": false, "unifiedWindows": {"five_hour": {"utilization": 0.28, "resetsAt": 1790092800}, "seven_day": {"utilization": 0.42, "resetsAt": 1790308800}}}}}}`
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	rl := n.Update.Meta.RateLimit
	if rl == nil || rl.Status != "allowed" || rl.UnifiedWindows["five_hour"].Utilization != 0.28 || rl.UnifiedWindows["seven_day"].ResetsAt != 1790308800 {
		t.Errorf("rate limit = %+v", rl)
	}
	if n.Update.Cost != nil {
		t.Error("an update with no cost decoded one")
	}
}
