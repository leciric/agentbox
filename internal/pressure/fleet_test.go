package pressure

import (
	"slices"
	"testing"
	"time"
)

const gb = int64(1) << 30

// vm is a VM at its 16 GiB cap with avail available and swap full.
func vm(avail int64) Sample {
	return Sample{Mem: Mem{Total: 16 * gb, Available: avail, SwapTotal: 4 * gb, SwapFree: 0}}
}

// machines are four agents made a minute apart, a oldest; working says which
// work.
func machines(working ...string) []Machine {
	var out []Machine
	for i, name := range []string{"p/a", "p/b", "p/c", "p/d"} {
		out = append(out, Machine{Agent: name, Since: t0.Add(time.Duration(i) * time.Minute), Working: slices.Contains(working, name)})
	}
	return out
}

// resync is what the daemon tells the fleet each step: the machines, frozen
// as the fleet left them.
func resync(f *Fleet, ms []Machine) {
	for i := range ms {
		ms[i].Frozen = f.Frozen(ms[i].Agent)
	}
	f.Sync(ms)
}

func TestUnderStallingPressureTheNewestFreezeFirstNeverTheOldestWorking(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	f.Sync(ms)
	var order []string
	now := t0
	for range 120 {
		now = now.Add(time.Second)
		resync(f, ms)
		got := f.Step(now, psi(40, 30))
		order = append(order, got.Freeze...)
	}
	if !slices.Equal(order, []string{"p/d", "p/c", "p/b"}) {
		t.Fatalf("froze %v, want newest first and never p/a", order)
	}
	if f.Frozen("p/a") {
		t.Fatal("the oldest working agent was frozen")
	}
}

func TestStallingFreezesOnlyOnceItHolds(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(machines("p/a", "p/b"))
	if got := f.Step(t0, psi(40, 30)); len(got.Freeze) != 0 {
		t.Fatalf("froze %v at the first high reading", got.Freeze)
	}
	if got := f.Step(t0.Add(DefaultPolicy.FreezeAfter), psi(40, 30)); !slices.Equal(got.Freeze, []string{"p/d"}) {
		t.Fatalf("froze %v once it held", got.Freeze)
	}
}

func TestAnIdleOldestAgentIsNotWhatIsKeptGoing(t *testing.T) {
	// a is idle; b is the oldest working, so b is kept and a may freeze.
	f := NewFleet(DefaultPolicy)
	ms := machines("p/b")
	f.Sync(ms)
	got := f.Step(t0, vm(gb)) // under the floor, swap full: Out
	if !slices.Equal(got.Freeze, []string{"p/d", "p/c", "p/a"}) {
		t.Fatalf("out of memory froze %v, want everything but p/b, newest first", got.Freeze)
	}
}

func TestTheOldestWorkingThawsAtOnceWhateverThePressure(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/b", "p/c")
	f.Sync(ms)
	f.Step(t0, vm(gb)) // freezes a and c and d; b is kept
	// b's turn ends: c, frozen mid-turn, is the oldest working now.
	ms = machines("p/c")
	resync(f, ms)
	got := f.Step(t0.Add(time.Second), vm(gb))
	if !slices.Equal(got.Thaw, []string{"p/c"}) {
		t.Fatalf("thawed %v, want the oldest working p/c at once", got.Thaw)
	}
	if !slices.Contains(got.Freeze, "p/b") {
		t.Fatalf("froze %v, want the now idle p/b with the VM out of memory", got.Freeze)
	}
}

func TestFrozenMachinesThawOldestFirstOnceCalm(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	f.Sync(ms)
	f.Step(t0, vm(gb))
	var order []string
	now := t0
	for range 120 {
		now = now.Add(time.Second)
		resync(f, ms)
		got := f.Step(now, vm(12*gb))
		if len(got.Freeze) > 0 {
			t.Fatalf("froze %v while calm", got.Freeze)
		}
		order = append(order, got.Thaw...)
	}
	if !slices.Equal(order, []string{"p/b", "p/c", "p/d"}) {
		t.Fatalf("thawed %v, want oldest first", order)
	}
}

func TestAheadCountsTheFrozenThawingBeforeAndTheWaitersStartingBefore(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	f.Sync(ms)
	f.Step(t0, vm(gb))
	f.Ask("p/e", t0)
	f.Ask("p/f", t0)
	for agent, want := range map[string]int{"p/b": 0, "p/c": 1, "p/d": 2, "p/e": 3, "p/f": 4} {
		if n, _, ok := f.Ahead(agent); !ok || n != want {
			t.Errorf("%s: %d ahead (held %v), want %d", agent, n, ok, want)
		}
	}
	if _, _, ok := f.Ahead("p/a"); ok {
		t.Error("the agent kept going is held")
	}
}

func TestANewAgentStartsOnRealFreeMemoryNotOnCounts(t *testing.T) {
	// The 2026-10-22 case: agents busy, 8 GB free. Plenty for a boot.
	f := NewFleet(DefaultPolicy)
	f.Sync(machines("p/a", "p/b", "p/c"))
	f.Ask("p/e", t0)
	got := f.Step(t0, vm(8*gb))
	if !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("with 8 GB free started %v", got.Start)
	}
}

func TestANewAgentWaitsWhenItsBootDoesntFitOverTheFloor(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(machines("p/a"))
	f.Ask("p/e", t0)
	// The floor is 1.6 GiB (10% of 16), the boot 1.25: 2.5 doesn't fit.
	if got := f.Step(t0, vm(5*gb/2)); len(got.Start) != 0 {
		t.Fatalf("started %v with 2.5 GiB available", got.Start)
	}
	if n, frozen, ok := f.Ahead("p/e"); !ok || frozen || n != 0 {
		t.Fatalf("waiter: %d ahead, frozen %v, held %v", n, frozen, ok)
	}
	if f.Why() != Low {
		t.Fatalf("why %q", f.Why())
	}
	if got := f.Step(t0.Add(5*time.Second), vm(4*gb)); !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("with 4 GiB started %v", got.Start)
	}
}

func TestAVMThatMayStillGrowCountsWhatItMayGrowBy(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(machines("p/a"))
	f.Ask("p/e", t0)
	s := Sample{Mem: Mem{Total: 8 * gb, Available: gb, Cap: 16 * gb, SwapTotal: gb, SwapFree: gb}}
	if got := f.Step(t0, s); !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("a VM 8 GiB short of its cap started %v", got.Start)
	}
}

func TestBootsNotYetShowingAreHeldAgainstWhatIsAvailable(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(machines("p/a"))
	for _, a := range []string{"p/e", "p/f", "p/g", "p/h"} {
		f.Ask(a, t0)
	}
	var started []string
	now := t0
	for range 20 { // 4.5 GiB available holds still: two boots fit, not four
		now = now.Add(time.Second)
		started = append(started, f.Step(now, vm(9*gb/2)).Start...)
	}
	if !slices.Equal(started, []string{"p/e", "p/f"}) {
		t.Fatalf("started %v", started)
	}
	// Once those boots show (or settle), the next may go.
	now = now.Add(BootSettle)
	if got := f.Step(now, vm(9*gb/2)); !slices.Equal(got.Start, []string{"p/g"}) {
		t.Fatalf("after the boots settled started %v", got.Start)
	}
}

func TestNothingStartsWhileAMachineIsFrozen(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b")
	f.Sync(ms)
	f.Step(t0, vm(gb)) // freezes b, c, d
	f.Ask("p/e", t0)
	now := t0.Add(time.Second)
	for range 5 {
		now = now.Add(time.Second)
		resync(f, ms)
		if got := f.Step(now, vm(10*gb)); len(got.Start) != 0 {
			t.Fatalf("started %v ahead of frozen machines", got.Start)
		}
	}
}

func TestWithNothingWorkingTheFirstWaiterStartsWhateverThePressure(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(machines())
	f.Ask("p/e", t0)
	if got := f.Step(t0, vm(gb)); !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("with every machine idle started %v", got.Start)
	}
}

func TestAMachineResumedBySomebodyElseIsNoLongerFrozen(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b")
	f.Sync(ms)
	f.Step(t0, vm(gb))
	if !f.Frozen("p/d") {
		t.Fatal("p/d not frozen")
	}
	f.Sync(machines("p/a", "p/b")) // every one running, none frozen
	if f.Frozen("p/d") {
		t.Fatal("p/d still frozen after it was resumed")
	}
}

func TestACancelledWaiterGoes(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Ask("p/e", t0)
	f.Cancel("p/e")
	if len(f.Waiting()) != 0 {
		t.Fatal("still waiting")
	}
}

func TestMedianBootCost(t *testing.T) {
	if got := MedianBootCost(nil); got != DefaultBootCost {
		t.Fatalf("none measured: %d", got)
	}
	if got := MedianBootCost([]int64{gb, 2 * gb, gb / 4}); got != gb {
		t.Fatalf("median %d", got)
	}
	if got := MedianBootCost([]int64{gb / 8}); got != MinBootCost {
		t.Fatalf("tiny %d", got)
	}
	if got := MedianBootCost([]int64{10 * gb}); got != MaxBootCost {
		t.Fatalf("huge %d", got)
	}
}

func TestUnderTheFloorWithSwapHalfFullTheNewestFreezesBeforeSwapIsFull(t *testing.T) {
	// The fleet lab's 2026-10-22 reading: nothing available, swap 71% used.
	f := NewFleet(DefaultPolicy)
	f.Sync(machines("p/a", "p/b", "p/c"))
	s := Sample{Mem: Mem{Total: 16 * gb, Available: gb / 2, SwapTotal: 4 * gb, SwapFree: 4 * gb * 29 / 100}}
	if got := f.Step(t0, s); !slices.Equal(got.Freeze, []string{"p/d"}) {
		t.Fatalf("froze %v, want the newest only", got.Freeze)
	}
	// With swap mostly free, the kernel makes room: nothing freezes.
	g := NewFleet(DefaultPolicy)
	g.Sync(machines("p/a", "p/b", "p/c"))
	s.Mem.SwapFree = 4 * gb * 8 / 10
	if got := g.Step(t0, s); len(got.Freeze) != 0 {
		t.Fatalf("with swap 20%% used froze %v", got.Freeze)
	}
}
