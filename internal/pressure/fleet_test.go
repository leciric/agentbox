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

// vmBusy is a VM at its 16 GiB cap with avail available and swap 60% used:
// under the floor, Short, and freezing a machine still frees memory.
func vmBusy(avail int64) Sample {
	return Sample{Mem: Mem{Total: 16 * gb, Available: avail, SwapTotal: 4 * gb, SwapFree: 4 * gb * 4 / 10}}
}

// holdAll steps f under s from now, a FreezeSettle apart, until it holds
// nothing more: what it froze, in order, and when it was done.
func holdAll(f *Fleet, now time.Time, ms []Machine, s Sample) ([]string, time.Time) {
	var order []string
	for range 20 {
		resync(f, now, ms)
		got := f.Step(now, s)
		order = append(order, got.Freeze...)
		if len(got.Freeze)+len(got.PauseRuns) == 0 && now.Sub(f.changed) >= FreezeSettle {
			return order, now
		}
		now = now.Add(FreezeSettle)
	}
	return order, now
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
func resync(f *Fleet, now time.Time, ms []Machine) {
	for i := range ms {
		ms[i].Frozen = f.Frozen(ms[i].Agent)
		ms[i].RunsPaused = f.RunsPaused(ms[i].Agent)
	}
	f.Sync(now, ms)
}

func TestUnderStallingPressureTheNewestFreezeFirstNeverTheOldestWorking(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	f.Sync(t0, ms)
	var order []string
	now := t0
	for range 120 {
		now = now.Add(time.Second)
		resync(f, now, ms)
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
	f.Sync(t0, machines("p/a", "p/b"))
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
	got, _ := holdAll(f, t0, ms, vmBusy(gb))
	if !slices.Equal(got, []string{"p/d", "p/c", "p/a"}) {
		t.Fatalf("short of memory froze %v, want everything but p/b, newest first", got)
	}
}

func TestTheOldestWorkingThawsAtOnceWhateverThePressure(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/b", "p/c")
	_, now := holdAll(f, t0, ms, vmBusy(gb)) // freezes a and c and d; b is kept
	// b's turn ends: c, frozen mid-turn, is the oldest working now.
	ms = machines("p/c")
	now = now.Add(time.Second)
	resync(f, now, ms)
	got := f.Step(now, vmBusy(gb))
	if !slices.Equal(got.Thaw, []string{"p/c"}) {
		t.Fatalf("thawed %v, want the oldest working p/c at once", got.Thaw)
	}
	if froze, _ := holdAll(f, now, ms, vmBusy(gb)); !slices.Equal(froze, []string{"p/b"}) {
		t.Fatalf("froze %v, want the now idle p/b with the VM short of memory", froze)
	}
}

func TestFrozenMachinesThawOldestFirstOnceCalm(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	_, now := holdAll(f, t0, ms, vmBusy(gb))
	var order []string
	for range 120 {
		now = now.Add(time.Second)
		resync(f, now, ms)
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
	holdAll(f, t0, ms, vmBusy(gb))
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
	f.Sync(t0, machines("p/a", "p/b", "p/c"))
	f.Ask("p/e", t0)
	got := f.Step(t0, vm(8*gb))
	if !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("with 8 GB free started %v", got.Start)
	}
}

func TestANewAgentWaitsWhenItsBootDoesntFitOverTheFloor(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(t0, machines("p/a"))
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
	f.Sync(t0, machines("p/a"))
	f.Ask("p/e", t0)
	s := Sample{Mem: Mem{Total: 8 * gb, Available: gb, Cap: 16 * gb, SwapTotal: gb, SwapFree: gb}}
	if got := f.Step(t0, s); !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("a VM 8 GiB short of its cap started %v", got.Start)
	}
}

func TestBootsNotYetShowingAreHeldAgainstWhatIsAvailable(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(t0, machines("p/a"))
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
	_, now := holdAll(f, t0, ms, vmBusy(gb)) // freezes b, c, d
	f.Ask("p/e", now)
	for range 5 {
		now = now.Add(time.Second)
		resync(f, now, ms)
		if got := f.Step(now, vm(10*gb)); len(got.Start) != 0 {
			t.Fatalf("started %v ahead of frozen machines", got.Start)
		}
	}
}

func TestWithNothingWorkingTheFirstWaiterStartsWhateverThePressure(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(t0, machines())
	f.Ask("p/e", t0)
	if got := f.Step(t0, vm(gb)); !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("with every machine idle started %v", got.Start)
	}
}

func TestAMachineResumedBySomebodyElseIsNoLongerFrozen(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b")
	f.Sync(t0, ms)
	f.Step(t0, vmBusy(gb))
	if !f.Frozen("p/d") {
		t.Fatal("p/d not frozen")
	}
	f.Sync(t0.Add(FreezeSettle), machines("p/a", "p/b")) // every one running, none frozen
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
	f.Sync(t0, machines("p/a", "p/b", "p/c"))
	s := Sample{Mem: Mem{Total: 16 * gb, Available: gb / 2, SwapTotal: 4 * gb, SwapFree: 4 * gb * 29 / 100}}
	if got := f.Step(t0, s); !slices.Equal(got.Freeze, []string{"p/d"}) {
		t.Fatalf("froze %v, want the newest only", got.Freeze)
	}
	// With swap mostly free, the kernel makes room: nothing freezes.
	g := NewFleet(DefaultPolicy)
	g.Sync(t0, machines("p/a", "p/b", "p/c"))
	s.Mem.SwapFree = 4 * gb * 8 / 10
	if got := g.Step(t0, s); len(got.Freeze) != 0 {
		t.Fatalf("with swap 20%% used froze %v", got.Freeze)
	}
}

// readings has the fleet see ms at now, each using what it did at t0 plus
// what grows says it grows by a second.
func readings(f *Fleet, now time.Time, ms []Machine, grows map[string]float64) {
	for i := range ms {
		ms[i].Memory = gb + int64(grows[ms[i].Agent]*now.Sub(t0).Seconds())
	}
	resync(f, now, ms)
}

func TestOutHoldsOneMachineAtATimeAndSparesIdleOnesWithSwapFull(t *testing.T) {
	// 2026-10-23, 01:14: seven agents on an 18 GB VM, zram full; 118 and
	// 119 each type-checking (vue-tsc, heavy), memory falling at up to 180
	// MB/s. The fleet froze six machines in 2 seconds, freeing nothing.
	f := NewFleet(DefaultPolicy)
	var ms []Machine
	for i, ref := range []string{"s/116", "s/117", "s/118", "s/119", "s/120", "s/121", "s/122"} {
		ms = append(ms, Machine{Agent: ref, Since: t0.Add(time.Duration(i) * time.Minute), Working: true})
	}
	ms[2].Heavy, ms[3].Heavy = true, true
	grows := map[string]float64{"s/118": 90 << 20, "s/119": 90 << 20}
	out := Sample{Mem: Mem{Total: 18 * gb, Available: gb, SwapTotal: 4.6 * 1e9, SwapFree: 0}}
	var froze, paused []string
	now := t0
	for range 30 { // a minute, a reading every 2 seconds
		now = now.Add(2 * time.Second)
		readings(f, now, ms, grows)
		got := f.Step(now, out)
		if n := len(got.Freeze) + len(got.PauseRuns); n > 1 {
			t.Fatalf("held %d machines in one step: %+v", n, got)
		}
		froze = append(froze, got.Freeze...)
		paused = append(paused, got.PauseRuns...)
		// Paused, a type check stops growing.
		for _, ref := range got.PauseRuns {
			grows[ref] = 0
		}
	}
	if !slices.Equal(paused, []string{"s/119", "s/118"}) || len(froze) != 0 {
		t.Fatalf("paused the runs of %v and froze %v; want only the type checks paused, newest first", paused, froze)
	}
	if !f.Spared() || !f.SwapFull() {
		t.Fatalf("spared %v, swap full %v: want the idle machines left running and said so", f.Spared(), f.SwapFull())
	}
}

func TestAMachineBeingFrozenIsNotFrozenAgain(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	f.Sync(t0, ms)
	if got := f.Step(t0, vmBusy(gb)); !slices.Equal(got.Freeze, []string{"p/d"}) {
		t.Fatalf("froze %v", got.Freeze)
	}
	// Its cgroup reads "not frozen" for a while yet (read before the freeze,
	// or a task stuck in reclaim): it is freezing, not resumed.
	var again []string
	for now := t0.Add(time.Second); now.Before(t0.Add(FreezeSettle)); now = now.Add(time.Second) {
		f.Sync(now, machines("p/a", "p/b", "p/c", "p/d"))
		got := f.Step(now, vmBusy(gb))
		again = append(again, got.Freeze...)
	}
	if len(again) != 0 || !f.Frozen("p/d") {
		t.Fatalf("froze %v again within FreezeSettle, p/d frozen %v", again, f.Frozen("p/d"))
	}
}

func TestTheMachineGrowingFastestFreezesFirstThenOneWithAHeavyCommand(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	ms[2].Heavy = true
	grows := map[string]float64{"p/b": 100 << 20}
	readings(f, t0, ms, grows) // what the daemon read before memory ran short
	var order []string
	now := t0
	for range 15 {
		now = now.Add(2 * time.Second)
		readings(f, now, ms, grows)
		got := f.Step(now, vmBusy(gb))
		order = append(order, got.Freeze...)
		for _, ref := range got.Freeze {
			grows[ref] = 0
		}
	}
	if !slices.Equal(order, []string{"p/b", "p/c", "p/d"}) {
		t.Fatalf("froze %v, want the growing p/b, then p/c with a heavy command, then the newest", order)
	}
}

func TestWithSwapFullAnIdleMachineIsNotFrozen(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a")
	var froze []string
	now := t0
	for range 15 {
		now = now.Add(2 * time.Second)
		readings(f, now, ms, nil)
		froze = append(froze, f.Step(now, vm(gb)).Freeze...)
	}
	if len(froze) != 0 {
		t.Fatalf("with swap full froze idle %v, which frees nothing", froze)
	}
	if !f.Spared() {
		t.Fatal("not said that idle machines were left running")
	}
	// Once it eases, that is no longer said.
	now = now.Add(2 * time.Second)
	readings(f, now, ms, nil)
	f.Step(now, vm(12*gb))
	if f.Spared() {
		t.Fatal("still spared once memory eased")
	}
}

func TestWithSwapFullAMachineThatGrowsWithItsRunsPausedFreezesThenThawsWhole(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/d")
	ms[3].Heavy = true
	grows := map[string]float64{"p/d": 50 << 20} // a dev server, not its build, grows
	var froze, paused []string
	now := t0
	for range 10 {
		now = now.Add(2 * time.Second)
		readings(f, now, ms, grows)
		got := f.Step(now, vm(gb))
		froze, paused = append(froze, got.Freeze...), append(paused, got.PauseRuns...)
	}
	if !slices.Equal(paused, []string{"p/d"}) || !slices.Equal(froze, []string{"p/d"}) {
		t.Fatalf("paused %v, froze %v: want p/d's runs paused, then, growing still, p/d frozen", paused, froze)
	}
	var thawed, resumed []string
	for range 60 {
		now = now.Add(2 * time.Second)
		readings(f, now, ms, nil)
		got := f.Step(now, vm(12*gb))
		thawed, resumed = append(thawed, got.Thaw...), append(resumed, got.ResumeRuns...)
	}
	if !slices.Equal(thawed, []string{"p/d"}) || !slices.Equal(resumed, []string{"p/d"}) {
		t.Fatalf("thawed %v, resumed the runs of %v once calm", thawed, resumed)
	}
}

func TestAMachineTheFleetForgotButIsToldOfAsFrozenThawsOnceCalm(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	ms := machines("p/a", "p/b", "p/c", "p/d")
	f.Sync(t0, ms)
	f.Step(t0, vmBusy(gb)) // freezes p/d
	// Past FreezeSettle it reads as not frozen: taken as resumed, forgotten.
	now := t0.Add(FreezeSettle)
	f.Sync(now, machines("p/a", "p/b", "p/c", "p/d"))
	if f.Frozen("p/d") {
		t.Fatal("still frozen")
	}
	// Then it reads frozen, the daemon telling it as the fleet's own.
	ms = machines("p/a", "p/b", "p/c", "p/d")
	ms[3].Frozen = true
	var thawed []string
	for range 30 {
		now = now.Add(2 * time.Second)
		f.Sync(now, ms)
		got := f.Step(now, vm(12*gb))
		thawed = append(thawed, got.Thaw...)
		for i := range ms {
			ms[i].Frozen = f.Frozen(ms[i].Agent)
		}
	}
	if !slices.Equal(thawed, []string{"p/d"}) {
		t.Fatalf("thawed %v, want p/d once calm", thawed)
	}
}
