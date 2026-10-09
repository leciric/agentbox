package daemon

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Which models, effort levels and context windows are used, counted per AI
// tool as an agent is created and as a turn completes. A chat's model is
// whatever its tool reports, or whatever somebody typed, so it is never sent
// as it is: it is matched against usageModels, a fixed list, and anything not
// on it is "other". Effort and windows are matched the same way. Every key is
// a prefix from api, the tool, and one of these fixed values.

// usageOther is the value of anything not on a list.
const usageOther = "other"

// usageDefault is the value when nobody chose: the tool's own default.
const usageDefault = "default"

// usageModels are the models counted by name, keyed by their normalized id
// (usageModelKey). Add a model here as it comes out; until then it is "other".
var usageModels = map[string]string{
	// Claude Code's aliases, which it resolves itself.
	"default": "default", "best": "best", "opusplan": "opusplan",
	"opus": "opus", "sonnet": "sonnet", "haiku": "haiku", "fable": "fable",
	// Claude.
	"claude_fable_5_1":  "fable_5_1",
	"claude_opus_5_5":   "opus_5_5",
	"claude_sonnet_5_5": "sonnet_5_5",
	"claude_opus_5":     "opus_5",
	"claude_sonnet_5":   "sonnet_5",
	"claude_opus_4_6":   "opus_4_6",
	"claude_opus_4_5":   "opus_4_5",
	"claude_opus_4_1":   "opus_4_1",
	"claude_opus_4":     "opus_4",
	"claude_sonnet_4_6": "sonnet_4_6",
	"claude_sonnet_4_5": "sonnet_4_5",
	"claude_sonnet_4":   "sonnet_4",
	"claude_haiku_4_5":  "haiku_4_5",
	"claude_3_7_sonnet": "sonnet_3_7",
	"claude_3_5_haiku":  "haiku_3_5",
	// OpenAI, as Codex and OpenCode name them.
	"gpt_5_5":            "gpt_5_5",
	"gpt_5_4":            "gpt_5_4",
	"gpt_5_4_mini":       "gpt_5_4_mini",
	"gpt_5_3_codex":      "gpt_5_3_codex",
	"gpt_5_2":            "gpt_5_2",
	"gpt_5_2_codex":      "gpt_5_2_codex",
	"gpt_5_1":            "gpt_5_1",
	"gpt_5_1_codex":      "gpt_5_1_codex",
	"gpt_5_1_codex_mini": "gpt_5_1_codex_mini",
	"gpt_5_1_codex_max":  "gpt_5_1_codex_max",
	"gpt_5":              "gpt_5",
	"gpt_5_codex":        "gpt_5_codex",
	"gpt_5_mini":         "gpt_5_mini",
	"gpt_5_nano":         "gpt_5_nano",
	"gpt_4_1":            "gpt_4_1",
	"gpt_4o":             "gpt_4o",
	"o3":                 "o3",
	"o4_mini":            "o4_mini",
	"codex_mini":         "codex_mini",
	// Google, and what OpenCode offers for free.
	"gemini_3_pro":     "gemini_3_pro",
	"gemini_3_flash":   "gemini_3_flash",
	"gemini_2_5_pro":   "gemini_2_5_pro",
	"gemini_2_5_flash": "gemini_2_5_flash",
	"grok_code_fast_1": "grok_code_fast_1",
	"big_pickle":       "big_pickle",
	// Cursor's own, and its names for others' (its "Auto" is "default").
	"composer_2":       "composer_2",
	"composer_2_5":     "composer_2_5",
	"claude_opus_4_7":  "opus_4_7",
	"claude_opus_4_8":  "opus_4_8",
	"claude_haiku_5_5": "haiku_5_5",
	"gpt_5_6_sol":      "gpt_5_6_sol",
	"gpt_5_6_terra":    "gpt_5_6_terra",
	"gpt_5_6_luna":     "gpt_5_6_luna",
	"gemini_3_1_pro":   "gemini_3_1_pro",
}

// usageEfforts are the effort levels counted by name.
var usageEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// usageWindows are the context windows counted by name, in tokens.
var usageWindows = map[int64]string{
	128_000: "128k", 200_000: "200k", 256_000: "256k", 272_000: "272k",
	400_000: "400k", 500_000: "500k", 1_000_000: "1m", 2_000_000: "2m",
}

// usageDateSuffix is what a dated or preview id carries after the model's
// name: "claude-haiku-4-5-20251001", "gemini-3-pro-preview".
var usageDateSuffix = regexp.MustCompile(`(_\d{8}|_latest|_preview)+$`)

// usageModelKey normalizes one part of a model's id the way usageModels is
// keyed: lowercase, "-" and "." as "_", no date or "[1m]" on the end.
func usageModelKey(part string) string {
	part = strings.ToLower(strings.TrimSpace(part))
	part, _ = state.SplitClaudeModel(part)
	part = strings.NewReplacer("-", "_", ".", "_").Replace(part)
	return usageDateSuffix.ReplaceAllString(part, "")
}

// usageModel is the key a model counts under, and the effort its id carries
// when it has one: Codex names a preset "gpt-5.1-codex/high", OpenCode
// "provider/model". "" is the tool's default.
func usageModel(model string) (key, effort string) {
	if strings.TrimSpace(model) == "" {
		return usageDefault, ""
	}
	key = usageOther
	for part := range strings.SplitSeq(model, "/") {
		p := usageModelKey(part)
		if known, ok := usageModels[p]; ok && key == usageOther {
			key = known
		} else if usageEfforts[p] {
			effort = p
		}
	}
	return key, effort
}

// usageEffort is the key an effort level counts under.
func usageEffort(effort string) string {
	e := strings.ToLower(strings.TrimSpace(effort))
	switch {
	case e == "" || e == usageDefault:
		return usageDefault
	case usageEfforts[e]:
		return e
	}
	return usageOther
}

// usageWindow is the key a context window counts under: 0 is the model's
// whole window, whose size nobody has told AgentBox, and below 0 the tool's
// own default.
func usageWindow(tokens int64) string {
	switch {
	case tokens < 0:
		return usageDefault
	case tokens == 0:
		return "full"
	}
	if name, ok := usageWindows[tokens]; ok {
		return name
	}
	return usageOther
}

// usageTool is the AI tool's part of a key, the same as agentFeature's.
func usageTool(ai string) string {
	return agentFeature(ai, "claude", "codex", "opencode", "cursor")
}

// countModelUse counts the model, effort and window something ran with, under
// the prefixes given.
func (s *Server) countModelUse(ai, model, effort string, window int64, modelPrefix, effortPrefix, windowPrefix string) {
	tool := usageTool(ai)
	key, effortKey := usageModelEffort(model, effort)
	s.countFeature(modelPrefix + "." + tool + "." + key)
	s.countFeature(effortPrefix + "." + tool + "." + effortKey)
	s.countFeature(windowPrefix + "." + tool + "." + usageWindow(window))
}

// usageModelEffort is the keys a model and an effort count under, where an
// effort nobody set is the one the model's preset names, as Codex's do.
func usageModelEffort(model, effort string) (string, string) {
	key, preset := usageModel(model)
	if strings.TrimSpace(effort) == "" {
		effort = preset
	}
	return key, usageEffort(effort)
}

// countCreatedModel counts what a new agent starts on: the chat settings it
// was created with (claudeChatDefaults), where a tool without them starts on
// its own default.
func (s *Server) countCreatedModel(ctx context.Context, a state.Agent) {
	if !s.usageStatsOn(ctx) {
		return
	}
	stored, err := s.store.Chat(ctx, a.Project, a.Name)
	if err != nil {
		return
	}
	var window int64 = -1 // OpenCode's and Cursor's own
	if a.AI == "claude" || a.AI == "codex" {
		// Both compact at the installation's window, unless a Claude Code
		// agent was given one of its own.
		window, _ = s.store.ClaudeCompactWindow(ctx)
		if n, err := strconv.ParseInt(stored.Options[state.ChatOptionContextWindow], 10, 64); err == nil && n > 0 {
			window = n
		}
	}
	s.countModelUse(a.AI, stored.Options["model"], stored.Options["effort"], window,
		api.FeatureAgentCreateModel, api.FeatureAgentCreateEffort, api.FeatureAgentCreateContext)
}
