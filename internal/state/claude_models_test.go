package state_test

import (
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

func TestMergePinnedClaudeModels(t *testing.T) {
	// An empty menu still gets the pinned models: there's nothing to
	// de-duplicate against yet.
	merged := state.MergePinnedClaudeModels(nil)
	if !hasValue(merged, "claude-fable-5-1") || len(merged) != 1 {
		t.Fatalf("MergePinnedClaudeModels(nil) = %+v, want Fable alone", merged)
	}

	// A menu that already lists a pinned value keeps the adapter's own entry
	// for it, not AgentBox's placeholder.
	adapterFable := api.ChatOptionChoice{Value: "claude-fable-5-1", Name: "Fable (really offered)", Description: "from the adapter"}
	merged = state.MergePinnedClaudeModels([]api.ChatOptionChoice{adapterFable})
	got, ok := find(merged, "claude-fable-5-1")
	if !ok || got != adapterFable {
		t.Fatalf("a menu that already offers a pinned value got overwritten: %+v", got)
	}

	// A menu with unrelated models keeps them, and gains the pinned ones.
	other := api.ChatOptionChoice{Value: "sonnet", Name: "Sonnet"}
	merged = state.MergePinnedClaudeModels([]api.ChatOptionChoice{other})
	if !hasValue(merged, "sonnet") || !hasValue(merged, "claude-fable-5-1") || len(merged) != 2 {
		t.Fatalf("MergePinnedClaudeModels didn't keep the real menu and add Fable: %+v", merged)
	}

	// A "[1m]" variant beside its own model is folded into it (D91); one with
	// no plain entry beside it stays, since it is the only way to that model.
	merged = state.MergePinnedClaudeModels([]api.ChatOptionChoice{{Value: "opus"}, {Value: "opus[1m]"}, {Value: "claude-sonnet-4-5[1m]"}})
	if hasValue(merged, "opus[1m]") || !hasValue(merged, "opus") || !hasValue(merged, "claude-sonnet-4-5[1m]") {
		t.Fatalf("the 1M variants weren't folded as they should be: %+v", merged)
	}
}

func TestIsPinnedClaudeChoice(t *testing.T) {
	merged := state.MergePinnedClaudeModels(nil)
	fable, ok := find(merged, "claude-fable-5-1")
	if !ok {
		t.Fatal("Fable missing from a merged empty menu")
	}
	if !state.IsPinnedClaudeChoice(fable) {
		t.Errorf("IsPinnedClaudeChoice(%+v) = false, want true", fable)
	}

	// A real adapter entry sharing the same value, but with the adapter's own
	// name and description, is not mistaken for AgentBox's own.
	adapterFable := api.ChatOptionChoice{Value: "claude-fable-5-1", Name: "Fable (really offered)", Description: "from the adapter"}
	if state.IsPinnedClaudeChoice(adapterFable) {
		t.Errorf("IsPinnedClaudeChoice(%+v) = true, want false", adapterFable)
	}

	// A model AgentBox never pinned isn't reported as pinned either.
	if state.IsPinnedClaudeChoice(api.ChatOptionChoice{Value: "sonnet", Name: "Sonnet"}) {
		t.Error("IsPinnedClaudeChoice reported an ordinary model as pinned")
	}
}

func TestMergePinnedClaudeModelValues(t *testing.T) {
	got := state.MergePinnedClaudeModelValues([]string{"sonnet", state.DefaultClaudeModel})
	want := map[string]bool{"sonnet": true, state.DefaultClaudeModel: true, "claude-fable-5-1": true}
	if len(got) != len(want) {
		t.Fatalf("MergePinnedClaudeModelValues() = %v, want %v", got, want)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("unexpected value %q", v)
		}
		delete(want, v)
	}
	if len(want) != 0 {
		t.Errorf("missing values: %v", want)
	}
}

func hasValue(choices []api.ChatOptionChoice, value string) bool {
	_, ok := find(choices, value)
	return ok
}

func find(choices []api.ChatOptionChoice, value string) (api.ChatOptionChoice, bool) {
	for _, c := range choices {
		if c.Value == value {
			return c, true
		}
	}
	return api.ChatOptionChoice{}, false
}
