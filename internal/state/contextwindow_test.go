package state_test

import (
	"context"
	"slices"
	"testing"

	"agentbox/internal/state"
)

func TestContextWindowsFollowTheModel(t *testing.T) {
	var w state.ClaudeWindows
	for _, tc := range []struct {
		model        string
		installation int64
		want         []int64
	}{
		{"opus", 200_000, []int64{200_000, 1_000_000}},
		{"sonnet", 150_000, []int64{150_000, 1_000_000}},
		{"default", 200_000, []int64{200_000, 1_000_000}},
		{"claude-fable-5-1", 200_000, []int64{200_000, 1_000_000}},
		{"haiku", 200_000, []int64{200_000}},         // one window: no choice
		{"opus", 0, []int64{1_000_000}},              // the installation already compacts at the whole window
		{"something-new", 200_000, []int64{200_000}}, // unknown: the short window until a turn says otherwise
	} {
		if got := w.ContextWindows(tc.model, tc.installation); !slices.Equal(got, tc.want) {
			t.Errorf("ContextWindows(%q, %d) = %v, want %v", tc.model, tc.installation, got, tc.want)
		}
	}
}

func TestWhatASessionReportedOverridesTheGuess(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.RememberClaudeModelWindow(ctx, "something-new", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberClaudeModelWindow(ctx, "opus", 200_000); err != nil {
		t.Fatal(err)
	}
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.ContextWindows("something-new", 200_000); len(got) != 2 {
		t.Errorf("a model seen at 1M offers %v, want both windows", got)
	}
	if got := w.ContextWindows("opus", 200_000); len(got) != 1 {
		t.Errorf("an account whose opus reported 200k offers %v, want one window", got)
	}
	// And an "opus[1m]" stored on that account keeps its suffix, since it is
	// the only way there to a long window.
	if got := w.NormalizeClaudeModel("opus[1m]"); got != "opus[1m]" {
		t.Errorf("NormalizeClaudeModel(opus[1m]) = %q where opus is short, want it kept", got)
	}
}

func TestA1MVariantOnTheMenuIsTheWayToALongWindow(t *testing.T) {
	w := state.ClaudeWindows{Seen: map[string]int64{"claude-sonnet-4-5": 200_000}, OneM: map[string]bool{"claude-sonnet-4-5": true}}
	if got := w.ContextWindows("claude-sonnet-4-5", 200_000); !slices.Equal(got, []int64{200_000, 1_000_000}) {
		t.Fatalf("ContextWindows = %v, want 200k and 1M", got)
	}
	if got := w.Model("claude-sonnet-4-5", 1_000_000); got != "claude-sonnet-4-5[1m]" {
		t.Errorf("started on %q for 1M, want the [1m] variant", got)
	}
	if got := w.Model("claude-sonnet-4-5", 200_000); got != "claude-sonnet-4-5" {
		t.Errorf("started on %q for 200k, want the plain model", got)
	}
	if got := w.Model("opus", 1_000_000); got != "opus" {
		t.Errorf("opus started as %q, want its own name: its window is already 1M", got)
	}
}

func TestOldOneMModelsKeepCompactingWhereTheyDid(t *testing.T) {
	var w state.ClaudeWindows
	model := w.NormalizeClaudeModel("opus[1m]")
	if model != "opus" {
		t.Fatalf("NormalizeClaudeModel(opus[1m]) = %q, want opus", model)
	}
	// Nothing chosen: the installation's window, as before D91.
	if got := w.CompactWindow(model, "", 200_000); got != 200_000 {
		t.Errorf("an old opus[1m] chat compacts at %d, want 200000", got)
	}
}

func TestCompactWindowIsTheChosenOne(t *testing.T) {
	var w state.ClaudeWindows
	for _, tc := range []struct {
		model, chosen string
		installation  int64
		want          int64
	}{
		{"opus", "", 200_000, 200_000},
		{"opus", "1000000", 200_000, 1_000_000},
		{"opus", "200000", 200_000, 200_000},
		{"haiku", "1000000", 200_000, 200_000}, // a window the model doesn't have falls back to its whole
		{"opus", "500000", 200_000, 200_000},   // not a choice any more: the installation's
		{"opus", "", 0, 0},                     // the installation's "whole window" leaves the key out
		{"opus", "200000", 150_000, 150_000},   // the installation moved: the short choice follows it
	} {
		if got := w.CompactWindow(tc.model, tc.chosen, tc.installation); got != tc.want {
			t.Errorf("CompactWindow(%q, %q, %d) = %d, want %d", tc.model, tc.chosen, tc.installation, got, tc.want)
		}
	}
}

func TestContextWindowChoiceIsValidatedAgainstTheModel(t *testing.T) {
	var w state.ClaudeWindows
	if got, err := w.ContextWindowChoice("opus", "1m", 200_000); err != nil || got != "1000000" {
		t.Errorf("ContextWindowChoice(opus, 1m) = %q, %v", got, err)
	}
	if got, err := w.ContextWindowChoice("opus", "200K", 200_000); err != nil || got != "200000" {
		t.Errorf("ContextWindowChoice(opus, 200K) = %q, %v", got, err)
	}
	if _, err := w.ContextWindowChoice("haiku", "1m", 200_000); err == nil {
		t.Error("Haiku was given a 1M window")
	}
	if _, err := w.ContextWindowChoice("opus", "lots", 200_000); err == nil {
		t.Error("a window that isn't one was accepted")
	}
}
