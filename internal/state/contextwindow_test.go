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
	if err := store.RememberClaudeModelWindow(ctx, "something-new", 1_000_000, 200_000); err != nil {
		t.Fatal(err)
	}
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.ContextWindows("something-new", 200_000); len(got) != 2 {
		t.Errorf("a model seen at 1M offers %v, want both windows", got)
	}
}

// A session started with no autoCompactWindow key at all doesn't run
// uncapped: Claude Code falls back to its own default there, ClaudeShortWindow,
// same as one that asked for it outright. Reporting exactly that size is still
// a cap, not opus's own answer, and remembering it as one is how an account's
// opus came to offer only 200k with no way back to 1M (D91).
func TestNoCompactWindowStillMeansTheToolsOwnDefault(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.RememberClaudeModelWindow(ctx, "opus", 200_000, 0); err != nil {
		t.Fatal(err)
	}
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Seen) != 0 {
		t.Errorf("remembered %v from an uncapped session reporting the tool's own default, want nothing", w.Seen)
	}
	if got := w.ContextWindows("opus", 200_000); len(got) != 2 {
		t.Errorf("opus offers %v after that report, want both windows still", got)
	}
	// A report above the tool's own default can only be the model's own,
	// uncapped or not.
	if err := store.RememberClaudeModelWindow(ctx, "sonnet", 1_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if w, err = store.ClaudeWindows(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Seen["sonnet"] != 1_000_000 {
		t.Errorf("Seen[sonnet] = %d, want 1000000", w.Seen["sonnet"])
	}
}

// A session compacting at 200k reports 200000 as its size whatever its
// model's window is. Remembering that made opus a 200k model for good, and
// Settings and create_agent offered it no 1M window.
func TestACompactCappedSizeIsNotTheModelsWindow(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	for _, model := range []string{"opus", "default"} {
		if err := store.RememberClaudeModelWindow(ctx, model, 200_000, 200_000); err != nil {
			t.Fatal(err)
		}
	}
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Seen) != 0 {
		t.Errorf("remembered %v from sizes capped at the compact window, want nothing", w.Seen)
	}
	for _, model := range []string{"opus", "default"} {
		if got, want := w.ContextWindows(model, 200_000), []int64{200_000, 1_000_000}; !slices.Equal(got, want) {
			t.Errorf("ContextWindows(%q) = %v, want %v", model, got, want)
		}
		if _, err := w.ContextWindowChoice(model, "1m", 200_000); err != nil {
			t.Errorf("1m for %s: %v", model, err)
		}
		if got, err := w.DefaultContextWindow(model, "1m", 200_000); err != nil || got != "1000000" {
			t.Errorf("DefaultContextWindow(%q, 1m) = %q, %v; want 1000000", model, got, err)
		}
	}

	// A size under the cap is the model's own, shorter than the cap; one
	// over it can only be the model's own.
	if err := store.RememberClaudeModelWindow(ctx, "short-one", 200_000, 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberClaudeModelWindow(ctx, "long-one", 1_000_000, 200_000); err != nil {
		t.Fatal(err)
	}
	if w, err = store.ClaudeWindows(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Seen["short-one"] != 200_000 || w.Seen["long-one"] != 1_000_000 {
		t.Errorf("Seen = %v, want short-one 200000 and long-one 1000000", w.Seen)
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

func TestDefaultContextWindowIsCheckedAgainstTheModel(t *testing.T) {
	var w state.ClaudeWindows
	for _, tc := range []struct{ model, value, want string }{
		{"opus", "", ""},
		{"opus", "200k", ""},
		{"opus", "1m", "1000000"},
		{"default", "1M", "1000000"},
		{"haiku", "200k", ""},
	} {
		if got, err := w.DefaultContextWindow(tc.model, tc.value, 200_000); err != nil || got != tc.want {
			t.Errorf("DefaultContextWindow(%q, %q) = %q, %v; want %q", tc.model, tc.value, got, err, tc.want)
		}
	}
	// The window the installation compacts at is the short choice, whatever
	// it has been set to.
	if got, err := w.DefaultContextWindow("opus", "300k", 300_000); err != nil || got != "" {
		t.Errorf("the installation's 300k = %q, %v; want it stored as the compact window", got, err)
	}
	for _, tc := range []struct{ model, value string }{
		{"haiku", "1m"},
		{"opus", "500k"},
		{"opus", "lots"},
	} {
		if got, err := w.DefaultContextWindow(tc.model, tc.value, 200_000); err == nil {
			t.Errorf("DefaultContextWindow(%q, %q) = %q, want it refused", tc.model, tc.value, got)
		}
	}
}
