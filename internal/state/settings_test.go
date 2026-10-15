package state_test

import (
	"context"
	"path/filepath"
	"testing"

	"agentbox/internal/state"
)

func TestSettings(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	// A setting nobody has touched reads as empty, not as an error: that's
	// what "use AgentBox's own default" looks like.
	got, err := st.Setting(ctx, state.SettingDefaultClaudeModel)
	if err != nil || got != "" {
		t.Fatalf("Setting() on a fresh store = %q, %v", got, err)
	}

	if err := st.SetSetting(ctx, state.SettingDefaultClaudeModel, "opus"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Setting(ctx, state.SettingDefaultClaudeModel); err != nil || got != "opus" {
		t.Fatalf("Setting() = %q, %v, want %q", got, err, "opus")
	}

	// Choosing again replaces, rather than failing on the key or keeping both.
	if err := st.SetSetting(ctx, state.SettingDefaultClaudeModel, "haiku"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Setting(ctx, state.SettingDefaultClaudeModel); err != nil || got != "haiku" {
		t.Fatalf("Setting() after a second choice = %q, %v, want %q", got, err, "haiku")
	}

	// Going back to the empty value is how you return to AgentBox's default.
	if err := st.SetSetting(ctx, state.SettingDefaultClaudeModel, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Setting(ctx, state.SettingDefaultClaudeModel); err != nil || got != "" {
		t.Fatalf("Setting() after clearing = %q, %v", got, err)
	}

	// Settings don't collide with each other.
	if err := st.SetSetting(ctx, state.SettingClaudeModelChoices, `[{"value":"opus","name":"Opus 5"}]`); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Setting(ctx, state.SettingDefaultClaudeModel); got != "" {
		t.Errorf("writing the menu changed the chosen model to %q", got)
	}
}

// A brand-new database starts its agents and its lead on the fresh-install
// settings, and reopening it, as an upgrade does, never writes them again: a
// setting an installation has left unset stays unset.
func TestFreshInstallSettingsAreSeededOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	ctx := context.Background()

	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		state.SettingDefaultAgentContextWindow: "1000000",
		state.SettingDefaultLeadModel:          "opus",
		state.SettingDefaultLeadContextWindow:  "1000000",
	} {
		if got, err := st.Setting(ctx, key); err != nil || got != want {
			t.Errorf("a fresh database's %s = %q, %v; want %q", key, got, err, want)
		}
	}
	// Left alone by the seeding: these read the code's own defaults.
	if got, err := st.Setting(ctx, state.SettingDefaultClaudeModel); err != nil || got != "" {
		t.Errorf("a fresh database's %s = %q, %v; want unset", state.SettingDefaultClaudeModel, got, err)
	}
	if err := st.DeleteSetting(ctx, state.SettingDefaultAgentContextWindow); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if got, err := st.Setting(ctx, state.SettingDefaultAgentContextWindow); err != nil || got != "" {
		t.Errorf("reopening wrote the fresh-install setting back: %q, %v; want it left unset", got, err)
	}
}
