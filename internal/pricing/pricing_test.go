package pricing

import (
	"math"
	"testing"
)

func TestLookupFindsTheModelHoweverItsToolNamesIt(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-4-5":                "claude-opus-4-5",
		"claude-haiku-4-5-20251001":      "claude-haiku-4-5",
		"claude-opus-4-5@20251101":       "claude-opus-4-5",
		"claude-sonnet-4-5[1m]":          "claude-sonnet-4-5",
		"anthropic/claude-sonnet-4-5":    "claude-sonnet-4-5",
		"gpt-5.3-codex/high":             "gpt-5.3-codex",
		"openai/gpt-5.1-codex-mini":      "gpt-5.1-codex-mini",
		"claude-4.5-sonnet-thinking":     "claude-sonnet-4-5",
		"claude-4-opus":                  "claude-opus-4",
		"Claude-Opus-5-5":                "claude-opus-5-5",
		"composer-2":                     "composer-2",
		"claude-opus-4-1-20250805":       "claude-opus-4-1",
		"claude-3-5-haiku-20241022":      "claude-3-5-haiku",
		"opencode/anthropic/gpt-5-codex": "gpt-5-codex",
	} {
		got, ok := Lookup(model)
		if !ok || got != table[want] {
			t.Errorf("Lookup(%q) = %+v, %v; want %s's %+v", model, got, ok, want, table[want])
		}
	}
}

func TestLookupLeavesUnknownModelsUnpriced(t *testing.T) {
	// A name close to one in the table isn't priced as it: a new version may
	// cost something else entirely.
	for _, model := range []string{"", "opus", "default", "claude-opus-4-9", "gpt-5.6", "gpt-5-6", "acme/secret-finetune-v2", "claude-sonnet"} {
		if p, ok := Lookup(model); ok {
			t.Errorf("Lookup(%q) = %+v, want no price", model, p)
		}
	}
}

func TestCostAddsEachKindAtItsPrice(t *testing.T) {
	p, _ := Lookup("claude-sonnet-4-5")
	// 1M input at $3, 100k output at $15, 10M cache reads at $0.30, 200k
	// cache writes at $3.75.
	got := p.Cost(1_000_000, 100_000, 10_000_000, 200_000)
	if want := 3 + 1.5 + 3 + 0.75; math.Abs(got-want) > 1e-9 {
		t.Errorf("Cost = %v, want %v", got, want)
	}
}

func TestEveryPriceIsWhole(t *testing.T) {
	for model, p := range table {
		if p.Input <= 0 || p.Output <= 0 || p.CacheRead <= 0 || p.CacheWrite < 0 {
			t.Errorf("%s: %+v has a price missing", model, p)
		}
		if canonical(model) != model {
			t.Errorf("%s is spelled %q once looked up: it can never be found", model, canonical(model))
		}
	}
}
