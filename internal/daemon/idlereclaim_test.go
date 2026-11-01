package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeIdleCgroups records what idle reclaim did to each machine.
type fakeIdleCgroups struct {
	mu                       sync.Mutex
	froze, thawed, reclaimed []string
	stopped                  []agent.IdleProcess
}

func (f *fakeIdleCgroups) Freeze(instance string) (*agent.IdleFreeze, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.froze = append(f.froze, instance)
	return &agent.IdleFreeze{Frozen: []agent.IdleProcess{{PID: 7, Name: "node", RSS: 1 << 30}}, Stopped: f.stopped}, nil
}

func (f *fakeIdleCgroups) Thaw(instance string, fr *agent.IdleFreeze) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	what := instance
	if fr == nil {
		what += " (unknown)"
	}
	f.thawed = append(f.thawed, what)
	return nil
}

func (f *fakeIdleCgroups) Reclaim(instance string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reclaimed = append(f.reclaimed, instance)
	return 2 << 30, 1 << 30, nil
}

func (f *fakeIdleCgroups) counts() (froze, thawed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.froze), len(f.thawed)
}

func newIdleReclaimTest(t *testing.T, on bool) (testDaemon, state.Agent, *fakeIdleCgroups) {
	t.Helper()
	d, a := newAutoStopIdleTest(t, "Running", time.Now().Add(-time.Hour))
	cg := &fakeIdleCgroups{}
	d.srv.idleCgroups = cg
	if err := d.srv.store.SetFlag(context.Background(), state.SettingIdleReclaim, on); err != nil {
		t.Fatal(err)
	}
	return d, a, cg
}

// TestIdleReclaimOffDoesNothing: off, as it is until somebody turns it on,
// nothing is frozen or stopped however long an agent idles, and the one pass
// that wakes what an earlier daemon froze finds nothing it knows of.
func TestIdleReclaimOffDoesNothing(t *testing.T) {
	t.Parallel()
	d, a, cg := newIdleReclaimTest(t, false)
	d.srv.setOutcome(a, api.AgentOutcomeDone, "")
	for range 3 {
		d.srv.stepIdleReclaim(context.Background(), time.Now().Add(24*time.Hour))
	}
	if froze, _ := cg.counts(); froze != 0 {
		t.Errorf("froze %v with the setting off", cg.froze)
	}
	if got := instanceStatus(t, d); got != "Running" {
		t.Errorf("instance = %q, want still Running", got)
	}
	if len(cg.thawed) != 1 || cg.thawed[0] != a.Instance+" (unknown)" {
		t.Errorf("thawed %v, want one look for what an earlier daemon froze", cg.thawed)
	}
}

// TestIdleReclaimFreezesAnIdleAgentAndAMessageWakesIt is the freeze half:
// not before the wait, once after it, shown on the agent, and woken by the
// first touch.
func TestIdleReclaimFreezesAnIdleAgentAndAMessageWakesIt(t *testing.T) {
	t.Parallel()
	d, a, cg := newIdleReclaimTest(t, true)
	ctx := context.Background()
	created := time.Now().Add(-time.Hour)

	d.srv.stepIdleReclaim(ctx, created.Add(4*time.Minute))
	if froze, _ := cg.counts(); froze != 0 {
		t.Fatalf("froze after 4 minutes idle: %v", cg.froze)
	}
	d.srv.stepIdleReclaim(ctx, created.Add(6*time.Minute))
	d.srv.stepIdleReclaim(ctx, created.Add(7*time.Minute))
	if froze, _ := cg.counts(); froze != 1 {
		t.Fatalf("froze %v, want once after 5 minutes idle", cg.froze)
	}
	if got := d.srv.reclaimedOf(a.Ref(), "running"); got != api.ReclaimedFrozen {
		t.Errorf("reclaimed = %q, want frozen", got)
	}
	// Not finished (no outcome): its machine keeps running however long.
	d.srv.stepIdleReclaim(ctx, created.Add(5*time.Hour))
	if got := instanceStatus(t, d); got != "Running" {
		t.Errorf("an unfinished agent's machine is %q", got)
	}

	d.srv.touchIdle(a.Ref())
	if _, thawed := cg.counts(); thawed != 2 { // the first step's look, then this
		t.Errorf("thawed %v after a touch", cg.thawed)
	}
	if got := d.srv.reclaimedOf(a.Ref(), "running"); got != "" {
		t.Errorf("reclaimed = %q after waking", got)
	}
	// Touched now: its idle time starts again.
	d.srv.stepIdleReclaim(ctx, time.Now().Add(time.Minute))
	if froze, _ := cg.counts(); froze != 1 {
		t.Errorf("froze again a minute after a touch: %v", cg.froze)
	}
}

// TestIdleReclaimLeavesWhatIsInUse: an open desktop or terminal, or a heavy
// run the governor holds, keeps an agent from freezing, and one frozen wakes.
func TestIdleReclaimLeavesWhatIsInUse(t *testing.T) {
	t.Parallel()
	d, a, cg := newIdleReclaimTest(t, true)
	ctx := context.Background()
	later := time.Now().Add(time.Hour)

	closeView := d.srv.viewIdle(a.Ref())
	d.srv.stepIdleReclaim(ctx, later)
	if froze, _ := cg.counts(); froze != 0 {
		t.Errorf("froze with its desktop open: %v", cg.froze)
	}
	closeView()

	d.srv.heavy.mu.Lock()
	d.srv.heavy.g.Ask(a.Ref(), "k", "go test ./...", time.Hour, time.Now())
	d.srv.heavy.mu.Unlock()
	d.srv.stepIdleReclaim(ctx, later)
	if froze, _ := cg.counts(); froze != 0 {
		t.Errorf("froze with a heavy run going: %v", cg.froze)
	}
	d.srv.heavy.mu.Lock()
	d.srv.heavy.g.End(a.Ref(), "k")
	d.srv.heavy.mu.Unlock()

	d.srv.stepIdleReclaim(ctx, later)
	if froze, _ := cg.counts(); froze != 1 {
		t.Fatalf("froze %v once nothing held it", cg.froze)
	}
	closeView = d.srv.viewIdle(a.Ref())
	defer closeView()
	if got := d.srv.reclaimedOf(a.Ref(), "running"); got != "" {
		t.Errorf("still %q with its terminal open", got)
	}
}

// TestIdleReclaimStopsAFinishedAgent is the stop half: a finished agent idle
// past the second wait has its machine stopped, shown on it, without the
// retired mark that would keep automatic messages from starting it again.
func TestIdleReclaimStopsAFinishedAgent(t *testing.T) {
	t.Parallel()
	d, a, cg := newIdleReclaimTest(t, true)
	ctx := context.Background()
	created := time.Now().Add(-time.Hour)
	d.srv.setOutcome(a, api.AgentOutcomeDone, "")

	d.srv.stepIdleReclaim(ctx, created.Add(6*time.Minute))
	if got := instanceStatus(t, d); got != "Running" {
		t.Fatalf("stopped after 6 minutes: %q", got)
	}
	if froze, _ := cg.counts(); froze != 1 {
		t.Errorf("froze %v before stopping", cg.froze)
	}
	d.srv.stepIdleReclaim(ctx, created.Add(11*time.Minute))
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Fatalf("instance = %q after 11 minutes finished, want Stopped", got)
	}
	if _, thawed := cg.counts(); thawed < 2 {
		t.Errorf("stopped without waking what it froze first: %v", cg.thawed)
	}
	if got := d.srv.reclaimedOf(a.Ref(), "stopped"); got != api.ReclaimedStopped {
		t.Errorf("reclaimed = %q, want stopped", got)
	}
	d.srv.mu.Lock()
	retired := d.srv.retired[a.Ref()]
	d.srv.mu.Unlock()
	if retired {
		t.Error("marked retired: automatic messages wouldn't start it again")
	}
	// Turned off, nothing it did is shown any more.
	if err := d.srv.store.SetFlag(ctx, state.SettingIdleReclaim, false); err != nil {
		t.Fatal(err)
	}
	d.srv.stepIdleReclaim(ctx, time.Now())
	if got := d.srv.reclaimedOf(a.Ref(), "stopped"); got != "" {
		t.Errorf("reclaimed = %q after turning it off", got)
	}
}

// TestIdleReclaimSaysWhatItStopped: what couldn't be frozen and was stopped
// is said in the agent's chat, without starting a turn.
func TestIdleReclaimSaysWhatItStopped(t *testing.T) {
	t.Parallel()
	d, a, cg := newIdleReclaimTest(t, true)
	cg.stopped = []agent.IdleProcess{{PID: 9, Name: "node", Command: "node server.js", RSS: 700 << 20}}
	// An agent with a chat to say it in, which the notice doesn't start.
	if err := d.srv.store.SetAgentAI(context.Background(), a.Project, a.Name, "claude"); err != nil {
		t.Fatal(err)
	}
	d.srv.stepIdleReclaim(context.Background(), time.Now().Add(time.Hour))
	want := "`node server.js` (" + agent.HumanBytes(700<<20) + ")"
	deadline := time.Now().Add(5 * time.Second)
	for {
		items, err := d.srv.store.ChatItems(context.Background(), a.Project, a.Name)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if s := string(it.Data); strings.Contains(s, want) && strings.Contains(s, "Start it again") {
				if d.srv.chatWorking(a.Ref()) {
					t.Error("telling it started a turn")
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the chat wasn't told; it has %d items", len(items))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestIdleReclaimSettings: off with the defaults until changed, its waits
// kept to a minute to a day.
func TestIdleReclaimSettings(t *testing.T) {
	t.Parallel()
	d, _, _ := newIdleReclaimTest(t, false)
	ctx := context.Background()
	got, err := d.client.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.IdleReclaim || got.IdleFreezeSeconds != 300 || got.IdleStopSeconds != 600 {
		t.Errorf("fresh: on %v, waits %d and %d; want off, 300 and 600", got.IdleReclaim, got.IdleFreezeSeconds, got.IdleStopSeconds)
	}
	on, freeze, stop := true, 120, 3600
	if got, err = d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{IdleReclaim: &on, IdleFreezeSeconds: &freeze, IdleStopSeconds: &stop}); err != nil {
		t.Fatal(err)
	}
	if !got.IdleReclaim || got.IdleFreezeSeconds != 120 || got.IdleStopSeconds != 3600 {
		t.Errorf("after setting: %v, %d, %d", got.IdleReclaim, got.IdleFreezeSeconds, got.IdleStopSeconds)
	}
	short := 30
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{IdleFreezeSeconds: &short}); err == nil {
		t.Error("took a 30-second wait")
	}
}
