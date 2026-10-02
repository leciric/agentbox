package agent

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Admission: whether a new agent's machine may start now, across every
// project. Slots (slots.go) say how many of one project's agents run at once;
// admission says whether the VM has the memory for one more, whoever asked for
// it: a project's chat, the app or the command line, queued or not. An agent
// that doesn't fit waits in its project's queue, whatever the create asked
// for, and starts when it does.
//
// Each agent has a size, which sets what it reserves of the VM's memory while
// it runs (Reservation). A running agent counts as the larger of what it
// reserved and what it uses now, so one that grew past its size counts what
// it grew to. Nothing running is ever paused or stopped to make room.
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

// What the fixed sizes reserve.
const (
	LightReservation = int64(2) << 30
	HeavyReservation = int64(8) << 30
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

// Reservation is what an agent of that size reserves of the VM's memory:
// about 2 GB light, about 8 GB heavy, and its project's learned peak (peak,
// ProjectPeak) normal or auto.
func Reservation(size string, peak int64) int64 {
	switch size {
	case SizeLight:
		return LightReservation
	case SizeHeavy:
		return HeavyReservation
	}
	return max(peak, 1)
}

// StarveAfter is how long an agent at the front of the wait may be passed by
// smaller ones that fit before it, before nothing passes it any more.
const StarveAfter = 10 * time.Minute

// Holder is an agent whose machine holds memory: running, paused, or being
// made.
type Holder struct {
	Ref, Project string
	Reserved     int64 // what its size reserves
	Using        int64 // what it was last seen using; 0 when not yet sampled
}

// Counts is what the agent counts for: what it reserved, or more once it
// grew past that.
func (h Holder) Counts() int64 { return max(h.Reserved, h.Using) }

// Waiter is an agent that wants to start.
type Waiter struct {
	Ref, Project string
	Need         int64     // its reservation
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

// Admit decides which waiters start now, given capacity, the memory agents
// may share, what the holders count for, and each project's free slots (nil
// when slots don't apply: the queue is off). waiting must be in the order
// the waiters joined. An agent that fits starts; so does one that doesn't
// when nothing at all holds memory, or it would never start. One waiting for
// a slot isn't waiting for memory, so it holds nobody up.
func Admit(capacity int64, holders []Holder, waiting []Waiter, free map[string]int, now time.Time) map[string]Verdict {
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
		if used == 0 || used+w.Need <= capacity {
			used += w.Need
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

// Used is what holders count for, together.
func Used(holders []Holder) int64 {
	var used int64
	for _, h := range holders {
		used += h.Counts()
	}
	return used
}

// WaitMessage says, in one line, why an agent that needs need waits, for its
// verdict: "queued: 6 agents in 2 projects reserve 15 of 18 GB; starts when
// ~7 GB is free".
func WaitMessage(v Verdict, capacity, need int64, holders []Holder, slots int) string {
	switch v.Why {
	case WaitSlot:
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
// A size is a reservation, not a cap: an agent may use more while the VM has
// memory nobody reserved, and nothing is ever OOM-killed for going over its
// size (no memory.max). Only as the VM nears its capacity does the daemon set
// memory.high on the agents that went over, furthest over first, down toward
// their reservation and some headroom: the kernel then reclaims from and slows
// that agent alone, rather than the whole VM running short.

// HighHeadroom is what memory.high leaves above an agent's reservation when
// it is tightened: a quarter of it, and at least 512 MiB.
func HighHeadroom(reserved int64) int64 { return max(reserved/4, int64(512)<<20) }

// highSlack is how much of capacity must be free, beyond what agents reserve
// or use, for every agent to be left unlimited: a tenth, and at least 1 GiB.
func highSlack(capacity int64) int64 { return max(capacity/10, int64(1)<<30) }

// MemoryHighs works out each holder's memory.high, by Ref: 0 for none
// ("max"). While at least highSlack of capacity is free, nobody gets one.
// Short of that, every agent may still grow into an even share of what is
// free, and the shortfall is taken from those furthest over their
// reservation first, never below reservation plus HighHeadroom.
func MemoryHighs(capacity int64, holders []Holder) map[string]int64 {
	out := make(map[string]int64, len(holders))
	free := capacity - Used(holders)
	slack := highSlack(capacity)
	if free >= slack || len(holders) == 0 {
		for _, h := range holders {
			out[h.Ref] = 0
		}
		return out
	}
	share := max(free, 0) / int64(len(holders))
	for _, h := range holders {
		out[h.Ref] = max(h.Counts()+share, h.Reserved+HighHeadroom(h.Reserved))
	}
	short := slack - free
	over := append([]Holder(nil), holders...)
	sort.SliceStable(over, func(i, j int) bool {
		return over[i].Using-over[i].Reserved > over[j].Using-over[j].Reserved
	})
	for _, h := range over {
		if short <= 0 {
			break
		}
		floor := h.Reserved + HighHeadroom(h.Reserved)
		give := min(short, h.Using-floor)
		if give <= 0 {
			continue
		}
		out[h.Ref] = h.Using - give
		short -= give
	}
	return out
}
