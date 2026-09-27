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
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.ContextWindows("something-new", 200_000); len(got) != 2 {
		t.Errorf("a model seen at 1M offers %v, want both windows", got)
	}
}

// The adapter's authoritative size is the model's whole window, whatever the
// session compacts at, so a later reading replaces an earlier one: an opus
// once remembered at 200k — from the adapter's guess, the way every account
// used to — offers 1M again as soon as a turn on it reports 1M, including a
// turn of a chat that compacts at 1M, whose reading earlier builds dropped as
// "only the compact window".
func TestALaterReadingHealsAWrongWindow(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.SetSetting(ctx, state.SettingClaudeModelWindows, `{"opus":200000}`); err != nil {
		t.Fatal(err)
	}
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.ContextWindows("opus", 200_000); len(got) != 1 {
		t.Fatalf("opus remembered at 200k offers %v, want only 200k", got)
	}
	if err := store.RememberClaudeModelWindow(ctx, "opus", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if w, err = store.ClaudeWindows(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := w.ContextWindows("opus", 200_000), []int64{200_000, 1_000_000}; !slices.Equal(got, want) {
		t.Errorf("ContextWindows(opus) = %v, want %v", got, want)
	}
	if got, err := w.DefaultContextWindow("opus", "1m", 200_000); err != nil || got != "1000000" {
		t.Errorf("DefaultContextWindow(opus, 1m) = %q, %v; want 1000000", got, err)
	}
}

// Launch is the one answer to what a chat runs at, for every combination a
// chat can be stored with.
func TestLaunch(t *testing.T) {
	long := state.ClaudeWindows{Seen: map[string]int64{"opus": 1_000_000}, OneM: map[string]bool{}}
	short := state.ClaudeWindows{Seen: map[string]int64{"opus": 200_000, "opus[1m]": 1_000_000}, OneM: map[string]bool{"opus": true}}
	for _, tc := range []struct {
		name          string
		w             state.ClaudeWindows
		model, chosen string
		installation  int64
		wantName      string
		wantCompact   int64
	}{
		{"nothing chosen", long, "opus", "", 200_000, "opus", 200_000},
		{"1M chosen", long, "opus", "1000000", 200_000, "opus", 1_000_000},
		{"1M written as people do", long, "opus", "1m", 200_000, "opus", 1_000_000},
		{"a stored opus[1m] from before D91", long, "opus[1m]", "", 200_000, "opus", 200_000},
		{"installation uncapped", long, "opus", "1000000", 0, "opus", 0},
		{"haiku can't have 1M", long, "haiku", "1000000", 200_000, "haiku", 200_000},
		{"the default model", long, "default", "1000000", 200_000, "default", 1_000_000},
		{"1M through the variant", short, "opus", "1000000", 200_000, "opus[1m]", 1_000_000},
		{"200k needs no variant", short, "opus", "", 200_000, "opus", 200_000},
	} {
		name, compact := tc.w.Launch(tc.model, tc.chosen, tc.installation)
		if name != tc.wantName || compact != tc.wantCompact {
			t.Errorf("%s: Launch(%q, %q, %d) = %q, %d; want %q, %d", tc.name, tc.model, tc.chosen, tc.installation, name, compact, tc.wantName, tc.wantCompact)
		}
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

// A "[1m]" variant a session really ran at 1M is the way to a long window for
// its plain model, just as one on the menu is: an account whose plain opus
// reported 200k, with no "[1m]" on its menu, still offers opus at 1M, and
// starts it as the variant.
func TestA1MVariantSeenRunningIsTheWayToALongWindow(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	if err := store.SetSetting(ctx, state.SettingClaudeModelWindows, `{"opus":200000,"opus[1m]":1000000,"haiku[1m]":200000}`); err != nil {
		t.Fatal(err)
	}
	w, err := store.ClaudeWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.ContextWindows("opus", 200_000); !slices.Equal(got, []int64{200_000, 1_000_000}) {
		t.Fatalf("ContextWindows(opus) = %v, want 200k and 1M", got)
	}
	if got, err := w.DefaultContextWindow("opus", "1m", 200_000); err != nil || got != "1000000" {
		t.Errorf("DefaultContextWindow(opus, 1m) = %q, %v", got, err)
	}
	if got := w.Model("opus", 1_000_000); got != "opus[1m]" {
		t.Errorf("opus at 1M starts as %q, want opus[1m]", got)
	}
	if got := w.Model("opus", 200_000); got != "opus" {
		t.Errorf("opus at 200k starts as %q, want opus", got)
	}
	// A variant that never reached 1M proves nothing.
	if got := w.ContextWindows("haiku", 200_000); !slices.Equal(got, []int64{200_000}) {
		t.Errorf("ContextWindows(haiku) = %v, want only 200k", got)
	}
}
