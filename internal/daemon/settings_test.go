package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// "GPU for agents" is off until it is turned on, and Settings says what the
// VM has to give, or why it has nothing.
func TestAgentGPUIsOffUntilItIsTurnedOn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	out, err := d.srv.currentSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if out.AgentGPU {
		t.Error("an installation that has never chosen gives agents the GPU")
	}
	if (out.VMGPU.Kind == "") == (out.VMGPU.Reason == "") {
		t.Errorf("a GPU with a reason, or none without one: %+v", out.VMGPU)
	}
	if out, err := patchSettings(t, d, `{"agentGPU":true}`); err != nil || !out.AgentGPU {
		t.Errorf("after turning it on: %+v, %v", out.AgentGPU, err)
	}
	if out, err := patchSettings(t, d, `{"prWatch":true}`); err != nil || !out.AgentGPU {
		t.Errorf("another setting turned it off: %+v, %v", out.AgentGPU, err)
	}
	if out, err := patchSettings(t, d, `{"agentGPU":false}`); err != nil || out.AgentGPU {
		t.Errorf("after turning it off: %+v, %v", out.AgentGPU, err)
	}
}

func TestContinueAfterRestartIsOnUntilItIsTurnedOff(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	out, err := d.srv.currentSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if !out.ContinueAfterRestart {
		t.Error("an installation that has never chosen doesn't continue agents after restarts")
	}
	if out, err := patchSettings(t, d, `{"continueAfterRestart":false}`); err != nil || out.ContinueAfterRestart {
		t.Errorf("after turning it off: %+v, %v", out, err)
	}
	if out, err := patchSettings(t, d, `{"resumeAfterLimit":true}`); err != nil || out.ContinueAfterRestart {
		t.Errorf("another setting turned it back on: %+v, %v", out, err)
	}
	if out, err := patchSettings(t, d, `{"continueAfterRestart":true}`); err != nil || !out.ContinueAfterRestart {
		t.Errorf("after turning it back on: %+v, %v", out, err)
	}
}

func TestCreditAIToolIsOffUntilItIsTurnedOn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)

	r := httptest.NewRequest(http.MethodGet, "/v1/settings", nil)
	out, err := d.srv.currentSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if out.CreditAITool {
		t.Error("an installation that has never chosen credits the AI tool")
	}
	if out, err := patchSettings(t, d, `{"creditAITool":true}`); err != nil || !out.CreditAITool {
		t.Errorf("after turning it on: %+v, %v", out, err)
	}
	if out, err := patchSettings(t, d, `{"resumeAfterLimit":true}`); err != nil || !out.CreditAITool {
		t.Errorf("another setting turned it back off: %+v, %v", out, err)
	}
	if out, err := patchSettings(t, d, `{"creditAITool":false}`); err != nil || out.CreditAITool {
		t.Errorf("after turning it back off: %+v, %v", out, err)
	}
}

func TestBuiltWithBadgeIsOffAndOfferedOnce(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	get := func() api.Settings {
		out, err := d.srv.currentSettings(httptest.NewRequest(http.MethodGet, "/v1/settings", nil))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := get(); out.BuiltWithBadge || out.BuiltWithBadgeOffer {
		t.Fatalf("a fresh installation has the badge or its offer: %+v", out)
	}
	ctx := context.Background()
	if err := d.srv.store.OfferBuiltWithBadge(ctx); err != nil {
		t.Fatal(err)
	}
	if out := get(); out.BuiltWithBadge || !out.BuiltWithBadgeOffer {
		t.Fatalf("after a merge: %+v", out)
	}
	if out, err := patchSettings(t, d, `{"dismissBuiltWithBadgeOffer":true}`); err != nil || out.BuiltWithBadgeOffer || out.BuiltWithBadge {
		t.Fatalf("after dismissing: %+v, %v", out, err)
	}
	if err := d.srv.store.OfferBuiltWithBadge(ctx); err != nil {
		t.Fatal(err)
	}
	if get().BuiltWithBadgeOffer {
		t.Error("a later merge offered it again")
	}
	if out, err := patchSettings(t, d, `{"builtWithBadge":true}`); err != nil || !out.BuiltWithBadge {
		t.Errorf("after turning it on: %+v, %v", out, err)
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

// TestLeadAndAgentDefaultsAreSeparate: Settings has a Lead section and an
// Agents section, each with its own model and context window. An installation
// that never chose has what it had before they were split — agents on
// AgentBox's own default, leads on Claude Code's, both at the compact window —
// and choosing one role's never moves the other's. A fresh installation starts
// its lead on opus at 1M (state.freshInstallSettings), so the installation
// from before the split is made by taking those rows back out.
func TestLeadAndAgentDefaultsAreSeparate(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	// An installation from before the split chose a model for its agents.
	if err := d.srv.store.SetSetting(ctx, state.SettingDefaultClaudeModel, "sonnet"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{state.SettingDefaultAgentContextWindow, state.SettingDefaultLeadModel, state.SettingDefaultLeadContextWindow} {
		if err := d.srv.store.DeleteSetting(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	out, err := patchSettings(t, d, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.DefaultClaudeModel != "sonnet" || out.DefaultAgentContextWindow != "" || out.DefaultLeadModel != "" || out.DefaultLeadContextWindow != "" {
		t.Fatalf("defaults after upgrading = %+v, want the agents' model kept and everything else as it was", out)
	}

	out, err = patchSettings(t, d, `{"defaultLeadModel":"opus","defaultLeadContextWindow":"1m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.DefaultLeadModel != "opus" || out.DefaultLeadContextWindow != "1000000" {
		t.Errorf("lead defaults = %q, %q; want opus at 1M", out.DefaultLeadModel, out.DefaultLeadContextWindow)
	}
	if out.DefaultClaudeModel != "sonnet" || out.DefaultAgentContextWindow != "" {
		t.Errorf("choosing the lead's defaults moved the agents': %q, %q", out.DefaultClaudeModel, out.DefaultAgentContextWindow)
	}

	// Haiku has no 1M window: not as the window chosen with it...
	if _, err := patchSettings(t, d, `{"defaultClaudeModel":"haiku","defaultAgentContextWindow":"1m"}`); err == nil {
		t.Error("new agents were given Haiku with a 1M window")
	}
	if model, _ := d.srv.store.Setting(ctx, state.SettingDefaultClaudeModel); model != "sonnet" {
		t.Errorf("a refused request still stored its model: %q", model)
	}
	// ...nor as a model moved under a 1M window already chosen.
	if _, err := patchSettings(t, d, `{"defaultLeadModel":"haiku"}`); err == nil || !strings.Contains(err.Error(), "the compact window") {
		t.Errorf("the lead moved to Haiku under its 1M window: err = %v", err)
	}
	out, err = patchSettings(t, d, `{"defaultLeadModel":"haiku","defaultLeadContextWindow":"200k"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.DefaultLeadModel != "haiku" || out.DefaultLeadContextWindow != "" {
		t.Errorf("lead defaults = %q, %q; want Haiku at the compact window", out.DefaultLeadModel, out.DefaultLeadContextWindow)
	}
	// Anything but the two windows is the compact window's own setting.
	if _, err := patchSettings(t, d, `{"defaultAgentContextWindow":"300k"}`); err == nil {
		t.Error("a 300k default window was accepted")
	}
}

// Settings offers both roles 200k and 1M on opus, and on Claude Code's own
// default — before any chat has remembered the adapter's menu, and after a
// session reported opus's whole window.
func TestOpusOffersItsWholeWindowInSettings(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.srv.store.RememberClaudeModelWindow(ctx, "opus", 1_000_000); err != nil {
		t.Fatal(err)
	}
	out, err := patchSettings(t, d, `{"defaultLeadModel":"opus"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"opus", "default"} {
		if got := out.ClaudeContextWindows[model]; len(got) != 2 || got[1] != state.ClaudeFullWindow {
			t.Errorf("claudeContextWindows[%q] = %v, want 200k and 1M", model, got)
		}
	}
	if _, err := patchSettings(t, d, `{"defaultAgentContextWindow":"1m","defaultLeadContextWindow":"1m"}`); err != nil {
		t.Errorf("1M on opus for both roles: %v", err)
	}
	if _, err := patchSettings(t, d, `{"defaultLeadModel":"","defaultLeadContextWindow":"1m"}`); err != nil {
		t.Errorf("1M on Claude Code's default for the lead: %v", err)
	}
}

// The app's language is kept by the daemon like any other setting: en-US until
// somebody picks another, any well-formed tag accepted (the app owns the list),
// and anything that can't be a tag refused without touching what's stored.
func TestLanguageIsStoredAndChecked(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)

	out, err := d.srv.currentSettings(httptest.NewRequest(http.MethodGet, "/v1/settings", nil))
	if err != nil || out.Language != api.DefaultLanguage {
		t.Fatalf("a fresh installation speaks %q, %v; want %q", out.Language, err, api.DefaultLanguage)
	}
	if out, err := patchSettings(t, d, `{"language":"pt-BR"}`); err != nil || out.Language != "pt-BR" {
		t.Fatalf("after choosing pt-BR: %q, %v", out.Language, err)
	}
	for _, bad := range []string{"Portuguese", "pt_BR", "../x", "e"} {
		if _, err := patchSettings(t, d, `{"language":"`+bad+`"}`); err == nil {
			t.Errorf("%q was accepted as a language", bad)
		}
	}
	if out, err := patchSettings(t, d, `{"prWatch":true}`); err != nil || out.Language != "pt-BR" {
		t.Errorf("another setting changed the language: %q, %v", out.Language, err)
	}
	if out, err := patchSettings(t, d, `{"language":""}`); err != nil || out.Language != api.DefaultLanguage {
		t.Errorf("clearing it: %q, %v; want the default", out.Language, err)
	}
}
