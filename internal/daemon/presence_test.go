package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// A lease lasts its TTL unrenewed, each renewal replacing what it views, and
// the daemon backs off for what no live lease views.
func TestPresenceLeasesLapse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	p := newViewLeases()
	p.now = func() time.Time { return now }

	if !p.away() || !p.idle("hello") {
		t.Fatal("with no lease, nothing is viewed")
	}
	came := p.renew(api.PresenceRequest{Client: "desktop-1", Views: []api.PresenceView{{Project: "hello", Agent: "hello/agent-01", Resources: true}}})
	if fmt.Sprint(came) != "[hello]" {
		t.Fatalf("came into view = %v, want [hello]", came)
	}
	if p.away() || p.idle("hello") || !p.idle("other") {
		t.Fatal("hello is viewed, other isn't")
	}
	if got := p.sampling(); fmt.Sprint(got) != "[hello/agent-01]" {
		t.Fatalf("sampling = %v", got)
	}

	// A second client viewing the same project brings nothing new into view.
	if came := p.renew(api.PresenceRequest{Client: "phone", Views: []api.PresenceView{{Project: "hello"}}}); len(came) != 0 {
		t.Fatalf("came into view again: %v", came)
	}

	// Renewed just before it lapses, the desktop's lease goes on; the phone's,
	// not renewed, lapses.
	now = now.Add(presenceTTL - time.Second)
	p.renew(api.PresenceRequest{Client: "desktop-1", Views: []api.PresenceView{{Project: "hello", Agent: "hello/agent-01", Resources: true}}})
	now = now.Add(2 * time.Second)
	if st := p.state(); len(st.Leases) != 1 || st.Leases[0].Client != "desktop-1" {
		t.Fatalf("leases = %+v, want only the renewed one", st.Leases)
	}

	// A renewal replaces the views: the resources panel closed.
	p.renew(api.PresenceRequest{Client: "desktop-1", Views: []api.PresenceView{{Project: "hello", Agent: "hello/agent-01"}}})
	if got := p.sampling(); len(got) != 0 {
		t.Fatalf("sampling after the panel closed = %v", got)
	}

	// Lapsed: nothing viewed, nothing sampled.
	now = now.Add(presenceTTL)
	if !p.away() || !p.idle("hello") || len(p.state().Leases) != 0 {
		t.Fatal("a lapsed lease still counts")
	}
	if came := p.renew(api.PresenceRequest{Client: "desktop-1", Views: []api.PresenceView{{Project: "hello"}}}); fmt.Sprint(came) != "[hello]" {
		t.Fatalf("after a lapse, came into view = %v", came)
	}

	// Ended cleanly: gone at once.
	p.end("desktop-1")
	if !p.away() {
		t.Fatal("an ended lease still counts")
	}
}

// On battery, or hot, everything backs off, even what is viewed.
func TestPresencePowerSaving(t *testing.T) {
	t.Parallel()
	p := newViewLeases()
	view := []api.PresenceView{{Project: "hello"}}
	p.renew(api.PresenceRequest{Client: "phone", Views: view})
	p.renew(api.PresenceRequest{Client: "desktop", Views: view, Power: &api.HostPower{Thermal: "fair"}})
	if p.idle("hello") {
		t.Fatal("on mains at a fair temperature, a viewed project backed off")
	}
	p.renew(api.PresenceRequest{Client: "desktop", Views: view, Power: &api.HostPower{Thermal: "serious"}})
	if !p.idle("hello") || !p.away() {
		t.Fatal("running hot, a viewed project didn't back off")
	}
	p.renew(api.PresenceRequest{Client: "desktop", Views: view, Power: &api.HostPower{OnBattery: true}})
	if pw := p.power(); !pw.OnBattery || !p.idle("hello") {
		t.Fatalf("on battery: power %+v, idle %v", pw, p.idle("hello"))
	}
}

// The HTTP side: a lease is taken and given up, a bad one refused, and an
// agent's history is served.
func TestPresenceEndpoints(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.RenewPresence(ctx, api.PresenceRequest{}); err == nil || !strings.Contains(err.Error(), "client name") {
		t.Fatalf("a lease with no client: %v", err)
	}
	if _, err := d.client.RenewPresence(ctx, api.PresenceRequest{Client: "x", Views: []api.PresenceView{{}}}); err == nil {
		t.Fatal("a view with no project was taken")
	}
	resp, err := d.client.RenewPresence(ctx, api.PresenceRequest{Client: "desktop-1", Kind: "desktop", Views: []api.PresenceView{{Project: "hello", Agent: "hello/agent-01", Resources: true}}})
	if err != nil || resp.TTLSeconds != 45 || resp.Idle {
		t.Fatalf("renew = %+v, %v", resp, err)
	}
	st, err := d.client.Presence(ctx)
	if err != nil || len(st.Leases) != 1 || st.Leases[0].Kind != "desktop" || fmt.Sprint(st.Sampling) != "[hello/agent-01]" {
		t.Fatalf("presence = %+v, %v", st, err)
	}
	if err := d.client.EndPresence(ctx, "desktop-1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.client.Presence(ctx); len(st.Leases) != 0 || !st.Idle {
		t.Fatalf("after ending, presence = %+v", st)
	}
}

// The sampler reads only the agents it is given, makes a point from the
// second reading on, starts again after a gap, and forgets what is older
// than its window.
func TestAgentSamplerKeepsAWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cpu := map[string]time.Duration{}
	reads := 0
	a := newAgentSampler()
	a.window = time.Minute
	a.now = func() time.Time { return now }
	a.read = func(instance string) (agent.AgentCounters, bool) {
		reads++
		cpu[instance] += time.Second // a full core over each 2-second tick: 50%
		return agent.AgentCounters{CPU: cpu[instance], Memory: 100}, true
	}
	watched := map[string]string{"hello/agent-01": "hello-agent-01"}

	if got := a.sample(map[string]string{}); len(got) != 0 || reads != 0 {
		t.Fatal("nothing watched, yet something was read")
	}
	if got := a.sample(watched); len(got) != 0 {
		t.Fatalf("a first reading made a point: %v", got)
	}
	for range 40 {
		now = now.Add(a.every)
		a.sample(watched)
	}
	h := a.history("hello/agent-01")
	if len(h) != 30 { // one minute of 2-second points
		t.Fatalf("kept %d points, want 30", len(h))
	}
	if h[0].CPU != 50 || h[0].Memory != 100 || !h[0].At.Before(h[1].At) {
		t.Fatalf("points = %+v", h[:2])
	}

	// Unwatched for a while, then watched again: the first reading back only
	// starts the series again, rather than averaging over the gap.
	now = now.Add(10 * time.Second)
	if got := a.sample(watched); len(got) != 0 {
		t.Fatalf("a reading after a gap made a point: %v", got)
	}
	now = now.Add(a.every)
	if got := a.sample(watched); got["hello/agent-01"].CPU != 50 {
		t.Fatalf("after the gap = %v", got)
	}

	// Nobody watches for longer than the window: forgotten.
	now = now.Add(2 * time.Minute)
	a.sample(map[string]string{})
	if h := a.history("hello/agent-01"); len(h) != 0 {
		t.Fatalf("kept %d points of an agent nobody watched", len(h))
	}
}

func TestBaseSyncBacksOffWhileIdle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if !baseSyncDue(now.Add(-time.Minute), now, false) {
		t.Fatal("a viewed project wasn't due on its tick")
	}
	if baseSyncDue(now.Add(-10*time.Minute), now, true) {
		t.Fatal("an unviewed project synced again after 10 minutes")
	}
	if !baseSyncDue(now.Add(-28*time.Minute), now, true) || !baseSyncDue(time.Time{}, now, true) {
		t.Fatal("an unviewed project wasn't due near its idle interval, or never synced")
	}
}

// Nobody looking, the watch backs off to its idle interval, yet still tells
// the agent when its checks fail; a project coming into view is looked at
// soon after, and from then on at its usual pace.
func TestPRWatchBacksOffWhileNobodyLooks(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	head := commitOn(t, agents["agent-01"], "reminders.txt")
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d.srv.prWatch.now = func() time.Time { return now }
	d.srv.presence.now = func() time.Time { return now }
	wait := func() time.Duration {
		d.srv.prWatch.mu.Lock()
		defer d.srv.prWatch.mu.Unlock()
		return d.srv.prWatch.repos["hello-stack"].next.Sub(now)
	}
	look := func() {
		now = now.Add(wait())
		d.srv.pollPRWatch(context.Background())
	}
	viewing := func(power *api.HostPower) {
		for _, project := range d.srv.presence.renew(api.PresenceRequest{Client: "desktop", Views: []api.PresenceView{{Project: "hello-stack"}}, Power: power}) {
			d.srv.prWatch.wake(project)
		}
	}

	gh.set(9, head, "OPEN", "MERGEABLE", "PENDING", "")
	d.srv.pollPRWatch(context.Background())
	if got := wait(); got != prWatchIdle {
		t.Fatalf("nobody looking, checks running: next look in %v, want the idle %v", got, prWatchIdle)
	}
	gh.set(9, head, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	look()
	if said, _ := rec.take(); len(said) != 1 || !strings.Contains(said[0], "`test`") {
		t.Fatalf("backed off, failing checks told the agent %q, want them told", said)
	}

	// The project comes into view: looked at again prWatchFast after the
	// last look, not prWatchIdle.
	gh.set(9, head, "OPEN", "MERGEABLE", "PENDING", "")
	now = now.Add(5 * time.Second)
	viewing(nil)
	if got := wait(); got != prWatchFast-5*time.Second {
		t.Fatalf("came into view: next look in %v, want %v", got, prWatchFast-5*time.Second)
	}
	look()
	if got := wait(); got != prWatchFast {
		t.Fatalf("viewed, checks running: next look in %v, want %v", got, prWatchFast)
	}

	// On battery, viewed or not, it backs off again; each look below renews
	// the lease first, as the app would have every 15 seconds.
	now = now.Add(wait())
	viewing(&api.HostPower{OnBattery: true})
	d.srv.pollPRWatch(context.Background())
	if got := wait(); got != prWatchIdle {
		t.Fatalf("on battery: next look in %v, want %v", got, prWatchIdle)
	}
	now = now.Add(wait())
	viewing(nil)
	d.srv.pollPRWatch(context.Background())
	if got := wait(); got != prWatchFast {
		t.Fatalf("back on mains: next look in %v, want %v", got, prWatchFast)
	}

	// The lease lapses unrenewed: idle again.
	now = now.Add(presenceTTL)
	d.srv.pollPRWatch(context.Background())
	if got := wait(); got != prWatchIdle {
		t.Fatalf("lease lapsed: next look in %v, want %v", got, prWatchIdle)
	}
}

// An agent a lease asks resources of is sampled, each point published and
// kept; one no lease asks for isn't read at all.
func TestAgentResourcesSampledWhileWatched(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	pullsProject(t, d, "agent-01")
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	reads := 0
	var cpu time.Duration
	d.srv.agentSamples.now = func() time.Time { return now }
	d.srv.agentSamples.read = func(string) (agent.AgentCounters, bool) {
		reads++
		cpu += 500 * time.Millisecond
		return agent.AgentCounters{CPU: cpu, Memory: 2 << 30, DiskWrite: uint64(reads) * 4096}, true
	}
	events, unsubscribe := d.srv.events.subscribe()
	defer unsubscribe()

	d.srv.sampleWatched(ctx)
	if reads != 0 {
		t.Fatal("an agent nobody watches was read")
	}
	if _, err := d.client.RenewPresence(ctx, api.PresenceRequest{Client: "desktop", Views: []api.PresenceView{{Project: "hello-stack", Agent: "hello-stack/agent-01", Resources: true}}}); err != nil {
		t.Fatal(err)
	}
	d.srv.sampleWatched(ctx)
	now = now.Add(agentResourceEvery)
	d.srv.sampleWatched(ctx)
	if reads != 2 {
		t.Fatalf("read %d times, want 2", reads)
	}
	timeout := time.After(5 * time.Second)
	for seen := false; !seen; {
		select {
		case ev := <-events:
			if ev.Type != api.EventAgentResources {
				continue // the agent's own changes, and the like
			}
			if !strings.Contains(string(ev.Data), `"ref":"hello-stack/agent-01"`) {
				t.Fatalf("event = %s", ev.Data)
			}
			seen = true
		case <-timeout:
			t.Fatal("no event")
		}
	}
	h, err := d.client.AgentResources(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if !h.Sampling || h.IntervalSeconds != 2 || h.WindowSeconds != 600 || len(h.Points) != 1 || h.Points[0].CPU != 25 || h.Points[0].DiskWrite != 2048 {
		t.Fatalf("history = %+v", h)
	}
}
