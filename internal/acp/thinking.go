package acp

import (
	"regexp"
	"strconv"
	"strings"
)

// ClaudeThinkingMeta is session/new's, session/resume's and session/load's
// _meta asking claude-agent-acp for Claude's thinking as summarized text, or
// nil for a model that can't take it (ClaudeAdaptiveThinking). The adapter
// spreads _meta.claudeCode.options into the Agent SDK's options; left to
// itself, the SDK asks current models for display "omitted", whose thinking
// blocks stream with no text, which the adapter drops, so the client sees no
// agent_thought_chunk at all. Adaptive is current models' default anyway, and
// the only thinking type they take: "enabled" with a budget, and "disabled",
// are refused.
func ClaudeThinkingMeta(model string) map[string]any {
	if !ClaudeAdaptiveThinking(model) {
		return nil
	}
	return map[string]any{"claudeCode": map[string]any{"options": map[string]any{
		"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
	}}}
}

// claudeVersioned finds a model id's family and version: "claude-haiku-4-5-20251001",
// "us.anthropic.claude-sonnet-4-5-v1:0", "claude-opus-4-1".
var claudeVersioned = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:-(\d{1,2}))?(?:\D|$)`)

// ClaudeAdaptiveThinking reports whether model takes thinking {type: "adaptive"}:
// Claude 4.6 and later, Fable and Mythos. Claude Haiku 4.5, Sonnet and Opus 4.5
// and older take only a budget_tokens, and answer adaptive with a 400. An alias
// ("opus", "haiku", "default", "opus[1m]") or no model at all is Claude Code's
// current model of that name, so it does. Ids in the old order
// ("claude-3-7-sonnet-20250219") are all older than 4.6.
func ClaudeAdaptiveThinking(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "claude-3") {
		return false
	}
	match := claudeVersioned.FindStringSubmatch(m)
	if match == nil {
		return true
	}
	if match[1] == "fable" || match[1] == "mythos" {
		return true
	}
	major, _ := strconv.Atoi(match[2])
	minor, _ := strconv.Atoi(match[3])
	return major > 4 || major == 4 && minor >= 6
}
