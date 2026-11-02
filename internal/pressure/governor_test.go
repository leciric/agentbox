package pressure

import (
	"fmt"
	"testing"
	"time"
)

func TestParseReadsBothLines(t *testing.T) {
	psi, err := Parse("some avg10=1.36 avg60=0.52 avg300=0.22 total=46356041310\nfull avg10=12.50 avg60=0.50 avg300=0.21 total=40694621386\n")
	if err != nil {
		t.Fatal(err)
	}
	if psi.Some.Avg10 != 1.36 || psi.Full.Avg10 != 12.5 || psi.Full.Total != 40694621386 || psi.Some.Avg300 != 0.22 {
		t.Fatalf("parsed %+v", psi)
	}
	if _, err := Parse("nothing here"); err == nil {
		t.Fatal("no lines parsed without an error")
	}
	if _, err := Parse("some avg10=x"); err == nil {
		t.Fatal("a bad number parsed")
	}
}

func psi(some, full float64) Sample {
	return Sample{PSI: PSI{Some: Stall{Avg10: some}, Full: Stall{Avg10: full}}}
}

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func TestAWaiterStartsAtOnceWhenCalmAndNothingRuns(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	if _, ok := g.Ask("p/a", "k", "go test ./...", time.Minute, t0); ok {
		t.Fatal("started before a step")
	}
	got := g.Step(t0, psi(0, 0))
	if len(got.Start) != 1 || got.Start[0].Key != "k" {
		t.Fatalf("calm step started %v", got.Start)
	}
	if _, ok := g.Ask("p/a", "k", "", time.Minute, t0); !ok {
		t.Fatal("asking again for a started run says it waits")
	}
}

func TestWaitersStartInTheOrderTheyAskedOnceCalm(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	g.Ask("p/a", "first", "", time.Minute, t0)
	g.Step(t0, psi(50, 0)) // nothing runs: the first starts whatever the pressure
	for _, k := range []string{"b", "c", "d"} {
		g.Ask("p/"+k, k, "", time.Minute, t0)
	}
	now := t0
	for range 10 {
		now = now.Add(time.Second)
		if got := g.Step(now, psi(50, 0)); len(got.Start) != 0 {
			t.Fatalf("started %s under pressure with a run going", got.Start[0].Key)
		}
	}
	var order []string
	for range 20 {
		now = now.Add(time.Second)
		for _, r := range g.Step(now, psi(1, 0)).Start {
			order = append(order, r.Key)
		}
	}
	if fmt.Sprint(order) != "[b c d]" {
		t.Fatalf("started %v, want [b c d]", order)
	}
}

func TestAskingAgainKeepsAWaitersPlace(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	g.Ask("p/a", "running", "", time.Minute, t0)
	g.Step(t0, psi(0, 0))
	g.Ask("p/b", "b", "", 0, t0)
	g.Ask("p/c", "c", "", 0, t0)
	later := t0.Add(DefaultPolicy.Grace - time.Second)
	g.Ask("p/b", "b", "", 0, later)
	g.Expire(t0.Add(DefaultPolicy.Grace+time.Second), func(*Run) bool { return true })
	w := g.Waiting()
	if len(w) != 1 || w[0].Key != "b" {
		t.Fatalf("waiting %v, want only b, which asked again", keys(w))
	}
}

func keys(runs []*Run) []string {
	var out []string
	for _, r := range runs {
		out = append(out, r.Key)
	}
	return out
}

// started gets n runs going, one per agent, each placed.
func started(t *testing.T, n int) (*Governor, time.Time) {
	t.Helper()
	g := NewGovernor(DefaultPolicy)
	now := t0
	for i := range n {
		g.Ask(fmt.Sprintf("p/a%d", i), fmt.Sprint(i), "", time.Hour, now)
		for len(g.Waiting()) > 0 {
			now = now.Add(time.Second)
			for _, r := range g.Step(now, psi(0, 0)).Start {
				g.Place(r.Agent, r.Key)
			}
		}
	}
	return g, now
}

func TestPressureThatStaysHighPausesTheNewestButNeverTheOldest(t *testing.T) {
	g, now := started(t, 3)
	var paused []string
	for range 120 {
		now = now.Add(time.Second)
		got := g.Step(now, psi(90, 60))
		for _, r := range got.Pause {
			paused = append(paused, r.Key)
		}
		if len(got.Resume) > 0 {
			t.Fatalf("resumed %v under pressure", keys(got.Resume))
		}
	}
	if fmt.Sprint(paused) != "[2 1]" {
		t.Fatalf("paused %v, want the newest first and never the oldest: [2 1]", paused)
	}
	var resumed []string
	for range 120 {
		now = now.Add(time.Second)
		for _, r := range g.Step(now, psi(1, 0)).Resume {
			resumed = append(resumed, r.Key)
		}
	}
	if fmt.Sprint(resumed) != "[1 2]" {
		t.Fatalf("resumed %v, want the oldest first: [1 2]", resumed)
	}
}

func TestABriefSpikePausesNothing(t *testing.T) {
	g, now := started(t, 2)
	for range 3 {
		now = now.Add(time.Second)
		if got := g.Step(now, psi(90, 60)); len(got.Pause) > 0 {
			t.Fatalf("paused %v after a spike of a few seconds", keys(got.Pause))
		}
	}
}

func TestARunThatIsntPlacedIsNeverPaused(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	now := t0
	g.Ask("p/a", "old", "", time.Hour, now)
	g.Step(now, psi(0, 0))
	g.Place("p/a", "old")
	g.Ask("p/b", "unplaced", "", time.Hour, now)
	now = now.Add(5 * time.Second)
	g.Step(now, psi(0, 0))
	for range 60 {
		now = now.Add(time.Second)
		if got := g.Step(now, psi(90, 60)); len(got.Pause) > 0 {
			t.Fatalf("paused %v", keys(got.Pause))
		}
	}
}

func TestWhenTheOldestEndsThePausedNextOldestResumesAtOnce(t *testing.T) {
	g, now := started(t, 2)
	for range 30 {
		now = now.Add(time.Second)
		g.Step(now, psi(90, 60))
	}
	if r := g.Running(); !r[1].Paused {
		t.Fatal("the newer run wasn't paused")
	}
	g.End("p/a0", "0")
	now = now.Add(time.Second)
	got := g.Step(now, psi(90, 60))
	if len(got.Resume) != 1 || got.Resume[0].Key != "1" {
		t.Fatalf("resumed %v, want 1, now the oldest", keys(got.Resume))
	}
}

func TestNothingStartsWhileARunIsPaused(t *testing.T) {
	g, now := started(t, 2)
	for range 30 {
		now = now.Add(time.Second)
		g.Step(now, psi(90, 60))
	}
	g.Ask("p/new", "new", "", time.Hour, now)
	// Calm, but not for long enough to resume: the paused run goes first.
	for range 5 {
		now = now.Add(time.Second)
		if got := g.Step(now, psi(1, 0)); len(got.Start) > 0 {
			t.Fatal("a waiter started ahead of a paused run")
		}
	}
}

func TestDropTakesEveryRunOfAnAgent(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	g.Ask("p/a", "1", "", time.Hour, t0)
	g.Step(t0, psi(0, 0))
	g.Ask("p/a", "2", "", time.Hour, t0)
	g.Ask("p/b", "3", "", time.Hour, t0)
	if gone := g.Drop("p/a"); len(gone) != 2 {
		t.Fatalf("dropped %v", keys(gone))
	}
	if len(g.Running())+len(g.Waiting()) != 1 {
		t.Fatal("dropped another agent's run")
	}
	if g.End("p/x", "nothing") != nil {
		t.Fatal("ended a run that isn't there")
	}
}

// The simulation: n agents' test runs in a VM of capacity, against a fake
// memory source whose pressure comes from what the runs hold. A run holds
// its memory while it runs; paused, it holds pausedShare of it (0 with swap
// to push it to, 1 without), and gets nothing done. Overcommitted, the VM
// thrashes: runs crawl, and pressure is high.
type simRun struct {
	key        string
	memory     float64 // GiB
	work, done int     // seconds of work, and done so far
	askAt      int     // when it asks to start
	finished   bool
	killed     bool
}

func simulate(t *testing.T, capacity, pausedShare float64, runs []*simRun) (seconds, maxRunning int) {
	t.Helper()
	g := NewGovernor(DefaultPolicy)
	byKey := map[string]*simRun{}
	for _, r := range runs {
		byKey[r.key] = r
	}
	// The VM is at its cap, with a quarter of it again as swap that paused
	// runs are pushed out to.
	pressureOf := func() Sample {
		var used, swapped float64
		for _, r := range g.Running() {
			share := 1.0
			if r.Paused {
				share = pausedShare
				swapped += byKey[r.Key].memory * (1 - pausedShare)
			}
			used += byKey[r.Key].memory * share
		}
		var out Sample
		switch {
		case used > capacity:
			out = psi(90, 60)
		case used > 0.85*capacity:
			out = psi(15, 3)
		}
		swap := capacity / 4
		out.Mem = Mem{
			Total: gibs(capacity), Cap: gibs(capacity), Available: gibs(max(0, capacity-used)),
			SwapTotal: gibs(swap), SwapFree: gibs(max(0, swap-swapped)),
		}
		return out
	}
	now := t0
	for second := 0; second < 4*3600; second++ {
		now = now.Add(time.Second)
		for _, r := range runs {
			if r.askAt == second {
				g.Ask("p/"+r.key, r.key, "go test ./...", time.Hour, now)
			}
		}
		// A run killed as it starts is stepped past at once, as the daemon
		// does after any run ends.
		for again := true; again; {
			again = false
			for _, r := range g.Step(now, pressureOf()).Start {
				g.Place(r.Agent, r.Key)
				if s := byKey[r.Key]; s.memory > capacity {
					// memory.max: it can't fit even alone, and is killed.
					s.killed = true
					g.End(r.Agent, r.Key)
					again = true
				}
			}
		}
		running := g.Running()
		waiting := g.Waiting()
		if len(running)+len(waiting) > 0 {
			going := 0
			for _, r := range running {
				if !r.Paused {
					going++
				}
			}
			if going == 0 {
				t.Fatalf("at %ds every run is paused or waiting: %d paused, %d waiting", second, len(running), len(waiting))
			}
			maxRunning = max(maxRunning, going)
		}
		thrashing := pressureOf().Full.Avg10 >= DefaultPolicy.Freeze
		for _, r := range running {
			s := byKey[r.Key]
			if r.Paused || (thrashing && second%4 != 0) {
				continue
			}
			s.done++
			if s.done >= s.work {
				s.finished = true
				g.End(r.Agent, r.Key)
			}
		}
		all := true
		for _, r := range runs {
			all = all && (r.finished || r.killed)
		}
		if all {
			return second, maxRunning
		}
	}
	for _, r := range runs {
		if !r.finished && !r.killed {
			t.Errorf("%s never finished: %d of %ds done", r.key, r.done, r.work)
		}
	}
	return 0, maxRunning
}

func gibs(n float64) int64 { return int64(n * (1 << 30)) }

func testRuns(n int, memory float64, work int, stagger int) []*simRun {
	var out []*simRun
	for i := range n {
		out = append(out, &simRun{key: fmt.Sprintf("r%d", i), memory: memory, work: work, askAt: i * stagger})
	}
	return out
}

func TestManyAgentsStartingTestsAtOnceAllFinish(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pausedShare float64
		runs        []*simRun
	}{
		{"with swap, all at once", 0, testRuns(12, 4, 120, 0)},
		{"without swap, all at once", 1, testRuns(12, 4, 120, 0)},
		{"with swap, a second apart", 0, testRuns(12, 5, 90, 1)},
		{"without swap, a second apart", 1, testRuns(12, 5, 90, 1)},
		{"runs that grow past the VM together", 0.3, testRuns(6, 7, 300, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			took, most := simulate(t, 16, tc.pausedShare, tc.runs)
			if took == 0 {
				return
			}
			t.Logf("finished in %ds, at most %d running at once", took, most)
		})
	}
}

func TestARunTooBigForTheVMIsKilledAndTheRestFinish(t *testing.T) {
	runs := testRuns(4, 3, 60, 0)
	big := &simRun{key: "big", memory: 20, work: 60}
	runs = append([]*simRun{big}, runs...)
	if took, _ := simulate(t, 16, 0, runs); took == 0 {
		return
	}
	if !big.killed {
		t.Fatal("the run bigger than the VM wasn't killed")
	}
	for _, r := range runs[1:] {
		if !r.finished {
			t.Fatalf("%s didn't finish", r.key)
		}
	}
}

// trace20261011 is what the sampler read every ten seconds as eight agents'
// runs (`agentbox heavy -- bash stress.sh`, 100 MB more every two seconds up
// to 2.5 GiB each) filled the CHV VM: 10 CPUs, an 18 GiB cap via virtio-mem,
// 4.5 GiB of zram. Then nothing for seven minutes, the VM thrashing, until
// the kernel killed in all eight agents at 15:01:33; the daemon's first
// pause came at 15:01:39, at full 66%.
var trace20261011 = []struct {
	at                      string
	avail, zram, some, full float64 // GiB, GiB, %, %
}{
	{"14:53:30", 7.8, 2.8, 7.8, 7.1},
	{"14:53:40", 5.9, 4.5, 11.2, 10.7},
	{"14:53:50", 2.3, 4.5, 5.6, 5.3},
}

func traceTime(t *testing.T, hhmmss string) time.Time {
	t.Helper()
	at, err := time.Parse("15:04:05", hhmmss)
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2026, 10, 11, at.Hour(), at.Minute(), at.Second(), 0, time.UTC)
}

// replay steps a governor with the trace's eight runs going every two
// seconds, as the daemon does, from the first reading until the kills, each
// reading held until the next (and the last one on: the daemon's loop, which
// can't be starved now, would read no better). It reports when each run was
// first paused.
func replay(t *testing.T, policy Policy) map[string]time.Time {
	t.Helper()
	g := NewGovernor(policy)
	start := traceTime(t, "14:53:00")
	for i := range 8 {
		key := fmt.Sprintf("r%d", i)
		g.Ask("p/agent-"+key, key, "bash stress.sh", time.Hour, start)
		for _, r := range g.Step(start, Sample{}).Start {
			g.Place(r.Agent, r.Key)
		}
		start = start.Add(policy.AdmitStep)
	}
	if n := len(g.Running()); n != 8 {
		t.Fatalf("%d runs started in a calm VM, want 8", n)
	}
	paused := map[string]time.Time{}
	kills := traceTime(t, "15:01:33")
	next := 0
	var s Sample
	for now := traceTime(t, "14:53:30"); now.Before(kills); now = now.Add(2 * time.Second) {
		if next < len(trace20261011) && !now.Before(traceTime(t, trace20261011[next].at)) {
			r := trace20261011[next]
			s = Sample{
				PSI: PSI{Some: Stall{Avg10: r.some}, Full: Stall{Avg10: r.full}},
				Mem: Mem{Total: gibs(17.8), Available: gibs(r.avail), SwapTotal: gibs(4.5), SwapFree: gibs(4.5 - r.zram), Cap: gibs(18)},
			}
			next++
		}
		for _, r := range g.Step(now, s).Pause {
			if _, ok := paused[r.Key]; !ok {
				paused[r.Key] = now
			}
		}
	}
	return paused
}

func TestTheStallOf20261011PausesBeforeIt(t *testing.T) {
	paused := replay(t, DefaultPolicy)
	if _, ok := paused["r0"]; ok {
		t.Error("the oldest run was paused")
	}
	if len(paused) != 7 {
		t.Fatalf("paused %d runs before the kills, want all 7 but the oldest", len(paused))
	}
	// zram full with a third available: the newest pauses at once.
	if at := paused["r7"]; !at.Equal(traceTime(t, "14:53:40")) {
		t.Errorf("the first pause came at %s, want 14:53:40", at.Format("15:04:05"))
	}
	// 13% available: everything that could grow stops, four seconds before
	// the VM began to thrash and seven minutes before the kills.
	for key, at := range paused {
		if at.After(traceTime(t, "14:53:50")) {
			t.Errorf("%s paused at %s, after 14:53:50", key, at.Format("15:04:05"))
		}
	}
}

func TestPressureAlonePausedNothingBeforeTheStallOf20261011(t *testing.T) {
	// The governor of #252, PSI only: full pressure never reached Freeze.
	policy := DefaultPolicy
	policy.Floor, policy.FloorShare = 0, 0
	if paused := replay(t, policy); len(paused) != 0 {
		t.Fatalf("PSI alone paused %d runs; the trace says it paused none", len(paused))
	}
}

func atCap(availGiB, swapUsedGiB float64) Sample {
	return Sample{Mem: Mem{Total: gibs(15.9), Cap: gibs(16), Available: gibs(availGiB), SwapTotal: gibs(4), SwapFree: gibs(4 - swapUsedGiB)}}
}

func TestHeadroom(t *testing.T) {
	p := DefaultPolicy
	still := Trend{}
	falling := func(gibPerSec float64) Trend { return Trend{Fall: gibPerSec * (1 << 30)} }
	churning := Trend{Churn: 200 << 20}
	// A 15.9 GiB VM's floor is 1.59 GiB, Low 3.18.
	for _, tc := range []struct {
		name string
		mem  Mem
		tr   Trend
		want Headroom
	}{
		{"short of its cap, nearly nothing available", Mem{Total: gibs(8), Cap: gibs(16), Available: gibs(0.1), SwapTotal: gibs(4)}, falling(1), Room},
		{"at its cap with plenty available", atCap(8, 4).Mem, still, Room},
		{"swap full of cold pages, a third available, still", atCap(5.5, 4).Mem, still, Room},
		{"a third available, swap full, falling slowly", atCap(5.5, 4).Mem, falling(0.01), Room},
		{"a third available, swap full, falling a byte a minute", atCap(5.5, 4).Mem, Trend{Fall: 1.0 / 60}, Room},
		{"a third available, swap busy, falling", atCap(5.5, 2.5).Mem, falling(0.05), Tight},
		{"little available, swap to spare", atCap(2, 1).Mem, still, Room},
		{"little available, swap full, still", atCap(3, 4).Mem, still, Tight},
		{"plenty available, swap full, churning", atCap(6, 4).Mem, churning, Tight},
		{"little available, swap full, churning", atCap(3, 4).Mem, churning, Short},
		{"swap full, falling to the floor in 20s", atCap(5.5, 4).Mem, falling(0.2), Short},
		{"swap full, falling to the floor in 5s", atCap(3, 4).Mem, falling(0.3), Out},
		{"under the floor, swap full", atCap(1, 4).Mem, still, Out},
		{"under the floor, swap to spare", atCap(1, 2).Mem, still, Room},
		{"nearly nothing available, no swap", Mem{Total: gibs(16), Available: gibs(1)}, still, Out},
		{"unread", Mem{}, falling(1), Room},
	} {
		if got := p.Headroom(tc.mem, tc.tr); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestATrendIsMeasuredOverASpan(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	at := func(sec int, availGiB, refaultedGiB float64) Trend {
		s := atCap(availGiB, 4)
		s.Mem.Refaulted = gibs(refaultedGiB)
		g.Step(t0.Add(time.Duration(sec)*time.Second), s)
		return g.Trend()
	}
	at(0, 8, 0)
	// A gigabyte gone in two seconds isn't a ramp yet: under a Span.
	if tr := at(2, 7, 0); tr.Fall != 0 {
		t.Errorf("fall over 2s = %v, want none yet", tr.Fall)
	}
	if tr := at(10, 7, 1); tr.Fall != 0.1*(1<<30) || tr.Churn != 0.1*(1<<30) {
		t.Errorf("over 10s: %+v, want 0.1 GiB/s of each", tr)
	}
	// Past the Window, the readings before it are forgotten.
	at(20, 7, 1)
	at(30, 7, 1)
	if tr := at(41, 7, 1); tr != (Trend{}) {
		t.Errorf("held still for 30s: %+v, want nothing", tr)
	}
}

// trace20261012 is the VM on nightly .84 from 19:47 to 19:56 on 2026-10-12:
// at its 17.8 GiB cap, 6.2 GiB available, zram 4.38 of 4.5 GiB used by idle
// agents' cold pages, PSI some/full about 0.3/0.2, next to nothing swapped
// back in. #273's rules paused sai-app-4/agent-33's `npx vp test run` and
// held a `python3 - <<'EOF'` edit for six minutes.
func trace20261012(sec int) Sample {
	return Sample{
		PSI: PSI{Some: Stall{Avg10: 0.3}, Full: Stall{Avg10: 0.2}},
		Mem: Mem{Total: gibs(17.8), Cap: gibs(18), Available: gibs(6.2 - 0.05*float64(sec%3)),
			SwapTotal: gibs(4.5), SwapFree: gibs(4.5 - 4.38), Refaulted: gibs(3) + int64(sec)<<20},
	}
}

func TestAFullSwapWithMemoryToSpareHoldsNothing(t *testing.T) {
	// What #273's rules took for Short: under 35% available, swap 90% full.
	if s := trace20261012(0); s.Mem.AvailableShare() >= 0.35 || s.Mem.SwapUsed() < 0.9 {
		t.Fatalf("the trace isn't what #273 paused for: %s", s)
	}
	g := NewGovernor(DefaultPolicy)
	now := t0
	for i := range 6 {
		g.Ask(fmt.Sprintf("p/a%d", i), "k", "npx vp test run --maxWorkers=2", time.Hour, now)
	}
	// Nine minutes of readings, every two seconds, as the daemon takes them.
	for sec := 0; sec < 9*60; sec += 2 {
		now = t0.Add(time.Duration(sec) * time.Second)
		got := g.Step(now, trace20261012(sec))
		for _, r := range got.Start {
			g.Place(r.Agent, r.Key)
		}
		if len(got.Pause) > 0 {
			t.Fatalf("at %ds paused %v: %s", sec, keys(got.Pause), trace20261012(sec))
		}
		if why := g.Why(); why != "" {
			t.Fatalf("at %ds held back for %q: %s", sec, why, trace20261012(sec))
		}
	}
	if n := len(g.Waiting()); n != 0 {
		t.Fatalf("%d runs still waiting", n)
	}
}

func TestNothingStartsOrResumesWhileTight(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	g.Ask("p/a", "a", "", time.Hour, t0)
	g.Step(t0, Sample{})
	g.Ask("p/b", "b", "", time.Hour, t0)
	now := t0
	for range 30 {
		now = now.Add(time.Second)
		if got := g.Step(now, atCap(3, 4)); len(got.Start) != 0 {
			t.Fatal("a run started with the VM tight")
		}
	}
	if g.Why() != Low {
		t.Errorf("waiting for %q, want %q", g.Why(), Low)
	}
	now = now.Add(time.Second)
	if got := g.Step(now, atCap(8, 4)); len(got.Start) != 1 {
		t.Fatal("the waiting run didn't start once there was room")
	}
	g.Place("p/a", "a")
	g.Place("p/b", "b")
	now = now.Add(time.Second)
	if got := g.Step(now, atCap(1, 4)); len(got.Pause) != 1 || got.Pause[0].Why != Low {
		t.Fatalf("out of memory, paused %v", keys(got.Pause))
	}
	// Calm PSI, but swap stays full and only 4 GiB is available, under
	// three floors: not yet.
	for range 30 {
		now = now.Add(time.Second)
		if got := g.Step(now, atCap(4, 4)); len(got.Resume) != 0 {
			t.Fatal("a paused run resumed with the VM still short of room")
		}
	}
	if g.Why() != Easing {
		t.Errorf("paused for %q, want %q", g.Why(), Easing)
	}
	for range 30 {
		now = now.Add(time.Second)
		if got := g.Step(now, atCap(6, 4)); len(got.Resume) == 1 {
			return
		}
	}
	t.Fatal("the paused run never resumed once there was room")
}

func TestTheFleetPausesAnAgentsRunsAndStartAllLeavesThem(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	for _, key := range []string{"1", "2"} {
		g.Ask("p/a", key, "vue-tsc", time.Minute, t0)
	}
	g.Ask("p/b", "1", "vitest", time.Minute, t0)
	g.StartAll(t0)
	g.Place("p/a", "1")
	g.Place("p/b", "1")
	paused := g.PauseAgent("p/a", t0, Low)
	if len(paused) != 1 || paused[0].ID() != "p/a/1" {
		t.Fatalf("paused %v, want p/a's placed run only", paused)
	}
	if got := g.StartAll(t0.Add(time.Second)); len(got.Resume) != 0 {
		t.Fatalf("StartAll resumed %v, which the fleet paused", got.Resume)
	}
	if got := g.ResumeAgent("p/a", t0.Add(2*time.Second)); len(got) != 1 || got[0].Paused {
		t.Fatalf("resumed %v", got)
	}
	if got := g.ResumeAgent("", t0.Add(3*time.Second)); len(got) != 0 {
		t.Fatalf("resumed %v again", got)
	}
}
