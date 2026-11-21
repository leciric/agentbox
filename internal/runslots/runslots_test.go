package runslots

import (
	"testing"
	"time"
)

const gib = int64(1) << 30

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func TestSlotsAreCountedFromTheBudgetLessTheCodePhase(t *testing.T) {
	p := New()
	// 13 GiB for the agents, 3 of them used by agents coding: 10 left,
	// five runs of the default 2 GiB.
	p.Measure(Budget{Pool: 13 * gib, CodeUse: 3 * gib})
	if c := p.Counts([]string{"a"}); c.Free != 5 || c.Total != 5 || c.InUse != 0 {
		t.Fatalf("counts = %+v, want 5 of 5 free", c)
	}
	if _, ok := p.Up("a/agent-1", "a", t0); !ok {
		t.Fatal("a free slot wasn't granted")
	}
	if c := p.Counts([]string{"a"}); c.InUse != 1 || c.Free != 4 || c.Total != 5 {
		t.Fatalf("counts = %+v, want 4 of 5 free", c)
	}
	// The holder's machine grows past its reservation: what it uses is
	// what it holds.
	p.Observe("a/agent-1", 5*gib)
	if c := p.Counts([]string{"a"}); c.Free != 2 {
		t.Fatalf("with the holder at 5 GiB, free = %d, want 2", c.Free)
	}
	// More coding takes from the runs.
	p.Measure(Budget{Pool: 13 * gib, CodeUse: 7 * gib})
	if c := p.Counts([]string{"a"}); c.Free != 0 || c.Total != 1 {
		t.Fatalf("counts = %+v, want 0 of 1 free", c)
	}
}

func TestEachProjectIsCountedInItsOwnPeak(t *testing.T) {
	p := New()
	p.SetPeak("big", Peak{Bytes: 6 * gib, Runs: 3})
	p.SetPeak("small", Peak{Bytes: gib, Runs: 3})
	p.Measure(Budget{Pool: 8 * gib})
	if n := p.FreeFor("big"); n != 1 {
		t.Fatalf("big fits %d, want 1", n)
	}
	if n := p.FreeFor("small"); n != 8 {
		t.Fatalf("small fits %d, want 8", n)
	}
	// Shown in the largest peak among the projects, so a slot shown free
	// fits any of them.
	if c := p.Counts([]string{"big", "small"}); c.Free != 1 || c.Size != 6*gib {
		t.Fatalf("counts = %+v, want 1 free of 6 GiB", c)
	}
	p.Up("big/agent-1", "big", t0)
	if _, ok := p.Up("big/agent-2", "big", t0); ok {
		t.Fatal("a second big run was granted with 2 GiB left")
	}
	// Someone queued is ahead of everyone after, even one that would fit.
	if st, ok := p.Up("small/agent-1", "small", t0); ok || st.State != Waiting || st.Ahead != 1 {
		t.Fatalf("small after a queued big: %+v granted=%v, want waiting with 1 ahead", st, ok)
	}
}

func TestOneRunAlwaysFits(t *testing.T) {
	p := New()
	p.SetPeak("huge", Peak{Bytes: 20 * gib})
	p.Measure(Budget{Pool: 4 * gib, CodeUse: 3 * gib})
	if c := p.Counts([]string{"huge"}); c.Free != 1 {
		t.Fatalf("with nothing held, free = %d, want 1", c.Free)
	}
	if _, ok := p.Up("huge/agent-1", "huge", t0); !ok {
		t.Fatal("with no slot held, a run that doesn't fit wasn't granted: nobody could ever run")
	}
	if _, ok := p.Up("huge/agent-2", "huge", t0); ok {
		t.Fatal("a second run was granted with no memory for it")
	}
}

func TestTheQueueIsFirstComeFirstServed(t *testing.T) {
	p := New()
	p.Measure(Budget{Pool: 4 * gib}) // two of 2 GiB
	p.Up("p/agent-1", "p", t0)
	p.Up("p/agent-2", "p", t0)
	for i, a := range []string{"p/agent-3", "p/agent-4", "p/agent-5"} {
		st, ok := p.Up(a, "p", t0.Add(time.Duration(i)*time.Second))
		if ok || st.State != Waiting || st.Ahead != i {
			t.Fatalf("%s: %+v granted=%v, want waiting with %d ahead", a, st, ok, i)
		}
	}
	// Asking again keeps the place.
	if st, _ := p.Up("p/agent-4", "p", t0.Add(5*time.Second)); st.Ahead != 1 {
		t.Fatalf("asking again moved agent-4 to %d ahead", st.Ahead)
	}
	// Try never queues, and says how many are.
	if st, ok := p.Try("p/agent-6", "p", t0); ok || st.State != "" || st.Ahead != 3 {
		t.Fatalf("try with a queue: %+v granted=%v", st, ok)
	}
	// A slot freed goes to the front of the queue, and only to it.
	if p.Down("p/agent-1") == nil {
		t.Fatal("agent-1 held no lease to give back")
	}
	_, granted, _ := p.Step(t0.Add(10 * time.Second))
	if len(granted) != 1 || granted[0].Agent != "p/agent-3" {
		t.Fatalf("granted %v, want agent-3", granted)
	}
	if st := p.Status("p/agent-5"); st.State != Waiting || st.Ahead != 1 {
		t.Fatalf("agent-5 is %+v, want 1 ahead", st)
	}
	// Leaving the queue moves those behind up.
	p.Down("p/agent-4")
	if st := p.Status("p/agent-5"); st.Ahead != 0 {
		t.Fatalf("agent-5 has %d ahead after agent-4 left", st.Ahead)
	}
}

func TestLeasesExpireWithoutAHeartbeat(t *testing.T) {
	p := New()
	p.Measure(Budget{Pool: 2 * gib})
	p.Up("p/agent-1", "p", t0)
	p.Up("p/agent-2", "p", t0)
	// Heartbeats keep it.
	for m := 5; m <= 30; m += 5 {
		now := t0.Add(time.Duration(m) * time.Minute)
		if !p.Beat("p/agent-1", now) {
			t.Fatalf("at %d minutes agent-1 held nothing", m)
		}
		// agent-2 keeps asking, which keeps its place.
		p.Up("p/agent-2", "p", now)
		if expired, _, _ := p.Step(now); len(expired) != 0 {
			t.Fatalf("at %d minutes %v expired while beating", m, expired)
		}
	}
	// Ten minutes without one, it goes, and the next in line has it.
	last := t0.Add(30 * time.Minute)
	p.Up("p/agent-2", "p", last.Add(LeaseTTL-time.Second))
	expired, granted, _ := p.Step(last.Add(LeaseTTL))
	if len(expired) != 1 || expired[0].Agent != "p/agent-1" {
		t.Fatalf("expired %v, want agent-1", expired)
	}
	if len(granted) != 1 || granted[0].Agent != "p/agent-2" {
		t.Fatalf("granted %v, want agent-2", granted)
	}
}

func TestAPlaceNobodyAsksForIsDropped(t *testing.T) {
	p := New()
	p.Measure(Budget{Pool: 2 * gib})
	p.Up("p/agent-1", "p", t0)
	p.Up("p/agent-2", "p", t0)
	p.Up("p/agent-3", "p", t0)
	p.Up("p/agent-3", "p", t0.Add(50*time.Second))
	_, _, dropped := p.Step(t0.Add(WaitTTL))
	if len(dropped) != 1 || dropped[0].Agent != "p/agent-2" {
		t.Fatalf("dropped %v, want agent-2, which stopped asking", dropped)
	}
	if st := p.Status("p/agent-3"); st.Ahead != 0 {
		t.Fatalf("agent-3 has %d ahead", st.Ahead)
	}
}

func TestPeaksAreLearned(t *testing.T) {
	p := New()
	if got := p.PeakOf("p"); got != DefaultPeak {
		t.Fatalf("unlearned peak %d, want the default", got)
	}
	if pk := p.Learn("p", 4*gib, t0); pk.Bytes != 4*gib || pk.Runs != 1 {
		t.Fatalf("first run: %+v", pk)
	}
	// A smaller run lowers it a quarter of the way.
	if pk := p.Learn("p", 2*gib, t0); pk.Bytes != 4*gib-gib/2 || pk.Runs != 2 {
		t.Fatalf("smaller run: %+v, want 3.5 GiB", pk)
	}
	// A larger one raises it at once.
	if pk := p.Learn("p", 5*gib, t0); pk.Bytes != 5*gib {
		t.Fatalf("larger run: %+v", pk)
	}
	// Never under the floor, and nothing measured changes nothing.
	if pk := p.Learn("q", 10<<20, t0); pk.Bytes != MinPeak {
		t.Fatalf("tiny run: %+v", pk)
	}
	if pk := p.Learn("q", 0, t0); pk.Runs != 1 {
		t.Fatalf("nothing measured counted a run: %+v", pk)
	}
	// The peak is what a new lease reserves.
	p.Measure(Budget{Pool: 100 * gib})
	p.Up("p/agent-1", "p", t0)
	if st := p.Status("p/agent-1"); st.Lease.Reserved != 5*gib {
		t.Fatalf("reserved %d, want the learned 5 GiB", st.Lease.Reserved)
	}
	p.Observe("p/agent-1", 3*gib)
	p.Observe("p/agent-1", 6*gib)
	p.Observe("p/agent-1", 2*gib)
	if l := p.Down("p/agent-1"); l.Peak != 6*gib || l.Use != 2*gib {
		t.Fatalf("lease peak %d use %d, want 6 GiB and 2 GiB", l.Peak, l.Use)
	}
}

func TestMarkAndDownForget(t *testing.T) {
	p := New()
	p.Measure(Budget{Pool: 8 * gib})
	if p.Mark("p/agent-1", func(l *Lease) { l.Browser = true }) {
		t.Fatal("marked a lease nobody holds")
	}
	p.Up("p/agent-1", "p", t0)
	p.Mark("p/agent-1", func(l *Lease) { l.Browser, l.Containers, l.ContainersKnown = true, []string{"c1"}, true })
	l := p.Down("p/agent-1")
	if l == nil || !l.Browser || len(l.Containers) != 1 {
		t.Fatalf("down gave %+v", l)
	}
	if p.Holds("p/agent-1") || p.Down("p/agent-1") != nil {
		t.Fatal("a lease given back is still held")
	}
}
