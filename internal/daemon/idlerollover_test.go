package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/state"
)

// The timer fires a margin before the cache expires: a tenth of it for a
// short cache, five minutes for a long one.
func TestIdleRolloverDelay(t *testing.T) {
	for ttl, want := range map[time.Duration]time.Duration{
		5 * time.Minute: 4*time.Minute + 30*time.Second,
		time.Hour:       55 * time.Minute,
	} {
		if got := idleRolloverDelay(ttl); got != want {
			t.Errorf("idleRolloverDelay(%s) = %s, want %s", ttl, got, want)
		}
	}
}

func TestPastLimits(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	later := now.Add(time.Hour).Unix()
	for _, c := range []struct {
		name    string
		reading acp.RateLimit
		want    bool
	}{
		{"within", acp.RateLimit{Status: "allowed", ResetsAt: later}, false},
		{"warned", acp.RateLimit{Status: "allowed_warning", ResetsAt: later}, false},
		{"refused", acp.RateLimit{Status: "rejected", ResetsAt: later}, true},
		{"overage", acp.RateLimit{Status: "allowed", IsUsingOverage: true, ResetsAt: later}, true},
		{"refused, since reset", acp.RateLimit{Status: "rejected", ResetsAt: now.Add(-time.Minute).Unix()}, false},
	} {
		if got := pastLimits(c.reading, now); got != c.want {
			t.Errorf("%s: pastLimits = %v, want %v", c.name, got, c.want)
		}
	}
}

// fakeTimer is a timer that is only ever fired by hand.
type fakeTimer struct {
	delay   time.Duration
	fire    func()
	stopped bool
}

func (f *fakeTimer) Stop() bool { f.stopped = true; return true }

// Every turn that ends sets the timer again, from the TTL the lead's settings
// name or the one Claude Code would pick; a timer that was replaced does
// nothing when it fires anyway.
func TestLeadIdleSchedulesBeforeTheCacheExpires(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	var timers []*fakeTimer
	d.srv.idleAfter = func(delay time.Duration, f func()) interface{ Stop() bool } {
		timer := &fakeTimer{delay: delay, fire: f}
		timers = append(timers, timer)
		return timer
	}
	lead := state.Agent{Project: "p", Name: state.LeadName, AI: "claude", Role: state.RoleLead}
	settings := filepath.Join(d.paths.LeadHome("p"), ".claude", "settings.json")
	writeSettings := func(doc string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	last := func() *fakeTimer { return timers[len(timers)-1] }

	// No login and no setting: Claude Code gives it five minutes.
	d.srv.leadIdle(lead)
	if got := last().delay; got != 4*time.Minute+30*time.Second {
		t.Errorf("with no login, the timer is set for %s", got)
	}

	// A subscription within its limits keeps it an hour.
	creds := d.srv.manager(nil).Creds
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-test"); err != nil {
		t.Fatal(err)
	}
	d.srv.leadIdle(lead)
	if !timers[0].stopped {
		t.Error("a new turn didn't call off the timer the last one set")
	}
	if got := last().delay; got != 55*time.Minute {
		t.Errorf("on a subscription, the timer is set for %s", got)
	}

	// Past its limits, five minutes again.
	account, _ := creds.ClaudeAccountOf("")
	raw, _ := json.Marshal(acp.RateLimit{Status: "allowed", IsUsingOverage: true, ResetsAt: time.Now().Add(time.Hour).Unix()})
	if err := d.srv.store.SetClaudeLimit(context.Background(), account, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	d.srv.leadIdle(lead)
	if got := last().delay; got != 4*time.Minute+30*time.Second {
		t.Errorf("past the limits, the timer is set for %s", got)
	}

	// What the settings say wins, the variable over the key.
	writeSettings(`{"promptCacheTtl": "1h"}`)
	d.srv.leadIdle(lead)
	if got := last().delay; got != 55*time.Minute {
		t.Errorf("with promptCacheTtl 1h, the timer is set for %s", got)
	}
	writeSettings(`{"promptCacheTtl": "1h", "env": {"CLAUDE_CODE_PROMPT_CACHE_TTL": "5m"}}`)
	d.srv.leadIdle(lead)
	if got := last().delay; got != 4*time.Minute+30*time.Second {
		t.Errorf("with the variable at 5m, the timer is set for %s", got)
	}

	// A stale timer firing leaves the current one in place.
	timers[0].fire()
	d.srv.mu.Lock()
	pending := d.srv.idleTimers["p"].timer
	d.srv.mu.Unlock()
	if pending != last() {
		t.Error("a replaced timer firing took the current one with it")
	}
	// The current one fires into a project that doesn't exist, and does nothing.
	last().fire()

	// Codex and OpenCode have no cache TTL to plan around.
	before := len(timers)
	d.srv.leadIdle(state.Agent{Project: "p", Name: state.LeadName, AI: "codex", Role: state.RoleLead})
	if len(timers) != before {
		t.Error("a Codex lead was given an idle rollover")
	}
}
