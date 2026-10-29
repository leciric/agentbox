package daemon

import (
	"context"
	"encoding/json"
	"errors"
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
	t.Parallel()
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
	t.Parallel()
	for used, want := range map[int64]bool{0: false, 42_000: false, 59_999: false, 60_000: true, 150_000: true} {
		if got := worthACard(used); got != want {
			t.Errorf("worthACard(%d) = %v, want %v", used, got, want)
		}
	}
}

func TestPastLimits(t *testing.T) {
	t.Parallel()
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

// keepaliveWorld is a daemon whose lead has a cache worth saving and whose
// timers, keepalives and agents a test drives by hand.
type keepaliveWorld struct {
	d       testDaemon
	lead    state.Agent
	timers  []*fakeTimer
	used    int64
	working bool
	sent    int
	err     error
	during  func()
}

func newKeepaliveWorld(t *testing.T, hour bool) *keepaliveWorld {
	t.Helper()
	w := &keepaliveWorld{
		d:    startTestDaemon(t, t.TempDir(), fakeIncus),
		lead: state.Agent{Project: "p", Name: state.LeadName, AI: "claude", Role: state.RoleLead},
		used: 150_000,
	}
	if hour {
		// A subscription within its limits: an hour's cache.
		if err := w.d.srv.manager(nil).Creds.SaveClaudeToken("default", "sk-ant-oat01-test"); err != nil {
			t.Fatal(err)
		}
	}
	w.d.srv.idleAfter = func(delay time.Duration, f func()) interface{ Stop() bool } {
		timer := &fakeTimer{delay: delay, fire: f}
		w.timers = append(w.timers, timer)
		return timer
	}
	w.d.srv.cacheGate = func(string) (state.Agent, int64, bool) { return w.lead, w.used, true }
	w.d.srv.projectWorking = func(string) bool { return w.working }
	w.d.srv.leadKeepalive = func(_ context.Context, a state.Agent) error {
		if a.Ref() != w.lead.Ref() {
			t.Errorf("the keepalive went to %s", a.Ref())
		}
		w.sent++
		if w.during != nil {
			w.during()
		}
		return w.err
	}
	return w
}

// fire fires the timer set last, as the clock reaching it would.
func (w *keepaliveWorld) fire() { w.timers[len(w.timers)-1].fire() }

func (w *keepaliveWorld) card() bool { return w.d.srv.leadCacheState("p").Due }

func (w *keepaliveWorld) count() int {
	w.d.srv.mu.Lock()
	defer w.d.srv.mu.Unlock()
	return w.d.srv.leadCaches["p"].keepalives
}

// While an agent works, an hour's cache that comes due gets a keepalive
// rather than the card, and its timer is set again for another hour.
func TestAWorkingAgentKeepsTheLeadsCacheWarm(t *testing.T) {
	w := newKeepaliveWorld(t, true)
	w.working = true
	w.d.srv.leadCacheIdle(w.lead)
	w.fire()
	if w.sent != 1 || w.card() {
		t.Fatalf("an agent working: %d keepalives, card %v; want 1 and no card", w.sent, w.card())
	}
	if len(w.timers) != 2 || w.timers[1].delay != 55*time.Minute {
		t.Fatalf("after a keepalive the timers are %+v, want a second at 55m", w.timers)
	}
	if w.count() != 1 {
		t.Errorf("the count is %d after one keepalive", w.count())
	}
	if got := w.d.srv.leadCacheState("p"); got.TTLSeconds != 3600 {
		t.Errorf("after a keepalive the cache is %+v", got)
	}
}

// With nobody working the lead waits on the user: the card, as before.
func TestAnIdleProjectGetsTheCardNotAKeepalive(t *testing.T) {
	w := newKeepaliveWorld(t, true)
	w.d.srv.leadCacheIdle(w.lead)
	w.fire()
	if w.sent != 0 || !w.card() {
		t.Errorf("nobody working: %d keepalives, card %v; want none and the card", w.sent, w.card())
	}
}

// On a five-minute cache a ping every four and a half minutes costs more
// than the cold re-send it would save: the card.
func TestAFiveMinuteCacheGetsTheCardNotAKeepalive(t *testing.T) {
	w := newKeepaliveWorld(t, false)
	w.working = true
	w.d.srv.leadCacheIdle(w.lead)
	if w.timers[0].delay != 4*time.Minute+30*time.Second {
		t.Fatalf("the timer is set for %s, want a five-minute cache's", w.timers[0].delay)
	}
	w.fire()
	if w.sent != 0 || !w.card() {
		t.Errorf("five minutes: %d keepalives, card %v; want none and the card", w.sent, w.card())
	}
}

// A context below the card's floor is cheap to re-send: neither.
func TestASmallContextGetsNoKeepalive(t *testing.T) {
	w := newKeepaliveWorld(t, true)
	w.working, w.used = true, cacheCardFloor-1
	w.d.srv.leadCacheIdle(w.lead)
	w.fire()
	if w.sent != 0 || w.card() || len(w.timers) != 1 {
		t.Errorf("below the floor: %d keepalives, card %v, %d timers", w.sent, w.card(), len(w.timers))
	}
}

// Four keepalives in a row, then the card; a turn that really runs starts
// the count again, and a keepalive's own doesn't.
func TestKeepalivesStopAtTheCapUntilATurnRuns(t *testing.T) {
	w := newKeepaliveWorld(t, true)
	w.working = true
	w.d.srv.leadCacheIdle(w.lead)
	w.fire()
	w.fire()
	if w.sent != 2 {
		t.Fatalf("%d keepalives, want 2", w.sent)
	}
	// The user writes, or an agent's notice wakes the lead: the turn ends.
	w.d.srv.leadCacheIdle(w.lead)
	if w.count() != 0 {
		t.Fatalf("a turn left the count at %d", w.count())
	}
	for range maxKeepalives {
		w.fire()
	}
	if w.sent != 2+maxKeepalives || w.card() {
		t.Fatalf("after the reset: %d keepalives, card %v; want %d and no card", w.sent, w.card(), 2+maxKeepalives)
	}
	w.fire()
	if w.sent != 2+maxKeepalives || !w.card() {
		t.Errorf("past the cap: %d keepalives, card %v; want no more and the card", w.sent, w.card())
	}
}

// A turn that ends while a keepalive is in flight has set the timer itself:
// the keepalive doesn't set it again over it, nor count against the new one.
func TestATurnDuringAKeepaliveWins(t *testing.T) {
	w := newKeepaliveWorld(t, true)
	w.working = true
	w.d.srv.leadCacheIdle(w.lead)
	w.during = func() { w.d.srv.leadCacheIdle(w.lead) }
	w.fire()
	if len(w.timers) != 2 || w.count() != 0 {
		t.Errorf("%d timers, count %d; want the turn's timer only, counting from nought", len(w.timers), w.count())
	}
}

// A keepalive that fails puts the card up instead, so the cache isn't lost
// without the user hearing of it.
func TestAFailedKeepaliveShowsTheCard(t *testing.T) {
	w := newKeepaliveWorld(t, true)
	w.working, w.err = true, errors.New("the session stopped")
	w.d.srv.leadCacheIdle(w.lead)
	w.fire()
	if w.sent != 1 || !w.card() || len(w.timers) != 1 {
		t.Errorf("a failed keepalive: %d sent, card %v, %d timers", w.sent, w.card(), len(w.timers))
	}
}
