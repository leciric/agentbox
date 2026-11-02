package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// setupFields is every field of the setup event, as the landing site's
// events.ts takes it: a field added to setupEvent fails
// TestSetupFieldsAreAllListed until it is listed here (and in events.ts).
var setupFields = map[string]healthField{
	"step":         healthEnum("the step of Setup reached, or failed", setupSteps...),
	"outcome":      healthEnum("how it went; missing and no_permission are kvm_check's", setupOutcomes...),
	"duration_s":   healthInt("how long the step itself ran, where AgentBox runs it; 0 for a step that is a moment", 0, setupMaxSeconds),
	"since":        healthEnum("how long after the setup began, bucketed (the first funnel's field)", healthSpans...),
	"since_s":      healthInt("how long after the setup began, seconds", 0, setupMaxSeconds),
	"error_class":  healthEnum("why a step failed; none when it didn't", append([]string{setupNone}, errorClasses...)...),
	"image_source": healthEnum("where image_ready's base image came from; none for other steps", append([]string{setupNone}, imageSources...)...),
	"tool":         healthEnum("the AI tool of ai_signed_in; none for other steps", append([]string{setupNone}, setupSignedInTools...)...),
	"front_end":    healthEnum("what runs AgentBox's VM", "chv", "lima", "lima_krunkit", "lima_vz", "wsl", "host", "other"),
}

func TestSetupFieldsAreAllListed(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(setupEvent{})
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	if err := json.Unmarshal(raw, &props); err != nil {
		t.Fatal(err)
	}
	listed := slices.Sorted(maps.Keys(setupFields))
	if got := slices.Sorted(maps.Keys(props)); !slices.Equal(got, listed) {
		t.Fatalf("the setup event's fields differ from those listed:\n sent   %v\n listed %v", got, listed)
	}
}

// setupProps are the setup events kept, oldest first, each checked against
// setupFields.
func setupProps(t *testing.T, d testDaemon) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range setupEvents(d) {
		var props map[string]any
		if err := json.Unmarshal(r.Props, &props); err != nil {
			t.Fatal(err)
		}
		delete(props, "utc_offset") // every event's, checked with the others
		for k, v := range props {
			f, ok := setupFields[k]
			if !ok {
				t.Errorf("%s isn't a setup field", k)
			} else if msg := f.check(v); msg != "" {
				t.Errorf("%s: %s", k, msg)
			}
		}
		out = append(out, props)
	}
	return out
}

// statsOnDaemon is a daemon whose usage stats are on, with its own loop's
// setup_started kept and forgotten, and the funnel begun at began.
func statsOnDaemon(t *testing.T, began time.Time) testDaemon {
	t.Helper()
	t.Setenv("AGENTBOX_NO_UPDATE_CHECK", "")
	t.Setenv("DO_NOT_TRACK", "")
	asVersion(t, "0.16.0")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	waitFor(t, "the daemon's own setup_started", func() bool { return len(setupEvents(d)) > 0 })
	first := setupProps(t, d)[0]
	if first["step"] != setupStarted || first["outcome"] != setupOK || first["duration_s"] != float64(0) || first["error_class"] != setupNone {
		t.Errorf("setup_started = %v", first)
	}
	_ = d.srv.store.ForgetUsageEvents(ctx, 0, "")
	_ = d.srv.store.SetSetting(ctx, settingUsageSetupAt, strconv.FormatInt(began.Unix(), 10))
	_ = d.srv.store.SetSetting(ctx, settingUsageSetupSteps, setupStarted)
	return d
}

func TestSetupFunnelTakesTheFrontEndsSteps(t *testing.T) {
	now := time.Now()
	d := statsOnDaemon(t, now.Add(-10*time.Minute))
	ctx := context.Background()
	at := func(ago time.Duration) int64 { return now.Add(-ago).Unix() }
	report := api.SetupFunnelReport{Began: at(15 * time.Minute), Steps: []api.SetupFunnelStep{
		{Step: setupVMReady, Outcome: setupOK, At: at(time.Minute), DurationS: 240},
		{Step: setupKVM, Outcome: setupKVMMissing, At: at(15 * time.Minute)},
		{Step: setupVMReady, Outcome: setupFailed, At: at(10 * time.Minute), DurationS: 30, Error: "fetching the VM's kernel from /home/someone: dial tcp: connection refused"},
		{Step: setupKVM, Outcome: setupOK, At: at(5 * time.Minute)},
		// What only a front end may say, or doesn't say at all, is dropped.
		{Step: setupFirstAgent, Outcome: setupOK, At: at(time.Minute)},
		{Step: setupVMReady, Outcome: setupKVMForbidden, At: at(time.Minute)},
		{Step: "made_up", Outcome: setupOK, At: at(time.Minute)},
	}}
	if err := d.client.ReportSetupFunnel(ctx, report); err != nil {
		t.Fatal(err)
	}
	// Handed over twice, as after two inits: kept once.
	if err := d.client.ReportSetupFunnel(ctx, report); err != nil {
		t.Fatal(err)
	}
	got := setupProps(t, d)
	want := []map[string]any{
		{"step": setupKVM, "outcome": setupKVMMissing, "error_class": classNotFound, "since_s": float64(0)},
		{"step": setupVMReady, "outcome": setupFailed, "error_class": classNetwork, "duration_s": float64(30), "since_s": float64(300)},
		{"step": setupKVM, "outcome": setupOK, "error_class": setupNone, "since_s": float64(600)},
		{"step": setupVMReady, "outcome": setupOK, "error_class": setupNone, "duration_s": float64(240), "since_s": float64(840), "since": "5m_30m"},
	}
	if len(got) != len(want) {
		t.Fatalf("setup events = %v", got)
	}
	for i, w := range want {
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("event %d (%s) %s = %v, want %v", i, w["step"], k, got[i][k], v)
			}
		}
	}
	// The setup began with the front end's first step, not the daemon's start.
	if at, _ := d.srv.store.Setting(ctx, settingUsageSetupAt); at != strconv.FormatInt(report.Began, 10) {
		t.Errorf("setup began at %s, want %d", at, report.Began)
	}

	// With the stats off, nothing the front end says is kept.
	_ = d.srv.store.ForgetUsageEvents(ctx, 0, "")
	_ = d.srv.store.SetSetting(ctx, settingUsageSetupSteps, "")
	if err := d.srv.store.SetFlag(ctx, state.SettingUsageStats, false); err != nil {
		t.Fatal(err)
	}
	if err := d.client.ReportSetupFunnel(ctx, report); err != nil {
		t.Fatal(err)
	}
	if rows := setupEvents(d); len(rows) != 0 {
		t.Errorf("with the stats off, kept %d setup events", len(rows))
	}
}

func TestSetupFunnelStepsOnceEachWithHowTheyWent(t *testing.T) {
	d := statsOnDaemon(t, time.Now().Add(-time.Hour))
	ctx := context.Background()

	d.srv.setupStepFailed(ctx, setupReport{step: setupImageReady, imageSource: "built"}, errors.New("sha256 mismatch"))
	d.srv.setupReached(ctx, setupReport{step: setupImageReady, imageSource: "downloaded", took: 312 * time.Second})
	// Signed in, seen by Setup's checks: the first tool's, once.
	d.srv.noteSetupChecks(ctx, []api.SetupCheck{{ID: "image", Status: api.SetupOK}, {ID: "claude", Status: api.SetupMissing}, {ID: "codex", Status: api.SetupOK}})
	d.srv.noteSetupChecks(ctx, []api.SetupCheck{{ID: "claude", Status: api.SetupOK}})
	d.srv.setupStep(ctx, setupFirstProject)
	// The first agent fails, the same way twice, then is made; one failing
	// after that isn't where anyone stopped.
	d.srv.setupStepFailed(ctx, setupReport{step: setupFirstAgent}, context.Canceled)
	d.srv.setupStepFailed(ctx, setupReport{step: setupFirstAgent}, context.Canceled)
	d.srv.setupReached(ctx, setupReport{step: setupFirstAgent, took: 42 * time.Second})
	d.srv.setupStepFailed(ctx, setupReport{step: setupFirstAgent}, errors.New("no space left on device"))
	// A failure with a class no list has is "other".
	d.srv.setupStepFailed(ctx, setupReport{step: setupFirstPR, class: "made_up"}, nil)

	got := setupProps(t, d)
	want := []map[string]any{
		{"step": setupImageReady, "outcome": setupFailed, "error_class": classChecksum, "image_source": "built", "tool": setupNone},
		{"step": setupImageReady, "outcome": setupOK, "error_class": setupNone, "image_source": "downloaded", "duration_s": float64(312)},
		{"step": setupSignedIn, "outcome": setupOK, "tool": "codex", "image_source": setupNone, "duration_s": float64(0)},
		{"step": setupFirstProject, "outcome": setupOK, "since": "30m_2h"},
		{"step": setupFirstAgent, "outcome": setupFailed, "error_class": classCancelled},
		{"step": setupFirstAgent, "outcome": setupOK, "duration_s": float64(42)},
		{"step": setupFirstPR, "outcome": setupFailed, "error_class": classOther},
	}
	if len(got) != len(want) {
		t.Fatalf("setup events = %v", got)
	}
	for i, w := range want {
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("event %d (%s) %s = %v, want %v", i, w["step"], k, got[i][k], v)
			}
		}
	}
	// Shown apart from the other events, as they are sent.
	if p := pendingUsage(t, d); !strings.Contains(p.Setup, `"first_pr"`) || strings.Contains(p.Events, `"setup"`) {
		t.Errorf("pending = %+v", p)
	}
}
