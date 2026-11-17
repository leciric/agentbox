package acp

import (
	"regexp"
	"strconv"
	"strings"
)

// ClaudeSessionMeta is session/new's, session/resume's and session/load's
// _meta for Claude Code. The adapter spreads _meta.claudeCode.options into the
// Agent SDK's options, over its own defaults.
//
// It turns prompt suggestions off: the adapter defaults promptSuggestions to
// true, which costs one extra request every turn for a suggestion nothing in
// AgentBox shows.
//
// And it asks for Claude's thinking as summarized text, for a model that takes
// it (ClaudeAdaptiveThinking). Left to itself, the SDK asks current models for
// display "omitted", whose thinking blocks stream with no text, which the
// adapter drops, so the client sees no agent_thought_chunk at all. Adaptive is
// current models' default anyway, and the only thinking type they take:
// "enabled" with a budget, and "disabled", are refused.
func ClaudeSessionMeta(model string) map[string]any {
	options := map[string]any{"promptSuggestions": false}
	if ClaudeAdaptiveThinking(model) {
		options["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
	}
	return map[string]any{"claudeCode": map[string]any{"options": options}}
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
