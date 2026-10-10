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

func psi(some, full float64) PSI {
	return PSI{Some: Stall{Avg10: some}, Full: Stall{Avg10: full}}
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
	pressureOf := func() PSI {
		var used float64
		for _, r := range g.Running() {
			share := 1.0
			if r.Paused {
				share = pausedShare
			}
			used += byKey[r.Key].memory * share
		}
		switch {
		case used > capacity:
			return psi(90, 60)
		case used > 0.85*capacity:
			return psi(15, 3)
		}
		return psi(0, 0)
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
