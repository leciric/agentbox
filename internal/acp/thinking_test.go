package acp

import "testing"

func TestClaudeAdaptiveThinking(t *testing.T) {
	for model, want := range map[string]bool{
		"":                                  true,
		"default":                           true,
		"opus":                              true,
		"opus[1m]":                          true,
		"haiku":                             true,
		"fable":                             true,
		"claude-fable-5-1":                  true,
		"claude-mythos-5":                   true,
		"claude-opus-5-5":                   true,
		"claude-sonnet-5":                   true,
		"claude-haiku-5-5":                  true,
		"claude-opus-4-6":                   true,
		"claude-sonnet-4-6[1m]":             true,
		"claude-opus-4-8":                   true,
		"claude-haiku-4-5":                  false,
		"claude-haiku-4-5-20251001":         false,
		"claude-sonnet-4-5":                 false,
		"claude-sonnet-4-5-20250929[1m]":    false,
		"us.anthropic.claude-opus-4-5-v1:0": false,
		"claude-opus-4-1":                   false,
		"claude-opus-4-20250514":            false,
		"claude-sonnet-4":                   false,
		"claude-3-7-sonnet-20250219":        false,
		"claude-3-5-haiku-latest":           false,
	} {
		if got := ClaudeAdaptiveThinking(model); got != want {
			t.Errorf("ClaudeAdaptiveThinking(%q) = %v, want %v", model, got, want)
		}
		if got := ClaudeThinkingMeta(model) != nil; got != want {
			t.Errorf("ClaudeThinkingMeta(%q) sent: %v, want %v", model, got, want)
		}
	}
}
