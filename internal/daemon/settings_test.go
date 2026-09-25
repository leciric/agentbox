package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// patchSettings sends a PATCH /v1/settings body the way the app does.
func patchSettings(t *testing.T, d testDaemon, body string) (api.Settings, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/v1/settings", strings.NewReader(body))
	w := httptest.NewRecorder()
	if err := d.srv.updateSettings(w, r); err != nil {
		return api.Settings{}, err
	}
	var out api.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestResourceDefaultsAreSeededThenChosen covers the one thing the resource
// settings do that the Claude Code settings don't: "" is a real choice there
// (new agents get no limit at all), so it can't also mean "nobody chose". The
// daemon seeds a concrete value once, and never writes over what was chosen —
// including a choice of no limit, which a re-seed would silently undo.
func TestResourceDefaultsAreSeededThenChosen(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()

	seeded := agent.DefaultLimits(agent.HostCores())
	value, set, err := d.srv.store.SettingValue(ctx, state.SettingDefaultCPU)
	if err != nil || !set || value != seeded.CPU {
		t.Fatalf("the CPU default after a first start = %q, set %v, %v; want %q", value, set, err, seeded.CPU)
	}
	// Seeded, not invented on every read: the app's input shows a number, and
	// the memory ceiling and the share start off deliberately empty.
	for _, key := range []string{state.SettingDefaultCPUAllowance, state.SettingDefaultMemory} {
		if value, set, err := d.srv.store.SettingValue(ctx, key); err != nil || !set || value != "" {
			t.Errorf("%s = %q, set %v, %v; want it stored and empty", key, value, set, err)
		}
	}

	out, err := patchSettings(t, d, `{"defaultCPU":"","defaultMemory":"8GiB","defaultCPUAllowance":"50%"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.DefaultCPU != "" || out.DefaultMemory != "8GiB" || out.DefaultCPUAllowance != "50%" {
		t.Errorf("settings after the change = %+v", out)
	}
	if out.HostCores != agent.HostCores() {
		t.Errorf("hostCores = %d, want %d: the app shows what a limit is carved out of", out.HostCores, agent.HostCores())
	}

	// Another daemon start must leave the choice of "no CPU limit" alone.
	if err := d.srv.seedResourceDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if value, _, _ := d.srv.store.SettingValue(ctx, state.SettingDefaultCPU); value != "" {
		t.Errorf("a restart put the CPU default back to %q; a chosen empty limit is a choice", value)
	}
}

// TestSettingsRefuseLimitsThatDontMeanWhatTheySay checks a bad default is
// refused where it is typed, rather than when the next agent is built.
func TestSettingsRefuseLimitsThatDontMeanWhatTheySay(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	for _, c := range []struct{ body, want string }{
		{`{"defaultCPU":"0-3"}`, "pin the agent to those exact cores"},
		{`{"defaultCPU":"two"}`, "whole number of cores"},
		{`{"defaultCPUAllowance":"200%"}`, "between 1% and 100%"},
		{`{"defaultMemory":"loads"}`, "a size like 8GiB"},
	} {
		if _, err := patchSettings(t, d, c.body); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("PATCH %s error = %v, want one mentioning %q", c.body, err, c.want)
		}
	}
	// And nothing was stored on the way to refusing.
	if value, _, _ := d.srv.store.SettingValue(context.Background(), state.SettingDefaultCPU); value == "0-3" {
		t.Error("a refused CPU default was stored anyway")
	}
}

// Resuming after a usage limit is on for an installation that has never
// chosen: it is the setting whose useful state is the one you get without
// touching it, and only turning it off is a choice worth storing.
func TestResumeAfterLimitIsOnUntilItIsTurnedOff(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	out, err := d.srv.currentSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if !out.ResumeAfterLimit {
		t.Error("an installation that has never chosen doesn't resume after a usage limit")
	}
	if out, err := patchSettings(t, d, `{"resumeAfterLimit":false}`); err != nil || out.ResumeAfterLimit {
		t.Errorf("after turning it off: %+v, %v", out, err)
	}
	// And a request that says nothing about it leaves it off.
	if out, err := patchSettings(t, d, `{"defaultMemory":"8GiB"}`); err != nil || out.ResumeAfterLimit {
		t.Errorf("another setting turned it back on: %+v, %v", out, err)
	}
	if out, err := patchSettings(t, d, `{"resumeAfterLimit":true}`); err != nil || !out.ResumeAfterLimit {
		t.Errorf("after turning it back on: %+v, %v", out, err)
	}
}

// TestClaudeMenuIsKnownOnceAnAdapterSentOne: the pinned models are always on
// the menu, so the app can't tell from the choices alone that Claude Code
// hasn't sent the account's own yet, and says so from this.
func TestClaudeMenuIsKnownOnceAnAdapterSentOne(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)

	out, err := d.srv.currentSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if out.ClaudeMenuKnown || len(out.ClaudeModelChoices) == 0 {
		t.Errorf("before any chat: known %v, with %d choices; want the pinned ones, not known", out.ClaudeMenuKnown, len(out.ClaudeModelChoices))
	}

	menu, _ := json.Marshal([]api.ChatOptionChoice{{Value: "claude-opus-5-5", Name: "Opus 5.5"}})
	if err := d.srv.store.SetSetting(context.Background(), state.SettingClaudeModelChoices, string(menu)); err != nil {
		t.Fatal(err)
	}
	if out, err = d.srv.currentSettings(r); err != nil || !out.ClaudeMenuKnown {
		t.Errorf("after an adapter sent a menu: known %v, %v", out.ClaudeMenuKnown, err)
	}
}
