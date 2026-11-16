package pressure

import (
	"testing"
	"time"
)

func roomy(avail float64) Mem {
	return Mem{Total: gibs(16), Cap: gibs(16), Available: gibs(avail), SwapTotal: gibs(4), SwapFree: gibs(4)}
}

// Machines boot oldest first, one a BootStep, only while one fits, and
// never while builds wait for memory; with nothing running, the first boots
// whatever the reading.
func TestAdmissionBootsOldestFirstWhileOneFits(t *testing.T) {
	a := NewAdmission(DefaultPolicy)
	now := t0
	a.Ask("p/new", t0.Add(time.Hour), now)
	a.Ask("p/old", t0, now)
	if n, ok := a.Place("p/new"); !ok || n != 1 {
		t.Fatalf("p/new is %d in line (%v), want behind the older p/old", n, ok)
	}
	// Nothing runs: the first boots, whatever the memory.
	if got := a.Step(now, roomy(0.5), false, 0); len(got) != 1 || got[0] != "p/old" {
		t.Fatalf("with nothing running booted %v, want p/old", got)
	}
	// Room for one more, but within BootStep of the last.
	if got := a.Step(now.Add(2*time.Second), roomy(8), false, 1); len(got) != 0 || a.Why() != WaitTurn {
		t.Fatalf("booted %v within BootStep (%q)", got, a.Why())
	}
	now = now.Add(10 * time.Second)
	// Builds wait for memory: they come first.
	if got := a.Step(now, roomy(8), true, 1); len(got) != 0 || a.Why() != WaitBuilds {
		t.Fatalf("booted %v while builds were held (%q)", got, a.Why())
	}
	// Short of a boot over the floor: 1.6 GiB floor + 1.25 GiB boot, less
	// the 1.25 GiB of the boot not yet showing.
	if got := a.Step(now, roomy(3.5), false, 1); len(got) != 0 || a.Why() != WaitMemory {
		t.Fatalf("booted %v with 3.5 GiB and a boot still settling (%q)", got, a.Why())
	}
	if got := a.Step(now, roomy(4.5), false, 1); len(got) != 1 || got[0] != "p/new" {
		t.Fatalf("booted %v with 4.5 GiB available", got)
	}
	if len(a.Waiting()) != 0 || a.Why() != "" {
		t.Errorf("still waiting: %v (%q)", a.Waiting(), a.Why())
	}
	if !a.Booted("p/new") || !a.Ask("p/new", t0, now) {
		t.Error("a booted machine asking again waits")
	}
}

// Nobody boots for having waited long: a boot into memory that isn't there
// gets processes killed.
func TestAdmissionNeverBootsIntoMissingMemory(t *testing.T) {
	a := NewAdmission(DefaultPolicy)
	a.Ask("p/a", t0, t0)
	for sec := 0; sec < 3600; sec += 2 {
		if got := a.Step(t0.Add(time.Duration(sec)*time.Second), roomy(2), false, 5); len(got) != 0 {
			t.Fatalf("booted %v after %ds with 2 GiB available", got, sec)
		}
	}
	a.Cancel("p/a")
	if _, ok := a.Place("p/a"); ok {
		t.Error("still in line after it gave up")
	}
}

// The agent the user is looking at goes first among the waiting builds; the
// others keep their order.
func TestGovernorPrefersTheViewedAgent(t *testing.T) {
	g := NewGovernor(DefaultPolicy)
	for _, a := range []string{"p/a", "p/b", "p/c"} {
		g.Ask(a, "k", "go test", time.Hour, t0)
	}
	g.Prefer("p/c")
	var order []string
	for _, r := range g.Waiting() {
		order = append(order, r.Agent)
	}
	if got := order[0] + order[1] + order[2]; got != "p/cp/ap/b" {
		t.Errorf("order %v", order)
	}
}
