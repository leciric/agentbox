package pressure

import (
	"sort"
	"time"
)

// Admission staggers agents' machines as they boot. A machine boots when it
// is asked to, never held for the VM's memory, nor for builds, nor for the
// user's computer being busy or under its reserve (19 Nov 2026, the user's
// decision): the kernel's limits are what protect the VM, each agent's
// memory.max and every agent's together under agentbox.agents (package
// agent), and the VM's cap and the host supervisor's checks are what protect
// the user's computer (package hostvm/chv). Until then machines that didn't
// fit waited On hold, in line, and on 19 Nov 2026 they waited for twenty
// minutes and more behind builds the agents' own shared limit held back,
// with the VM's memory free; the same day the host's 8.2 GiB free, under
// its 7.6 GiB reserve within two minutes, held every boot while its own
// memory pressure read 0.
//
// Two things are left. Machines asking at once boot one every BootStep,
// oldest agent first, so each boot's burst of disk and CPU isn't on top of
// the last's. And none boots while the user's computer, outside the VM, is
// below its hard floor now (HostShort: the host supervisor's hostFloor, the
// larger of 2 GiB and 6% of it), where a boot is memory its kernel would
// find by killing something, the VM first; the supervisor stops agents
// meanwhile (Shed), and machines boot again as soon as the host is over it.
type Admission struct {
	// BootStep is the least time between two boots.
	BootStep time.Duration
	// HostShort, set before each Step, says the user's computer is below its
	// hard floor now (api.HostLoad.HoldBoots).
	HostShort bool

	waiting []*AdmitWaiter
	booting []booting
	last    time.Time // the last boot
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
	WaitHost = "host" // the user's computer, outside the VM, is below its hard floor
	WaitTurn = "turn" // another machine boots just before it
)

// DefaultBootStep is the daemon's.
const DefaultBootStep = 5 * time.Second

// BootSettle is how long a machine let boot counts as booting (Booted): its
// asker, asking again within it, boots at once rather than in line again.
const BootSettle = 45 * time.Second

// booting is a machine let boot, until when it counts as booting.
type booting struct {
	agent string
	until time.Time
}

// NewAdmission is an admission booting a machine every DefaultBootStep.
func NewAdmission() *Admission {
	return &Admission{BootStep: DefaultBootStep}
}

// Ask is a machine asking to boot at now. It reports whether it may at once,
// having been let boot just before (Booted); one that waits boots in a later
// Step, which may be the very next.
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

// Booted reports whether agent was let boot since it asked, within
// BootSettle.
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

// Waits reports whether agent's machine waits to boot.
func (a *Admission) Waits(agent string) bool { return a.find(agent) >= 0 }

// Why is why the first in line waited at the last Step: WaitHost, WaitTurn,
// or "" for nothing waiting.
func (a *Admission) Why() string { return a.why }

// Holds is why a machine asking to boot at now would wait, or "" when it may
// boot.
func (a *Admission) Holds(now time.Time) string {
	switch {
	case a.HostShort:
		return WaitHost
	case !a.last.IsZero() && now.Sub(a.last) < a.BootStep:
		return WaitTurn
	}
	return ""
}

// Step returns the machines that may boot at now, at most one.
func (a *Admission) Step(now time.Time) []string {
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
	if why := a.Holds(now); why != "" {
		a.why = why
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

// SettleFall is how fast, in bytes a second, the VM's available memory may
// still be falling for what was last started to count as settled
// (Unsettled): a machine booting or a chat loading its session takes its
// memory in its first seconds, and falls slower once it has.
const SettleFall = 32 << 20 // 32 MiB/s

// Unsettled says what keeps the VM from being settled at s, with t, for one
// more piece of work a restart cut short to be carried on after the last,
// or "" when nothing does: a memory stall (some pressure at Admit, or full
// at Calm), the VM paging to its disk, the user's computer busy, and, while
// steady is asked for, its memory still falling at SettleFall or faster.
// It spaces out what a restart carries on, one after another; it holds
// nothing else back.
func (p Policy) Unsettled(s Sample, t Trend, steady bool) string {
	switch {
	case s.Some.Avg10 >= p.Admit || s.Full.Avg10 >= p.Calm:
		return "memory pressure in the VM (" + s.String() + ")"
	case s.Mem.DiskSwapOut():
		return "the VM paging out to its disk"
	case s.HostBusy:
		return "your computer being busy"
	case steady && t.Fall >= SettleFall:
		return "the VM's memory still falling"
	}
	return ""
}
