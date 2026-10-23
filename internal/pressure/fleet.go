package pressure

import (
	"sort"
	"time"
)

// The fleet: the governor's rules applied to whole agents' machines rather
// than to their heavy commands, behind Settings → Experimental ("Pause whole
// agents for memory"). Off, it isn't used and the Governor alone holds runs.
//
// With many agents across projects the VM ran out of memory and every one of
// them crawled; pausing only heavy commands left the rest to grow: a dev
// server, a language server, a browser, the AI tool itself. So once memory
// pressure holds (PSI over Freeze for FreezeAfter, or the headroom Short or
// Out, as Judge says), the newest machine is frozen whole (its cgroup, its
// memory pushed out to zram by the daemon), then the next newest, and they
// thaw, oldest first, once it has stayed calm. The oldest working agent is
// never frozen, so one always makes progress: an agent waiting on the model
// is working, and so is one with a heavy command going.
//
// Starting a machine goes by what the VM really has: one starts whenever
// what is available, with what the VM may still grow by, exceeds what a boot
// takes (BootCost, measured from machines that booted) plus the floor. No
// counts, no reservations, no estimates of what an agent will come to use:
// on 2026-10-22 three agents each estimated at 6 GB held new ones back with
// 8 GB free. While a machine is frozen nothing new starts: it thaws first.
// The cost of a boot is held against what is available only until the boot
// shows in it (BootSettle), so ten asking at once don't all fit the same
// gigabyte.

// Machine is a running agent's machine as the fleet sees it.
type Machine struct {
	Agent string // the agent's ref
	// Since is when the agent was made: oldest first, newest frozen first.
	Since time.Time
	// Working: a turn of its chat runs (waiting on the model included), or
	// a heavy command of its.
	Working bool
	// Frozen is whether the fleet froze it; FrozenAt when, Why why.
	Frozen   bool
	FrozenAt time.Time
	Why      Reason
}

// Waiter is a machine asking to start.
type Waiter struct {
	Agent string
	Asked time.Time
}

// FleetActions is what a fleet step decided, by agent ref.
type FleetActions struct {
	Freeze, Thaw, Start []string
}

// Empty reports whether there is nothing to apply.
func (a FleetActions) Empty() bool { return len(a.Freeze)+len(a.Thaw)+len(a.Start) == 0 }

// Boot costs: what the fleet holds against available memory for a machine
// starting, until it shows (BootSettle).
const (
	DefaultBootCost = int64(5) << 28 // 1.25 GiB
	MinBootCost     = int64(1) << 29 // 512 MiB
	MaxBootCost     = int64(3) << 30
	BootSettle      = 45 * time.Second
)

// Fleet keeps the machines and those asking to start. It isn't safe for
// concurrent use.
type Fleet struct {
	Policy Policy
	// BootCost is what a machine's boot takes, measured; DefaultBootCost
	// until it is.
	BootCost int64

	machines  []*Machine // oldest first
	waiting   []*Waiter  // in the order they asked
	booting   []booting
	highSince time.Time
	calmSince time.Time
	changed   time.Time // the last freeze or thaw
	admitted  time.Time // the last start
	last      Sample
	trend     Trend
	why       Reason
	trends    trends
}

type booting struct {
	agent string
	until time.Time
}

// NewFleet is a fleet with policy.
func NewFleet(policy Policy) *Fleet { return &Fleet{Policy: policy, BootCost: DefaultBootCost} }

// Sync is the machines running now, frozen by the fleet or not, and whether
// each works. A machine the fleet froze that runs again (somebody resumed it)
// is frozen no longer; one gone is forgotten.
func (f *Fleet) Sync(running []Machine) {
	known := map[string]*Machine{}
	for _, m := range f.machines {
		known[m.Agent] = m
	}
	out := make([]*Machine, 0, len(running))
	for _, r := range running {
		m := &Machine{Agent: r.Agent, Since: r.Since, Working: r.Working}
		if k := known[r.Agent]; k != nil && r.Frozen {
			m.Frozen, m.FrozenAt, m.Why = true, k.FrozenAt, k.Why
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	f.machines = out
}

// Machines are the machines, oldest first.
func (f *Fleet) Machines() []Machine {
	out := make([]Machine, len(f.machines))
	for i, m := range f.machines {
		out[i] = *m
	}
	return out
}

// Frozen reports whether the fleet froze agent.
func (f *Fleet) Frozen(agent string) bool {
	m := f.find(agent)
	return m != nil && m.Frozen
}

func (f *Fleet) find(agent string) *Machine {
	for _, m := range f.machines {
		if m.Agent == agent {
			return m
		}
	}
	return nil
}

// Ask is a machine asking to start: it reports whether it may now (it
// started, or nothing holds it). One that waits starts in a later Step.
func (f *Fleet) Ask(agent string, now time.Time) bool {
	for _, b := range f.booting {
		if b.agent == agent {
			return true
		}
	}
	for _, w := range f.waiting {
		if w.Agent == agent {
			return false
		}
	}
	f.waiting = append(f.waiting, &Waiter{Agent: agent, Asked: now})
	return false
}

// Started reports whether agent was let start since it asked.
func (f *Fleet) Started(agent string) bool {
	for _, b := range f.booting {
		if b.agent == agent {
			return true
		}
	}
	return false
}

// Cancel takes a waiter out, as its asker gives up.
func (f *Fleet) Cancel(agent string) {
	for i, w := range f.waiting {
		if w.Agent == agent {
			f.waiting = append(f.waiting[:i], f.waiting[i+1:]...)
			return
		}
	}
}

// Waiting are the machines asking to start, in order.
func (f *Fleet) Waiting() []Waiter {
	out := make([]Waiter, len(f.waiting))
	for i, w := range f.waiting {
		out[i] = *w
	}
	return out
}

// Ahead is, for a frozen machine, how many frozen ones thaw before it, and
// for one waiting to start, how many start before it (frozen ones thaw
// first). ok is false for a machine neither frozen nor waiting.
func (f *Fleet) Ahead(agent string) (n int, frozen, ok bool) {
	for _, m := range f.machines {
		if m.Agent == agent {
			return n, true, m.Frozen
		}
		if m.Frozen {
			n++
		}
	}
	n = f.frozen()
	for _, w := range f.waiting {
		if w.Agent == agent {
			return n, false, true
		}
		n++
	}
	return 0, false, false
}

// Last is the sample the last step saw; Trend how memory was moving; Why
// why machines were held then, "" for nothing.
func (f *Fleet) Last() Sample { return f.last }
func (f *Fleet) Trend() Trend { return f.trend }
func (f *Fleet) Why() Reason  { return f.why }
func (f *Fleet) frozen() int  { return count(f.machines, func(m *Machine) bool { return m.Frozen }) }
func (f *Fleet) working() bool {
	return count(f.machines, func(m *Machine) bool { return m.Working }) > 0
}

func count(ms []*Machine, yes func(*Machine) bool) int {
	n := 0
	for _, m := range ms {
		if yes(m) {
			n++
		}
	}
	return n
}

// Fits reports whether a machine's boot fits in m now: what is available,
// with what the VM may still grow by, less the boots not showing yet, over
// BootCost plus the floor.
func (f *Fleet) Fits(m Mem, now time.Time) bool {
	if !m.Known() {
		return true
	}
	room := float64(m.Available)
	if m.Cap > m.Total {
		room += float64(m.Cap - m.Total)
	}
	for _, b := range f.booting {
		if now.Before(b.until) {
			room -= float64(f.BootCost)
		}
	}
	return room-float64(f.BootCost) >= f.Policy.floor(m)
}

// protected is the machine never frozen: the oldest working one, frozen or
// not, or nil when none works.
func (f *Fleet) protected() *Machine {
	for _, m := range f.machines {
		if m.Working {
			return m
		}
	}
	return nil
}

// newestFreezable is the newest machine not frozen and not the protected
// one, or nil.
func (f *Fleet) newestFreezable() *Machine {
	keep := f.protected()
	for i := len(f.machines) - 1; i >= 0; i-- {
		if m := f.machines[i]; !m.Frozen && m != keep {
			return m
		}
	}
	return nil
}

// Step decides, from the sample now, what freezes, thaws and starts.
func (f *Fleet) Step(now time.Time, s Sample) FleetActions {
	f.last = s
	p := f.Policy
	psi := s.PSI
	tr := f.trends.observe(p, now, s)
	f.trend = tr
	room, why := f.judge(s.Mem, tr)
	high := psi.Full.Avg10 >= p.Freeze
	calm := psi.Full.Avg10 < p.Calm && psi.Some.Avg10 < p.Admit && p.roomy(s.Mem, tr)
	if !high {
		f.highSince = time.Time{}
	} else if f.highSince.IsZero() {
		f.highSince = now
	}
	if !calm {
		f.calmSince = time.Time{}
	} else if f.calmSince.IsZero() {
		f.calmSince = now
	}
	kept := f.booting[:0]
	for _, b := range f.booting {
		if now.Before(b.until) {
			kept = append(kept, b)
		}
	}
	f.booting = kept
	switch {
	case room != Room:
		f.why = why
	case high || psi.Some.Avg10 >= p.Admit:
		f.why = Stalling
	default:
		f.why = ""
	}
	var out FleetActions
	freeze := func(m *Machine, why Reason) {
		m.Frozen, m.FrozenAt, m.Why = true, now, why
		f.changed = now
		out.Freeze = append(out.Freeze, m.Agent)
	}
	thaw := func(m *Machine) {
		m.Frozen = false
		f.changed = now
		out.Thaw = append(out.Thaw, m.Agent)
	}

	// The oldest working machine always runs: frozen while it was idle, or
	// once an older one stopped working, it thaws now, whatever the pressure.
	if keep := f.protected(); keep != nil && keep.Frozen {
		thaw(keep)
	}
	frozen := f.frozen()
	switch {
	case !f.working() && len(f.booting) == 0 && len(f.waiting) > 0:
		// Nothing works: the first waiter starts, whatever the pressure, or
		// a VM full of idle machines would never start another.
		out.Start = append(out.Start, f.start(now))
	case room == Out:
		for m := f.newestFreezable(); m != nil; m = f.newestFreezable() {
			freeze(m, why)
		}
	case room == Short && now.Sub(f.changed) >= p.HeadroomStep:
		if m := f.newestFreezable(); m != nil {
			freeze(m, why)
		}
	case high && now.Sub(f.highSince) >= p.FreezeAfter && now.Sub(f.changed) >= p.Step:
		if m := f.newestFreezable(); m != nil {
			freeze(m, Stalling)
		}
	case calm && frozen > 0 && now.Sub(f.calmSince) >= p.CalmAfter && now.Sub(f.changed) >= p.Step:
		for _, m := range f.machines {
			if m.Frozen {
				thaw(m)
				break
			}
		}
	case frozen == 0 && len(f.waiting) > 0 && psi.Some.Avg10 < p.Admit && room == Room &&
		now.Sub(f.admitted) >= p.AdmitStep && f.Fits(s.Mem, now):
		out.Start = append(out.Start, f.start(now))
	}
	if f.why == "" && f.frozen() > 0 {
		f.why = Easing
	}
	if f.why == "" && len(f.waiting) > 0 && !f.Fits(s.Mem, now) {
		f.why = Low
	}
	return out
}

// judge is Policy.Judge, and Short sooner: under the floor with swap at
// SwapBusy, rather than full. Judge waits for full swap, as with room in it
// the kernel makes more; but swap fills only as fast as its device takes
// it, and what a machine still growing doesn't fit, it is killed for, the
// oldest working one first among them as it is the biggest. In the fleet lab
// on 2026-10-22, six machines growing 5 MiB/s each in 2 GiB with 1 GiB of
// swap had 92 KiB available at 71% swap, Room to Judge, and were Out only
// at 95%: freezing five then left the oldest no room, and the kernel killed
// it. Freezing a machine stops all its growth, and what it holds goes out to
// swap and stays out, so it is worth doing before swap is full.
func (f *Fleet) judge(m Mem, tr Trend) (Headroom, Reason) {
	h, why := f.Policy.Judge(m, tr)
	if h >= Short || !m.AtCap() {
		return h, why
	}
	if float64(m.Available) < f.Policy.floor(m) && m.SwapUsed() >= f.Policy.SwapBusy {
		return Short, Low
	}
	return h, why
}

func (f *Fleet) start(now time.Time) string {
	w := f.waiting[0]
	f.waiting = f.waiting[1:]
	f.booting = append(f.booting, booting{agent: w.Agent, until: now.Add(BootSettle)})
	f.admitted = now
	return w.Agent
}

// MedianBootCost is the boot cost a fleet goes by from the boots measured,
// held within MinBootCost and MaxBootCost; DefaultBootCost for none.
func MedianBootCost(measured []int64) int64 {
	if len(measured) == 0 {
		return DefaultBootCost
	}
	s := append([]int64(nil), measured...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return min(max(s[len(s)/2], MinBootCost), MaxBootCost)
}
