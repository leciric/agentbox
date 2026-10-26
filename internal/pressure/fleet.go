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
// Out, as Judge says), one machine is frozen whole (its cgroup, its memory
// pushed out to zram by the daemon), and, once that has had FreezeSettle to
// show, another if it still holds; they thaw, oldest first, once it has
// stayed calm. The oldest working agent is never frozen, so one always makes
// progress: an agent waiting on the model is working, and so is one with a
// heavy command going.
//
// Which one: the machine whose memory grows fastest, then one with a heavy
// command going, then the newest. On 2026-10-23, on an 18 GB VM with its
// 4.6 GB of zram full, two type checks (3.8 and 3.3 GB) ate memory at up to
// 180 MB/s; the fleet, then newest first and every machine at once when Out,
// froze six machines in 2 seconds, the idle ones among them. With swap full
// a frozen machine's memory has nowhere to go: freezing one stops it
// growing and frees nothing, so then an idle machine isn't frozen at all,
// and of a machine with a heavy command going only that command is paused
// (the governor's run freezer), its AI tool left to carry on.
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
	// Heavy: a heavy command of its runs in a cgroup of its own, so it can
	// be paused alone.
	Heavy bool
	// Memory is what the machine uses, without caches, in bytes; 0 unread.
	Memory int64
	// Frozen is whether the fleet froze it; RunsPaused whether, instead or
	// first, it paused its heavy commands. To Sync, whether its cgroup is
	// asked to freeze, and whether runs of its are paused for the fleet.
	// FrozenAt is when the first of them was done, Why why.
	Frozen     bool
	RunsPaused bool
	FrozenAt   time.Time
	Why        Reason
	// Growth is how fast Memory grows, in bytes a second, over the last
	// GrowthWindow; 0 while it holds or falls. The fleet works it out.
	Growth float64

	acted time.Time // when the fleet last froze it or paused its runs
}

// held reports whether the fleet holds the machine, whole or its runs.
func (m *Machine) held() bool { return m.Frozen || m.RunsPaused }

// Waiter is a machine asking to start.
type Waiter struct {
	Agent string
	Asked time.Time
}

// FleetActions is what a fleet step decided, by agent ref.
type FleetActions struct {
	Freeze, Thaw, Start []string
	// PauseRuns are the agents whose heavy commands to pause rather than
	// their whole machine, swap being full; ResumeRuns those to resume.
	PauseRuns, ResumeRuns []string
}

// Empty reports whether there is nothing to apply.
func (a FleetActions) Empty() bool {
	return len(a.Freeze)+len(a.Thaw)+len(a.Start)+len(a.PauseRuns)+len(a.ResumeRuns) == 0
}

const (
	// FreezeSettle is how long after freezing a machine (or pausing its
	// runs) the fleet waits before it holds another for headroom: three of
	// the daemon's 2-second readings, for what it uses and what the VM has
	// available to show that it stopped growing. Within it, a machine the
	// fleet froze that doesn't read as frozen yet is taken to be freezing.
	FreezeSettle = 6 * time.Second
	// GrowthWindow is what a machine's Growth is measured over.
	GrowthWindow = 6 * time.Second
	// Growing is the Growth at which a machine is what is eating the VM's
	// memory: frozen first, and, swap full, worth freezing at all.
	Growing = float64(8 << 20) // 8 MiB/s
)

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
	// Settle is FreezeSettle, or a test's.
	Settle time.Duration

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
	// used are each machine's readings of Memory over GrowthWindow, by ref.
	used map[string][]used
	// spared: the last step would have held a machine for memory, but,
	// swap full, none left to hold would have stopped anything growing.
	spared bool
}

type used struct {
	at    time.Time
	bytes int64
}

type booting struct {
	agent string
	until time.Time
}

// NewFleet is a fleet with policy.
func NewFleet(policy Policy) *Fleet {
	return &Fleet{Policy: policy, BootCost: DefaultBootCost, Settle: FreezeSettle, used: map[string][]used{}}
}

// Sync is the machines running now at now, frozen or with runs paused by the
// fleet or not, and whether each works, what it uses. A machine the fleet
// froze that runs again (somebody resumed it) is frozen no longer, unless it
// was frozen within FreezeSettle: what Sync is told may have been read
// before the freeze. One told of as frozen is the fleet's, whatever it
// remembers: the daemon leaves out machines paused by anybody else. One gone
// is forgotten.
func (f *Fleet) Sync(now time.Time, running []Machine) {
	known := map[string]*Machine{}
	for _, m := range f.machines {
		known[m.Agent] = m
	}
	out := make([]*Machine, 0, len(running))
	seen := map[string]bool{}
	for _, r := range running {
		m := &Machine{Agent: r.Agent, Since: r.Since, Working: r.Working, Heavy: r.Heavy, Memory: r.Memory}
		if k := known[r.Agent]; k != nil && k.held() {
			settling := now.Sub(k.acted) < f.Settle
			m.Frozen = k.Frozen && (r.Frozen || settling)
			m.RunsPaused = k.RunsPaused && (r.RunsPaused || settling)
			if m.held() {
				m.FrozenAt, m.Why, m.acted = k.FrozenAt, k.Why, k.acted
			}
		}
		// Frozen, and told of as the fleet's own though it had forgotten it
		// (it once read as resumed): the fleet's to thaw again.
		if r.Frozen && !m.Frozen {
			m.Frozen = true
			if m.FrozenAt.IsZero() {
				m.FrozenAt, m.Why = now, Low
			}
		}
		m.Growth = f.growth(now, r.Agent, r.Memory)
		seen[r.Agent] = true
		out = append(out, m)
	}
	for ref := range f.used {
		if !seen[ref] {
			delete(f.used, ref)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	f.machines = out
}

// growth keeps a machine's reading of what it uses and says how fast that
// grows over GrowthWindow, in bytes a second.
func (f *Fleet) growth(now time.Time, ref string, bytes int64) float64 {
	if f.used == nil {
		f.used = map[string][]used{}
	}
	seen := f.used[ref]
	if bytes > 0 && (len(seen) == 0 || now.Sub(seen[len(seen)-1].at) >= time.Second) {
		seen = append(seen, used{now, bytes})
	}
	for len(seen) > 1 && now.Sub(seen[1].at) >= GrowthWindow {
		seen = seen[1:]
	}
	f.used[ref] = seen
	if len(seen) < 2 {
		return 0
	}
	first, last := seen[0], seen[len(seen)-1]
	dt := last.at.Sub(first.at).Seconds()
	if dt <= 0 || last.bytes <= first.bytes {
		return 0
	}
	return float64(last.bytes-first.bytes) / dt
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

// RunsPaused reports whether the fleet paused agent's heavy commands.
func (f *Fleet) RunsPaused(agent string) bool {
	m := f.find(agent)
	return m != nil && m.RunsPaused
}

// SwapFull reports whether, as the last step saw it, swap is full (or there
// is none): what a frozen machine holds stays in memory, and freezing one
// frees nothing.
func (f *Fleet) SwapFull() bool {
	return f.last.Mem.Known() && f.last.Mem.SwapUsed() >= f.Policy.SwapFull
}

// Spared reports whether the last step left machines running that it would
// have frozen with room in swap: idle ones, which, swap full, would free
// nothing and stop nothing growing.
func (f *Fleet) Spared() bool { return f.spared }

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

// Ahead is, for a frozen machine, how many held ones thaw before it, and
// for one waiting to start, how many start before it (held ones thaw first).
// ok is false for a machine neither frozen nor waiting: one with only its
// heavy commands paused shows as those.
func (f *Fleet) Ahead(agent string) (n int, frozen, ok bool) {
	for _, m := range f.machines {
		if m.Agent == agent {
			return n, true, m.Frozen
		}
		if m.held() {
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
func (f *Fleet) frozen() int  { return count(f.machines, (*Machine).held) }
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

// toHold is the machine to hold next, and whether to pause only its heavy
// commands: the one growing fastest (Growing or more), else the newest with
// a heavy command going, else the newest; never one frozen already or the
// protected one. With swap full, freezing frees nothing and only stops
// growth: an idle machine isn't held, and of one with a heavy command going
// only its commands are paused, unless it grows still with them paused.
func (f *Fleet) toHold(swapFull bool) (m *Machine, runsOnly bool) {
	keep := f.protected()
	rank := func(m *Machine) int {
		switch {
		case m.Growth >= Growing:
			return 2
		case m.Heavy && !m.RunsPaused:
			return 1
		}
		return 0
	}
	var best *Machine
	for i := len(f.machines) - 1; i >= 0; i-- {
		c := f.machines[i]
		if c.Frozen || c == keep || (c.RunsPaused && c.Growth < Growing) {
			continue
		}
		r := rank(c)
		if swapFull && r == 0 {
			continue
		}
		if best == nil || r > rank(best) || r == 2 && rank(best) == 2 && c.Growth > best.Growth {
			best = c
		}
	}
	if best == nil {
		return nil, false
	}
	return best, swapFull && best.Heavy && !best.RunsPaused
}

// spareable reports whether a machine other than the protected one is
// neither frozen nor held: one the fleet left running.
func (f *Fleet) spareable() bool {
	keep := f.protected()
	return count(f.machines, func(m *Machine) bool { return m != keep && !m.held() }) > 0
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
	swapFull := f.SwapFull()
	// hold freezes the next machine, or pauses its heavy commands.
	hold := func(why Reason) {
		m, runsOnly := f.toHold(swapFull)
		f.spared = m == nil && swapFull && f.spareable()
		if m == nil {
			return
		}
		if !m.held() {
			m.FrozenAt, m.Why = now, why
		}
		m.acted = now
		f.changed = now
		if runsOnly {
			m.RunsPaused = true
			out.PauseRuns = append(out.PauseRuns, m.Agent)
			return
		}
		m.Frozen = true
		out.Freeze = append(out.Freeze, m.Agent)
	}
	thaw := func(m *Machine) {
		f.changed = now
		if m.Frozen {
			m.Frozen = false
			out.Thaw = append(out.Thaw, m.Agent)
		}
		if m.RunsPaused {
			m.RunsPaused = false
			out.ResumeRuns = append(out.ResumeRuns, m.Agent)
		}
	}

	// The oldest working machine always runs: frozen while it was idle, or
	// once an older one stopped working, it thaws now, whatever the pressure.
	if keep := f.protected(); keep != nil && keep.held() {
		thaw(keep)
	}
	frozen := f.frozen()
	switch {
	case !f.working() && len(f.booting) == 0 && len(f.waiting) > 0:
		// Nothing works: the first waiter starts, whatever the pressure, or
		// a VM full of idle machines would never start another.
		out.Start = append(out.Start, f.start(now))
	case room == Out || room == Short:
		// One at a time, Out too: what the VM has available shows a freeze
		// only at the next reading, and its trend later still.
		if now.Sub(f.changed) >= f.Settle {
			hold(why)
		}
	case high && now.Sub(f.highSince) >= p.FreezeAfter && now.Sub(f.changed) >= p.Step:
		hold(Stalling)
	case calm && frozen > 0 && now.Sub(f.calmSince) >= p.CalmAfter && now.Sub(f.changed) >= p.Step:
		for _, m := range f.machines {
			if m.held() {
				thaw(m)
				break
			}
		}
	case frozen == 0 && len(f.waiting) > 0 && psi.Some.Avg10 < p.Admit && room == Room &&
		now.Sub(f.admitted) >= p.AdmitStep && f.Fits(s.Mem, now):
		out.Start = append(out.Start, f.start(now))
	}
	if room == Room && !high {
		f.spared = false
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
