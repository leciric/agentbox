package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The card shows a margin before the cache expires: a tenth of it for a
// short cache, five minutes for a long one.
func TestCacheCardDelay(t *testing.T) {
	for ttl, want := range map[time.Duration]time.Duration{
		5 * time.Minute: 4*time.Minute + 30*time.Second,
		time.Hour:       55 * time.Minute,
	} {
		if got := cacheCardDelay(ttl); got != want {
			t.Errorf("cacheCardDelay(%s) = %s, want %s", ttl, got, want)
		}
	}
}

func TestWorthACard(t *testing.T) {
	for used, want := range map[int64]bool{0: false, 42_000: false, 59_999: false, 60_000: true, 150_000: true} {
		if got := worthACard(used); got != want {
			t.Errorf("worthACard(%d) = %v, want %v", used, got, want)
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
	d.srv.leadCacheIdle(lead)
	if got := last().delay; got != 4*time.Minute+30*time.Second {
		t.Errorf("with no login, the timer is set for %s", got)
	}

	// A subscription within its limits keeps it an hour.
	creds := d.srv.manager(nil).Creds
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-test"); err != nil {
		t.Fatal(err)
	}
	d.srv.leadCacheIdle(lead)
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
	d.srv.leadCacheIdle(lead)
	if got := last().delay; got != 4*time.Minute+30*time.Second {
		t.Errorf("past the limits, the timer is set for %s", got)
	}

	// What the settings say wins, the variable over the key.
	writeSettings(`{"promptCacheTtl": "1h"}`)
	d.srv.leadCacheIdle(lead)
	if got := last().delay; got != 55*time.Minute {
		t.Errorf("with promptCacheTtl 1h, the timer is set for %s", got)
	}
	writeSettings(`{"promptCacheTtl": "1h", "env": {"CLAUDE_CODE_PROMPT_CACHE_TTL": "5m"}}`)
	d.srv.leadCacheIdle(lead)
	if got := last().delay; got != 4*time.Minute+30*time.Second {
		t.Errorf("with the variable at 5m, the timer is set for %s", got)
	}

	// A stale timer firing leaves the current one in place.
	timers[0].fire()
	d.srv.mu.Lock()
	pending := d.srv.leadCaches["p"].timer
	d.srv.mu.Unlock()
	if pending != last() {
		t.Error("a replaced timer firing took the current one with it")
	}
	// The current one fires into a project that doesn't exist: no card.
	last().fire()
	if d.srv.leadCacheState("p").Due {
		t.Error("a project that doesn't exist got a card")
	}

	// Codex and OpenCode have no cache TTL to plan around.
	before := len(timers)
	d.srv.leadCacheIdle(state.Agent{Project: "p", Name: state.LeadName, AI: "codex", Role: state.RoleLead})
	if len(timers) != before {
		t.Error("a Codex lead was given a cache card")
	}
}

// The card is state the API shows and the events carry: it goes when a turn
// ends and when the user has chosen, and a compaction forgets the cache.
func TestCacheCardComesAndGoes(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	d.srv.idleAfter = func(time.Duration, func()) interface{ Stop() bool } { return &fakeTimer{} }
	events, unsubscribe := d.srv.events.subscribe()
	defer unsubscribe()
	cards := func() []api.ChatCache {
		t.Helper()
		var out []api.ChatCache
		for {
			select {
			case ev := <-events:
				if ev.Type != api.EventChatCache {
					continue
				}
				var c api.ChatCache
				if err := json.Unmarshal(ev.Data, &c); err != nil {
					t.Fatal(err)
				}
				out = append(out, c)
			default:
				return out
			}
		}
	}
	up := func() {
		d.srv.mu.Lock()
		d.srv.leadCaches["p"].due = true
		d.srv.mu.Unlock()
	}
	lead := state.Agent{Project: "p", Name: state.LeadName, AI: "claude", Role: state.RoleLead}

	if got := d.srv.leadCacheState("p"); got.IdleSince != nil || got.Due {
		t.Errorf("before any turn, the cache is %+v", got)
	}
	start := time.Now()
	d.srv.leadCacheIdle(lead)
	got := d.srv.leadCacheState("p")
	if got.IdleSince == nil || got.IdleSince.Before(start) || got.TTLSeconds != 300 || got.TTLSource != "no subscription" || got.Due {
		t.Fatalf("after a turn, the cache is %+v", got)
	}
	if want := got.IdleSince.Add(4*time.Minute + 30*time.Second); !got.DueAt.Equal(want) {
		t.Errorf("the card is due at %s, want %s", got.DueAt, want)
	}
	if want := got.IdleSince.Add(5 * time.Minute); !got.ExpiresAt.Equal(want) {
		t.Errorf("the cache expires at %s, want %s", got.ExpiresAt, want)
	}
	if c := cards(); len(c) != 0 {
		t.Errorf("a turn ending with no card up published %d cards", len(c))
	}

	// A turn ending takes the card down, whoever started it.
	up()
	d.srv.leadCacheIdle(lead)
	if c := cards(); len(c) != 1 || c[0].Due {
		t.Errorf("a turn ending with the card up published %+v", c)
	}

	// Sending as it is takes it down and keeps the cache.
	up()
	d.srv.settleLeadCache("p", false)
	if c := cards(); len(c) != 1 || c[0].Due || c[0].IdleSince == nil {
		t.Errorf("sending as it is published %+v", c)
	}
	// Compacting forgets the cache: the next session starts one.
	up()
	d.srv.settleLeadCache("p", true)
	if c := cards(); len(c) != 1 || c[0].Due || c[0].IdleSince != nil {
		t.Errorf("compacting published %+v", c)
	}
	d.srv.settleLeadCache("p", true)
	if c := cards(); len(c) != 0 {
		t.Errorf("settling a cache nobody tracks published %+v", c)
	}
}
