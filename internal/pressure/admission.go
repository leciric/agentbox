package pressure

import (
	"sort"
	"time"
)

// Admission decides when agents' machines boot, by what the VM really has,
// whatever else is on: the user starts as many agents as they like, and
// those that don't fit wait On hold, machine stopped, and boot by themselves,
// oldest first, as room frees. No counts and no estimates of what an agent
// will come to use (#252 dropped those: three agents estimated at 6 GB held
// new ones back with 8 GB free). An agent waiting on its model costs what it
// holds, which the reading already shows, so nothing counts agents.
//
// A machine boots when what is available, with what the VM may still grow
// by, less the boots not yet showing in it (BootSettle), exceeds what a boot
// takes (BootCost, measured from the last boots) plus the policy's floor;
// never while the VM pages to its disk swap; and never while a build waits
// or is paused for memory: the work of agents already running comes before
// a new agent's. One boots at a time, BootStep apart, so each boot shows in
// the reading before the next is judged. When no machine runs at all, the
// first in line boots whatever the reading, or nothing ever would.
//
// Unlike pressure.Fleet's queue, nobody boots for having waited long
// (Queue.MaxHeld): a machine started into memory that isn't there gets an
// agent's processes killed, which is what this is for.
type Admission struct {
	Policy   Policy
	BootCost int64
	// BootStep is the least time between two boots.
	BootStep time.Duration

	waiting []*AdmitWaiter
	booting []booting
	last    time.Time // the last boot
	mem     Mem
	why     string
}

// AdmitWaiter is a machine asking to boot.
type AdmitWaiter struct {
	Agent string
	// Made is when the agent was made: the oldest boots first.
	Made  time.Time
	Asked time.Time
}

// The reasons a machine waits to boot (Admission.Why).
const (
	WaitMemory = "memory" // the VM hasn't the memory for another boot
	WaitBuilds = "builds" // agents' builds wait or are paused for memory
	WaitTurn   = "turn"   // another machine boots just before it
)

// DefaultBootStep is the daemon's.
const DefaultBootStep = 5 * time.Second

// NewAdmission is an admission with policy.
func NewAdmission(policy Policy) *Admission {
	return &Admission{Policy: policy, BootCost: DefaultBootCost, BootStep: DefaultBootStep}
}

// Ask is a machine asking to boot at now. It reports whether it may at
// once: nothing waits ahead of it and it fits. One that waits boots in a
// later Step.
func (a *Admission) Ask(agent string, made, now time.Time) bool {
	if a.Booted(agent) {
		return true
	}
	if a.find(agent) >= 0 {
		return false
	}
	a.waiting = append(a.waiting, &AdmitWaiter{Agent: agent, Made: made, Asked: now})
	sort.SliceStable(a.waiting, func(i, j int) bool { return a.waiting[i].Made.Before(a.waiting[j].Made) })
	return false
}

// Booted reports whether agent was let boot since it asked, until its boot
// settles.
func (a *Admission) Booted(agent string) bool {
	for _, b := range a.booting {
		if b.agent == agent {
			return true
		}
	}
	return false
}

// Cancel takes a waiter out, as its asker gives up.
func (a *Admission) Cancel(agent string) {
	if i := a.find(agent); i >= 0 {
		a.waiting = append(a.waiting[:i], a.waiting[i+1:]...)
	}
}

func (a *Admission) find(agent string) int {
	for i, w := range a.waiting {
		if w.Agent == agent {
			return i
		}
	}
	return -1
}

// Waiting are the machines asking to boot, in the order they will.
func (a *Admission) Waiting() []AdmitWaiter {
	out := make([]AdmitWaiter, len(a.waiting))
	for i, w := range a.waiting {
		out[i] = *w
	}
	return out
}

// Place is how many machines boot before agent's, and whether it waits.
func (a *Admission) Place(agent string) (ahead int, ok bool) {
	i := a.find(agent)
	return max(i, 0), i >= 0
}

// Why is why the first in line waited at the last Step: WaitMemory,
// WaitBuilds, WaitTurn, or "" for nothing waiting.
func (a *Admission) Why() string { return a.why }

// Mem is the VM's memory as the last Step read it.
func (a *Admission) Mem() Mem { return a.mem }

// Fits reports whether a boot fits in m now: what is available, with what
// the VM may still grow by, less the boots not showing yet, over BootCost
// plus the floor. Unknown memory always fits.
func (a *Admission) Fits(m Mem, now time.Time) bool {
	if !m.Known() {
		return true
	}
	if m.DiskSwapOut() {
		return false
	}
	return a.room(m, now)-float64(a.BootCost) >= a.Policy.floor(m)
}

func (a *Admission) room(m Mem, now time.Time) float64 {
	room := float64(m.Available)
	if m.Cap > m.Total {
		room += float64(m.Cap - m.Total)
	}
	for _, b := range a.booting {
		if now.Before(b.until) {
			room -= float64(a.BootCost)
		}
	}
	return room
}

// Step is the VM's memory at now, with whether builds wait or are paused
// for memory and how many machines run: it returns the machines that may
// boot now, at most one.
func (a *Admission) Step(now time.Time, m Mem, buildsHeld bool, running int) []string {
	a.mem = m
	kept := a.booting[:0]
	for _, b := range a.booting {
		if now.Before(b.until) {
			kept = append(kept, b)
		}
	}
	a.booting = kept
	if len(a.waiting) == 0 {
		a.why = ""
		return nil
	}
	switch {
	case running == 0 && len(a.booting) == 0:
		// Nothing runs: the first boots whatever the reading.
	case !a.last.IsZero() && now.Sub(a.last) < a.BootStep:
		a.why = WaitTurn
		return nil
	case buildsHeld:
		a.why = WaitBuilds
		return nil
	case !a.Fits(m, now):
		a.why = WaitMemory
		return nil
	}
	w := a.waiting[0]
	a.waiting = a.waiting[1:]
	a.booting = append(a.booting, booting{agent: w.Agent, until: now.Add(BootSettle)})
	a.last = now
	a.why = ""
	if len(a.waiting) > 0 {
		a.why = WaitTurn
	}
	return []string{w.Agent}
}
