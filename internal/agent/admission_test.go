package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckSize(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"": "", "auto": "", "light": "light", "normal": "normal", "heavy": "heavy"} {
		if got, err := CheckSize(in); err != nil || got != want {
			t.Errorf("CheckSize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := CheckSize("huge"); err == nil {
		t.Error("an unknown size was accepted")
	}
}

func TestBurst(t *testing.T) {
	t.Parallel()
	shape := Shape{Baseline: 600 << 20, Burst: 3 * gib}
	for size, want := range map[string]int64{"": 3 * gib, "normal": 3 * gib, "light": 2*gib - 600<<20, "heavy": 8*gib - 600<<20} {
		if got := Burst(size, shape); got != want {
			t.Errorf("Burst(%q) = %d, want %d", size, got, want)
		}
	}
	// A project whose heavy phases were seen taking more than 8 GB keeps that.
	if got := Burst(SizeHeavy, Shape{Baseline: gib, Burst: 10 * gib}); got != 10*gib {
		t.Errorf("heavy burst = %d, want the learned 10 GiB", got)
	}
	// Light never goes below the floor, however big the baseline.
	if got := Burst(SizeLight, Shape{Baseline: 3 * gib, Burst: gib}); got != minBurst {
		t.Errorf("light burst = %d, want %d", got, minBurst)
	}
	if got := learnedShape(0, 0); got != (Shape{Baseline: defaultBaseline, Burst: defaultBurst}) {
		t.Errorf("an unknown project's shape = %+v", got)
	}
	if got := learnedShape(600<<20, 100<<20); got.Burst != minBurst {
		t.Errorf("a tiny learned burst = %d, want the floor", got.Burst)
	}
	if PoolFloor(16*gib) != 4*gib || PoolFloor(2*gib) != gib || PoolFloor(gib/2) != gib/2 {
		t.Error("PoolFloor isn't a quarter, at least 1 GiB, at most everything")
	}
}

func TestHolderCountsBaselineOnly(t *testing.T) {
	t.Parallel()
	// Bursting under a lease: admission counts its baseline, the pool all of it.
	h := Holder{Reserved: gib, Lease: 3 * gib, Using: 3 * gib}
	if h.Counts() != gib || h.Holds() != 4*gib {
		t.Errorf("leased: counts %d, holds %d", h.Counts(), h.Holds())
	}
	// Grown past its baseline with no lease: counts what it uses.
	h = Holder{Reserved: gib, Using: 3 * gib}
	if h.Counts() != 3*gib || h.Holds() != 3*gib {
		t.Errorf("grown: counts %d, holds %d", h.Counts(), h.Holds())
	}
}

func TestBurstPool(t *testing.T) {
	t.Parallel()
	holders := []Holder{{Ref: "a", Reserved: 2 * gib}, {Ref: "b", Reserved: 2 * gib}}
	if !BurstFits(8*gib, holders, 3*gib) {
		t.Error("a burst that fits wasn't given")
	}
	// Bigger than what's free, but nobody else bursts: it runs alone.
	if !BurstFits(8*gib, holders, 6*gib) {
		t.Error("a lone big burst was refused")
	}
	holders[1].Lease = 3 * gib
	if BurstFits(8*gib, holders, 2*gib) {
		t.Error("a burst that doesn't fit beside another's lease was given")
	}
	free, leased, agents := BurstFree(8*gib, holders)
	if free != gib || leased != 3*gib || agents != 1 {
		t.Errorf("BurstFree = %d, %d, %d", free, leased, agents)
	}
}

func TestBurstEnv(t *testing.T) {
	t.Parallel()
	if got := BurstEnv(3*gib, 16)["GOFLAGS"]; got != "-p=4" {
		t.Errorf("3 GiB on 16 cores: GOFLAGS = %q, want -p=4", got)
	}
	if got := BurstEnv(8*gib, 2)["VITEST_MAX_THREADS"]; got != "2" {
		t.Errorf("8 GiB on 2 cores: %q, want the core count", got)
	}
	if got := BurstEnv(256<<20, 8)["CARGO_BUILD_JOBS"]; got != "1" {
		t.Errorf("a small lease: %q, want 1", got)
	}
}

func TestAdmit(t *testing.T) {
	t.Parallel()
	now := time.Now()
	holders := []Holder{
		{Ref: "p/a", Project: "p", Reserved: 4 * gib},
		{Ref: "r/b", Project: "r", Reserved: 4 * gib, Using: 6 * gib}, // grew: counts 6
	}
	verdicts := func(capacity int64, holders []Holder, waiting []Waiter, free map[string]int) map[string]Verdict {
		return Admit(capacity, holders, waiting, free, now, 0)
	}

	t.Run("across projects, by memory", func(t *testing.T) {
		got := verdicts(15*gib, holders, []Waiter{
			{Ref: "p/big", Project: "p", Need: 8 * gib, Since: now.Add(-time.Minute)},
			{Ref: "r/small", Project: "r", Need: 2 * gib, Since: now},
			{Ref: "p/small", Project: "p", Need: 2 * gib, Since: now},
			{Ref: "r/more", Project: "r", Need: 2 * gib, Since: now},
		}, nil)
		// 10 used of 15: the 8 doesn't fit, the first two 2s do, the third doesn't.
		if got["p/big"] != (Verdict{Why: WaitMemory}) || !got["r/small"].Start || !got["p/small"].Start || got["r/more"].Start {
			t.Errorf("verdicts = %+v", got)
		}
	})

	t.Run("a heavy agent that has waited long enough isn't passed", func(t *testing.T) {
		got := verdicts(16*gib, holders, []Waiter{
			{Ref: "p/big", Project: "p", Need: 8 * gib, Since: now.Add(-StarveAfter)},
			{Ref: "r/small", Project: "r", Need: 2 * gib, Since: now},
		}, nil)
		if got["r/small"] != (Verdict{Why: WaitBehind, Behind: "p/big"}) {
			t.Errorf("the light agent = %+v, want it behind p/big", got["r/small"])
		}
	})

	t.Run("one agent always starts on an empty VM", func(t *testing.T) {
		got := verdicts(4*gib, nil, []Waiter{{Ref: "p/huge", Need: 8 * gib}, {Ref: "p/next", Need: gib}}, nil)
		if !got["p/huge"].Start || got["p/next"].Start {
			t.Errorf("verdicts = %+v, want the first alone", got)
		}
	})

	t.Run("slots, unless the create didn't ask to queue", func(t *testing.T) {
		free := map[string]int{"p": 1, "r": 0}
		got := verdicts(64*gib, holders, []Waiter{
			{Ref: "r/q", Project: "r", Need: gib},
			{Ref: "p/q1", Project: "p", Need: gib},
			{Ref: "p/q2", Project: "p", Need: gib},
			{Ref: "r/new", Project: "r", Need: gib, AnySlot: true},
		}, free)
		if got["r/q"].Why != WaitSlot || !got["p/q1"].Start || got["p/q2"].Why != WaitSlot || !got["r/new"].Start {
			t.Errorf("verdicts = %+v", got)
		}
	})
}

// Reproduces the live bug: an unleased heavy phase (a build with no
// `agentbox heavy`) grows past its baseline while another agent's burst
// stays within the pool under a lease, yet the pool's floor — held back
// for leases nobody takes — was charged again on top of the unleased
// growth, queuing an agent the VM plainly has the room for.
func TestAdmitUnleasedHeavyPhaseDoubleCounts(t *testing.T) {
	t.Parallel()
	now := time.Now()
	total := 16 * gib
	holders := []Holder{
		{Ref: "organic/77", Project: "organic", Reserved: 2 * gib, Using: 2 * gib},                 // idle, at its baseline
		{Ref: "organic/78", Project: "organic", Reserved: 2 * gib, Lease: 4 * gib, Using: 6 * gib}, // a browser test, using its whole lease
		{Ref: "t3code/01", Project: "t3code", Reserved: gib, Using: 11 * gib},                      // an AppImage build with no `agentbox heavy`
	}
	waiting := []Waiter{{Ref: "t3code/new", Project: "t3code", Need: gib, Since: now}}

	// Before the fix: capacity is total less the floor, however little of it
	// is actually leased out.
	capacityOld := total - PoolFloor(total)
	if got := Admit(capacityOld, holders, waiting, nil, now, 0)["t3code/new"]; got.Start {
		t.Fatal("started on the old capacity; the scenario should already be tight, or it proves nothing")
	}

	// After the fix: the unleased build's growth past its baseline already
	// draws from the floor meant for leases, so Capacity gives the unused
	// rest of it back.
	capacity := Capacity(total, holders)
	if got := Admit(capacity, holders, waiting, nil, now, 0)["t3code/new"]; !got.Start {
		t.Errorf("verdict = %+v, capacity = %d; want it to start, the VM has the room", got, capacity)
	}
}

// Admit's sanity bound: whatever the bookkeeping says, a waiter starts when
// the VM's real spare memory comfortably covers it, and still waits when it
// doesn't quite.
func TestAdmitComfortableMargin(t *testing.T) {
	t.Parallel()
	now := time.Now()
	holders := []Holder{{Ref: "p/a", Project: "p", Reserved: 4 * gib, Using: 4 * gib}}
	waiting := []Waiter{{Ref: "p/new", Project: "p", Need: 2 * gib, Since: now}}

	if got := Admit(4*gib, holders, waiting, nil, now, 0)["p/new"]; got.Start {
		t.Fatal("started with no capacity and no real memory to fall back on")
	}
	if got := Admit(4*gib, holders, waiting, nil, now, 2*gib+ComfortableMargin)["p/new"]; !got.Start {
		t.Errorf("verdict = %+v, want it to start: the VM has comfortably more free than it needs", got)
	}
	if got := Admit(4*gib, holders, waiting, nil, now, 2*gib+ComfortableMargin-1)["p/new"]; got.Start {
		t.Error("started a hair under the comfortable margin")
	}
}

func TestWaitMessage(t *testing.T) {
	t.Parallel()
	holders := []Holder{{Project: "p", Reserved: 4 * gib}, {Project: "r", Reserved: 4 * gib, Using: 7 * gib}}
	if got, want := WaitMessage(Verdict{Why: WaitMemory}, 18*gib, 8*gib, holders, 0), "queued: 2 agents in 2 projects reserve 11 of 18 GB; starts when ~8 GB is free"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := WaitMessage(Verdict{Why: WaitSlot}, 0, 0, nil, 1), "queued: its project's 1 slot is taken; starts when one is free"; got != want {
		t.Errorf("got %q", got)
	}
}

func TestMemoryHighs(t *testing.T) {
	t.Parallel()
	holders := []Holder{
		{Ref: "fat", Reserved: 4 * gib, Using: 9 * gib},
		{Ref: "grown", Reserved: 4 * gib, Using: 6 * gib},
		{Ref: "lean", Reserved: 4 * gib, Using: 2 * gib},
	}
	// Room to spare: no limits.
	for ref, high := range MemoryHighs(32*gib, holders) {
		if high != 0 {
			t.Errorf("with room, %s's memory.high = %d", ref, high)
		}
	}
	// 19 counted of 20: short of the 2 GiB slack by 1, taken from the
	// furthest over first, nobody under reservation plus headroom.
	highs := MemoryHighs(20*gib, holders)
	if highs["fat"] >= 9*gib {
		t.Errorf("the agent furthest over isn't held below what it uses: %d", highs["fat"])
	}
	if highs["grown"] < 6*gib {
		t.Errorf("the agent less far over gave memory up before the one furthest over: %d", highs["grown"])
	}
	for ref, high := range highs {
		if high < 4*gib+HighHeadroom(4*gib) {
			t.Errorf("%s's memory.high %d is below its reservation and headroom", ref, high)
		}
	}
	// Far past the cap, the furthest over is held at its floor.
	if got := MemoryHighs(14*gib, holders)["fat"]; got != 4*gib+HighHeadroom(4*gib) {
		t.Errorf("with the VM over its cap, the fattest agent's memory.high = %d, want its floor", got)
	}
	// A lease is held like a reservation: a bursting agent isn't tightened
	// below its baseline and lease.
	leased := []Holder{{Ref: "burst", Reserved: gib, Lease: 3 * gib, Using: 4 * gib}, {Ref: "fat", Reserved: gib, Using: 6 * gib}}
	highs = MemoryHighs(10*gib, leased)
	if highs["burst"] < 4*gib+HighHeadroom(4*gib) {
		t.Errorf("the leased agent's memory.high = %d, below its lease", highs["burst"])
	}
	if highs["fat"] >= 6*gib {
		t.Errorf("the agent over its baseline with no lease isn't tightened: %d", highs["fat"])
	}
}

func TestSetMemoryHigh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.m")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "memory.high")
	if err := os.WriteFile(file, []byte("max\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var writes []string
	write := func(path, value string) error {
		writes = append(writes, value)
		return os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
	if err := setMemoryHigh(root, "m", 0, write); err != nil || len(writes) != 0 {
		t.Errorf("max over max wrote %v, %v", writes, err)
	}
	if err := setMemoryHigh(root, "m", 5*gib+1, write); err != nil || len(writes) != 1 || writes[0] != "5368709120" {
		t.Errorf("writes = %v, %v; want 5 GiB, page-aligned", writes, err)
	}
	if err := setMemoryHigh(root, "m", 5*gib, write); err != nil || len(writes) != 1 {
		t.Errorf("an unchanged value was written again: %v, %v", writes, err)
	}
	if err := setMemoryHigh(root, "m", 0, write); err != nil || writes[len(writes)-1] != "max" {
		t.Errorf("lifting it wrote %v, %v", writes, err)
	}
	if err := setMemoryHigh(root, "gone", gib, write); err != nil {
		t.Errorf("a machine with no cgroup: %v", err)
	}
}
