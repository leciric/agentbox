package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// usageKey is the server's rule for a feature key (agentbox-landing's
// functions/api/v1/usage.ts): every key made here has to pass it.
var usageKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}(\.[a-z0-9_]{1,32}){0,4}$`)

func TestUsageModelsComeFromTheList(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, model, effort string }{
		{"", "default", ""},
		{"default", "default", ""},
		{"opus", "opus", ""},
		{"opus[1m]", "opus", ""},
		{"claude-opus-5-5", "opus_5_5", ""},
		{"Claude-Fable-5-1", "fable_5_1", ""},
		{"claude-haiku-4-5-20251001", "haiku_4_5", ""},
		{"anthropic/claude-sonnet-5", "sonnet_5", ""},
		{"gpt-5.1-codex/high", "gpt_5_1_codex", "high"},
		{"github-copilot/gpt-5.4", "gpt_5_4", ""},
		{"google/gemini-3-pro-preview", "gemini_3_pro", ""},
		// Whatever isn't on the list is never sent as it is.
		{"my-company/llama-finetune-acme-secret", "other", ""},
		{"claude-opus-99-9", "other", ""},
		{"ft:gpt-4o:acme::abc123", "other", ""},
	} {
		model, effort := usageModel(c.in)
		if model != c.model || effort != c.effort {
			t.Errorf("usageModel(%q) = %q, %q; want %q, %q", c.in, model, effort, c.model, c.effort)
		}
	}
	for id, key := range usageModels {
		if !usageKey.MatchString("turn.model.claude." + key) {
			t.Errorf("%s's key %q isn't a feature key", id, key)
		}
	}
}

func TestUsageValuesAreFixed(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"": "default", "HIGH": "high", "xhigh": "xhigh", "turbo": "other"} {
		if got := usageEffort(in); got != want {
			t.Errorf("usageEffort(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{-1: "default", 0: "full", 200_000: "200k", 1_000_000: "1m", 123_456: "other"} {
		if got := usageWindow(in); got != want {
			t.Errorf("usageWindow(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "default", "bypassPermissions": "bypass", "acceptEdits": "accept_edits", "read-only": "read_only", "full-access": "full_access", "build": "default", "yolo": "other"} {
		if got := usageMode(in); got != want {
			t.Errorf("usageMode(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[time.Duration]string{0: "lt_1m", 3 * time.Minute: "1m_5m", time.Hour: "30m_2h", 30 * time.Hour: "1d_1w", 30 * 24 * time.Hour: "gt_1w"} {
		if got := usageSpan(in); got != want {
			t.Errorf("usageSpan(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{0: "unknown", 4 << 30: "lt_8g", 16 << 30: "16_32g", 128 << 30: "64g_plus"} {
		if got := usageMemory(in); got != want {
			t.Errorf("usageMemory(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "system", "pt-BR": "pt_br", "en-US": "en_us", "fr-FR": "other"} {
		if got := usageLanguage(in); got != want {
			t.Errorf("usageLanguage(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "host", "linux": "chv", "windows": "wsl", "darwin": "lima"} {
		if got := usageFrontEnd(in); got != want {
			t.Errorf("usageFrontEnd(%q) = %q, want %q", in, got, want)
		}
	}
}

func pendingUsage(t *testing.T, d testDaemon) api.UsageStatsPending {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := d.srv.usageStatsPending(rec, httptest.NewRequest(http.MethodGet, "/v1/usage-stats/pending", nil)); err != nil {
		t.Fatal(err)
	}
	var out api.UsageStatsPending
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUsageEventsAreRecordedShownAndSent(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	var fake fakeUsage
	d := startTestDaemon(t, t.TempDir(), fakeIncus, testConfig{updateURL: fake.start(t), releasesURL: fakeStable(t, "0.16.0")})
	ctx := context.Background()
	waitFor(t, "the check as the daemon starts", func() bool {
		status, err := d.client.Update(ctx)
		return err == nil && status.CheckedAt != nil
	})
	// A new installation begins its setup, and says how it is, once a day.
	waitFor(t, "the first events", func() bool {
		sent := fake.sentEvents()
		return len(sent[eventHeartbeat]) == 1 && len(sent[eventSetup]) == 1
	})
	if step := fake.sentEvents()[eventSetup][0]["step"]; step != setupStarted {
		t.Errorf("first setup step = %v", step)
	}
	hb := fake.sentEvents()[eventHeartbeat][0]
	if len(hb) != 13 || hb["front_end"] == "" || hb["projects"] != float64(0) {
		t.Errorf("heartbeat = %v", hb)
	}

	lead := state.Agent{Project: "p", Name: state.LeadName, AI: "claude", Role: state.RoleLead}
	d.srv.recordTurn(lead, chat.TurnStats{
		Model: "acme/secret-finetune-v2", Effort: "high", Mode: "bypassPermissions", Window: 1_000_000,
		State: "completed", Duration: 90 * time.Second, Input: 10, Cached: 20, CacheCreation: 3, Output: 4, Reasoning: 1, Subagents: true,
	})
	d.srv.recordError(errCreateFailed, "codex")
	// What is shown is what is sent, and never the model's own name.
	pending := pendingUsage(t, d)
	if !pending.On || pending.EventsWaiting != 2 || !strings.HasSuffix(pending.EventsURL, "/api/v1/events") {
		t.Fatalf("pending = %+v", pending)
	}
	if strings.Contains(pending.Events, "secret") || !strings.Contains(pending.Events, `"turn.completed"`) {
		t.Errorf("pending events = %s", pending.Events)
	}
	// The counts went in beside the event.
	_, counts, _ := d.srv.store.FeatureUsage(ctx, "9999-12-31")
	if counts[usageDay(time.Now())]["turn.model.claude.other"] != 1 || counts[usageDay(time.Now())]["turn.context.claude.1m"] != 1 {
		t.Errorf("counts = %v", counts)
	}

	// The day's heartbeat was sent: the next check sends only what's new.
	d.sendNow(t)
	sent := fake.sentEvents()
	if len(sent[eventHeartbeat]) != 1 || len(sent[eventTurn]) != 1 || len(sent[eventError]) != 1 {
		t.Fatalf("sent = %v", sent)
	}
	turn := sent[eventTurn][0]
	want := map[string]any{
		"tool": "claude", "model": "other", "effort": "high", "context": "1m", "mode": "bypass", "role": "lead",
		"outcome": "done", "duration_ms": float64(90000), "input_tokens": float64(10), "cached_tokens": float64(20),
		"cache_creation_tokens": float64(3), "output_tokens": float64(4), "reasoning_tokens": float64(1), "subagents": true,
	}
	for k, v := range want {
		if turn[k] != v {
			t.Errorf("turn.completed %s = %v, want %v", k, turn[k], v)
		}
	}
	if len(turn) != len(want) {
		t.Errorf("turn.completed has %d fields, want %d: %v", len(turn), len(want), turn)
	}
	if left := pendingUsage(t, d); left.EventsWaiting != 0 || left.Events != "" {
		t.Errorf("after sending, waiting %+v", left)
	}

	// A server that's down keeps them for the next check; one that refuses
	// them would refuse them again, so they go.
	fake.answerEvents(http.StatusServiceUnavailable)
	d.srv.recordError(errAuthFailed, "claude")
	d.sendNow(t)
	if n := pendingUsage(t, d).EventsWaiting; n != 1 {
		t.Errorf("after a 503, %d waiting, want 1", n)
	}
	fake.answerEvents(http.StatusBadRequest)
	d.sendNow(t)
	if n := pendingUsage(t, d).EventsWaiting; n != 0 {
		t.Errorf("after a 400, %d waiting, want 0", n)
	}

	// Off forgets what wasn't sent, and keeps nothing more.
	d.srv.recordError(errAuthFailed, "claude")
	if _, err := patchSettings(t, d, `{"usageStats": false}`); err != nil {
		t.Fatal(err)
	}
	d.srv.recordError(errAuthFailed, "claude")
	if rows, _ := d.srv.store.UsageEvents(ctx, 10); len(rows) != 0 {
		t.Errorf("with the stats off, kept %d events", len(rows))
	}
	if p := pendingUsage(t, d); p.On || p.Events != "" {
		t.Errorf("with the stats off, pending = %+v", p)
	}
}

func TestSetupFunnelSkipsAnInstallationInUse(t *testing.T) {
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	_ = d.srv.store.DeleteSetting(ctx, settingUsageSetupAt)
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	_ = d.srv.store.ForgetUsageEvents(ctx, 0, "")
	d.srv.beginSetupFunnel(ctx)
	d.srv.setupStep(ctx, setupFirstAgent)
	if rows, _ := d.srv.store.UsageEvents(ctx, 10); len(rows) != 0 {
		t.Errorf("an installation in use sent %d setup events", len(rows))
	}

	// A new one sends each step once.
	_ = d.srv.store.SetSetting(ctx, settingUsageSetupAt, "1")
	d.srv.setupStep(ctx, setupFirstAgent)
	d.srv.setupStep(ctx, setupFirstAgent)
	rows, _ := d.srv.store.UsageEvents(ctx, 10)
	if len(rows) != 1 || !strings.Contains(string(rows[0].Props), `"since":"gt_1w"`) {
		t.Errorf("setup events = %v", rows)
	}
}

func TestUsageEventsAreCapped(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	for i := range state.MaxUsageEvents + 3 {
		e := state.UsageEvent{ID: "id", Day: "2026-10-07", Name: eventError, Props: json.RawMessage(`{"n":` + strings.Repeat("1", 1+i%3) + `}`)}
		if err := d.srv.store.AddUsageEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := d.srv.store.UsageEvents(ctx, state.MaxUsageEvents*2)
	if len(rows) != state.MaxUsageEvents || rows[0].Seq != 4 {
		t.Errorf("kept %d, the first %d", len(rows), rows[0].Seq)
	}
}
