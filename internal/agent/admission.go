package agent

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// Admission: whether a new agent's machine may start now, across every
// project. Slots (slots.go) say how many of one project's agents run at once;
// admission says whether the VM has the memory for one more, whoever asked for
// it: a project's chat, the app or the command line, queued or not. An agent
// that doesn't fit waits in its project's queue, whatever the create asked
// for, and starts when it does.
//
// What an agent holds has two parts. Its baseline is what it uses writing
// code, the most of the time: admission counts only that. Its burst is what a
// heavy phase adds on top (a test run, a build, the browser, a recording),
// which it takes from the VM's burst pool as a lease (BurstPool) for as long
// as the phase lasts. Both are learned per project (Shape); the agent's size
// sets its burst. A running agent counts as the larger of its baseline and
// what it uses beyond its lease, so one that grew counts what it grew to.
// Nothing running is ever paused or stopped to make room.
//
// Waiting agents are looked at in the order they joined. One that doesn't fit
// lets smaller ones behind it start into what's free, but only for so long
// (StarveAfter): once it has waited that long, nothing behind it starts until
// it has.

// Agent sizes.
const (
	SizeAuto   = "auto"
	SizeLight  = "light"
	SizeNormal = "normal"
	SizeHeavy  = "heavy"
)

// What the fixed sizes reach in a heavy phase, baseline and burst together.
const (
	LightTotal = int64(2) << 30
	HeavyTotal = int64(8) << 30
)

// CheckSize checks an agent's size and gives it as it is stored: "" for auto,
// which is what normal is too until something decides otherwise.
func CheckSize(size string) (string, error) {
	switch size {
	case "", SizeAuto:
		return "", nil
	case SizeLight, SizeNormal, SizeHeavy:
		return size, nil
	}
	return "", fmt.Errorf("unknown size %q: use auto, light, normal or heavy", size)
}

// Shape is what a project's agents use: their baseline, writing code, and
// their burst, what a heavy phase adds to it.
type Shape struct {
	Baseline, Burst int64
}

// What a project nothing is known about is taken to use, until its agents
// are seen: together, the 4 GiB its peak was taken to be.
const (
	defaultBaseline = int64(1) << 30
	defaultBurst    = int64(3) << 30
	minBurst        = int64(512) << 20
)

// learnedShape fills in what wasn't learned yet with the defaults.
func learnedShape(baseline, burst int64) Shape {
	if baseline <= 0 {
		baseline = defaultBaseline
	}
	if burst <= 0 {
		burst = defaultBurst
	}
	return Shape{Baseline: baseline, Burst: max(burst, minBurst)}
}

// Burst is what an agent of that size takes from the burst pool in a heavy
// phase: enough for about 2 GB in all light, about 8 GB heavy (more if the
// project's heavy phases were seen taking more), and the project's learned
// burst normal or auto.
func Burst(size string, shape Shape) int64 {
	switch size {
	case SizeLight:
		return max(LightTotal-shape.Baseline, minBurst)
	case SizeHeavy:
		return max(HeavyTotal-shape.Baseline, shape.Burst)
	}
	return max(shape.Burst, minBurst)
}

// PoolFloor is what admission leaves of capacity for heavy phases: a quarter,
// and at least 1 GiB. Baselines fill the rest.
func PoolFloor(capacity int64) int64 { return min(max(capacity/4, int64(1)<<30), capacity) }

// Capacity is what capacity is, to compare Used against: total less the
// burst pool's floor, except for whatever of the floor nobody actually
// leases. The floor is withheld, a quarter of total at most, to leave every
// lease the pool might grant room beside the baselines Used already counts;
// when what's actually leased falls short of it, the rest sits idle,
// withheld for nobody. Meanwhile a heavy phase with no lease (a build, a
// browser run started with no `agentbox heavy`) draws on no pool at all: its
// Counts is already its full Using, charged against capacity on its own.
// Shrinking capacity by the whole floor regardless charges that growth
// against room reserved for leases it never took — the same memory, twice.
func Capacity(total int64, holders []Holder) int64 {
	var leased int64
	for _, h := range holders {
		leased += max(0, h.Lease)
	}
	return total - max(PoolFloor(total)-leased, 0)
}

// StarveAfter is how long an agent at the front of the wait may be passed by
// smaller ones that fit before it, before nothing passes it any more.
const StarveAfter = 10 * time.Minute

// Holder is an agent whose machine holds memory: running, paused, or being
// made.
type Holder struct {
	Ref, Project string
	Reserved     int64 // its baseline
	Lease        int64 // what it holds of the burst pool now, if anything
	Using        int64 // what it was last seen using; 0 when not yet sampled
}

// Counts is what the agent counts for in admission: its baseline, or more
// once it grew past that beyond what its lease covers.
func (h Holder) Counts() int64 { return max(h.Reserved, h.Using-h.Lease) }

// Holds is what the agent holds of the VM's memory, lease and all.
func (h Holder) Holds() int64 { return max(h.Reserved+h.Lease, h.Using) }

// Waiter is an agent that wants to start.
type Waiter struct {
	Ref, Project string
	Need         int64     // its baseline
	Since        time.Time // when it joined the wait
	// AnySlot lets it start whatever its project's slots say: a create that
	// didn't ask to queue. Memory still has to fit.
	AnySlot bool
}

// Verdict is what admission says about one waiter.
type Verdict struct {
	Start bool
	// Why it waits, when it does: WaitMemory, WaitBehind or WaitSlot.
	Why string
	// Behind is the waiter it waits behind, for WaitBehind.
	Behind string
}

// Why a waiter waits.
const (
	WaitMemory = "memory" // it doesn't fit in what's free
	WaitBehind = "behind" // it would fit, but the front of the wait has waited long enough
	WaitSlot   = "slot"   // its project's slots are full
)

// ComfortableMargin is how much of the VM's real MemAvailable must be left
// beyond a waiter's need for Admit's sanity bound to override its own
// bookkeeping: the same floor the VM is kept grown to ahead of use (the
// host's CHV supervisor's memHeadroomMin), so admitting by this bound never
// leaves the guest with less free than it would keep itself anyway.
const ComfortableMargin = int64(2) << 30

// Admit decides which waiters start now, given capacity, the memory agents
// may share, what the holders count for, and each project's free slots (nil
// when slots don't apply: the queue is off). waiting must be in the order
// the waiters joined. An agent that fits starts; so does one that doesn't
// when nothing at all holds memory, or it would never start, or the VM's
// real available memory comfortably fits it whatever the bookkeeping above
// says (available 0 or less skips this: it couldn't be read). One waiting
// for a slot isn't waiting for memory, so it holds nobody up.
func Admit(capacity int64, holders []Holder, waiting []Waiter, free map[string]int, now time.Time, available int64) map[string]Verdict {
	used := Used(holders)
	out := make(map[string]Verdict, len(waiting))
	var front *Waiter // the first waiter that didn't fit
	for i := range waiting {
		w := &waiting[i]
		slotted := free == nil || w.AnySlot
		switch {
		case !slotted && free[w.Project] <= 0:
			out[w.Ref] = Verdict{Why: WaitSlot}
			continue
		case front != nil && now.Sub(front.Since) >= StarveAfter:
			out[w.Ref] = Verdict{Why: WaitBehind, Behind: front.Ref}
			continue
		}
		comfortable := available > 0 && available-w.Need >= ComfortableMargin
		if used == 0 || used+w.Need <= capacity || comfortable {
			used += w.Need
			available -= w.Need
			if !slotted {
				free[w.Project]--
			}
			out[w.Ref] = Verdict{Start: true}
			continue
		}
		out[w.Ref] = Verdict{Why: WaitMemory}
		if front == nil {
			front = w
		}
	}
	return out
}

// Used is what holders count for in admission, together.
func Used(holders []Holder) int64 {
	var used int64
	for _, h := range holders {
		used += h.Counts()
	}
	return used
}

// WaitMessage says, in one line, why an agent that needs need waits, for its
// verdict, slots its project's number of them and pinned whether the user
// fixed that number: "queued: 6 agents in 2 projects reserve 15 of 18 GB; starts when
// ~7 GB is free".
func WaitMessage(v Verdict, capacity, need int64, holders []Holder, slots int, pinned bool) string {
	switch v.Why {
	case WaitSlot:
		if pinned {
			return fmt.Sprintf("queued: its project runs at most %d %s at once (its Agents at once setting); starts when one stops", slots, plural(slots, "agent", "agents"))
		}
		return fmt.Sprintf("queued: its project's %d %s taken; starts when one is free", slots, plural(slots, "slot is", "slots are"))
	case WaitBehind:
		return fmt.Sprintf("queued: %s has waited longest for memory and starts first; this one starts after it", v.Behind)
	}
	projects := map[string]bool{}
	for _, h := range holders {
		projects[h.Project] = true
	}
	return fmt.Sprintf("queued: %d %s in %d %s reserve %s of %s GB; starts when ~%s GB is free",
		len(holders), plural(len(holders), "agent", "agents"), len(projects), plural(len(projects), "project", "projects"),
		GB(Used(holders)), GB(capacity), GB(need))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// GB is bytes as whole gigabytes (GiB), for a line of text.
func GB(n int64) string {
	return fmt.Sprintf("%d", int64(math.Round(float64(n)/(1<<30))))
}

// memory.high
//
// Neither a baseline nor a burst is a cap: an agent may use more while the VM
// has memory nobody holds, and nothing is ever OOM-killed for going over (no
// memory.max). Only as the VM nears its capacity does the daemon set
// memory.high on the agents that went over what they hold, furthest over
// first, down toward their baseline and lease and some headroom: the kernel
// then reclaims from and slows that agent alone, rather than the whole VM
// running short.

// HighHeadroom is what memory.high leaves above what an agent holds when it is
// tightened: a quarter of it, and at least 512 MiB.
func HighHeadroom(held int64) int64 { return max(held/4, int64(512)<<20) }

// highSlack is how much of capacity must be free, beyond what agents hold or
// use, for every agent to be left unlimited: a tenth, and at least 1 GiB.
func highSlack(capacity int64) int64 { return max(capacity/10, int64(1)<<30) }

// held is what memory.high is tightened toward: baseline and lease.
func (h Holder) held() int64 { return h.Reserved + h.Lease }

// MemoryHighs works out each holder's memory.high, by Ref: 0 for none
// ("max"). While at least highSlack of capacity is free, nobody gets one.
// Short of that, every agent may still grow into an even share of what is
// free, and the shortfall is taken from those furthest over what they hold
// first, never below that plus HighHeadroom.
func MemoryHighs(capacity int64, holders []Holder) map[string]int64 {
	out := make(map[string]int64, len(holders))
	var used int64
	for _, h := range holders {
		used += h.Holds()
	}
	free := capacity - used
	slack := highSlack(capacity)
	if free >= slack || len(holders) == 0 {
		for _, h := range holders {
			out[h.Ref] = 0
		}
		return out
	}
	share := max(free, 0) / int64(len(holders))
	for _, h := range holders {
		out[h.Ref] = max(h.Holds()+share, h.held()+HighHeadroom(h.held()))
	}
	short := slack - free
	over := append([]Holder(nil), holders...)
	sort.SliceStable(over, func(i, j int) bool {
		return over[i].Using-over[i].held() > over[j].Using-over[j].held()
	})
	for _, h := range over {
		if short <= 0 {
			break
		}
		floor := h.held() + HighHeadroom(h.held())
		give := min(short, h.Using-floor)
		if give <= 0 {
			continue
		}
		out[h.Ref] = h.Using - give
		short -= give
	}
	return out
}

// The burst pool
//
// A heavy phase takes its agent's burst from the pool as a lease, and waits
// for one when the pool has none to give (internal/daemon/burst.go). The pool
// is whatever of capacity no agent holds: admission leaves at least PoolFloor
// of it, and a lease is given when its burst fits in what is free now, or
// when no other agent holds one, so a burst bigger than the pool still runs,
// alone.

// BurstFits says whether a lease of burst may be given now, among holders
// (the asking agent's own Lease 0).
func BurstFits(capacity int64, holders []Holder, burst int64) bool {
	var used int64
	leased := false
	for _, h := range holders {
		used += h.Holds()
		leased = leased || h.Lease > 0
	}
	return !leased || used+burst <= capacity
}

// BurstFree is what the pool has free now, for saying why a lease waits.
func BurstFree(capacity int64, holders []Holder) (free, leased int64, agents int) {
	var used int64
	for _, h := range holders {
		used += h.Holds()
		if h.Lease > 0 {
			leased += h.Lease
			agents++
		}
	}
	return max(capacity-used, 0), leased, agents
}

// workerMemory is what one test worker is given room for under a lease.
const workerMemory = int64(768) << 20

// BurstEnv is the environment a heavy phase runs with: its test runners'
// parallelism held to what its lease has room for (a worker per 768 MiB, no
// more than cpus), so go test, vitest and cargo don't start a worker per core
// on a lease sized for a few.
func BurstEnv(lease int64, cpus int) map[string]string {
	n := int(max(lease/workerMemory, 1))
	n = max(min(n, cpus), 1)
	v := strconv.Itoa(n)
	return map[string]string{
		"GOFLAGS":            "-p=" + v,
		"VITEST_MAX_THREADS": v,
		"VITEST_MAX_FORKS":   v,
		"CARGO_BUILD_JOBS":   v,
	}
}
