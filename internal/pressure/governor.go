package pressure

import (
	"math"
	"time"
)

// Heavy commands. An agent's test runs and builds (runs) start the moment
// they are asked for, and nothing waits or is paused for memory (19 Nov 2026,
// the user's decision): like T3 Code, the kernel's limits are what protect
// the VM, each agent's memory.max and every agent's together under
// agentbox.agents' memory.high and memory.max (package agent), with the
// agent told what was killed when one is reached. Until then the governor
// held runs back on the VM's PSI, which also counts tasks stalled at those
// limits of their own: with the VM at its 8 GiB cap and 2 to 4 GiB of it
// free, the agents throttled at their shared limit kept builds waiting and
// paused for twenty minutes and more, and every boot behind them.
//
// What is left is Runs, which runs go, for what knows whether an agent works
// (idle reclaim, the VM's own idle shrink, the shared package cache) and how
// many workers a build starts with (the cores split between the runs going);
// how short the VM's memory is (Runs.Why: the headroom Judge works out, or
// PSI stalling), which the app is shown and nothing acts on (builds ran fewer
// jobs for it until 20 Nov 2026); and, with the experimental "pause whole
// agents" on, the Fleet, which still pauses a machine's runs or the machine.
//
// Pressure alone comes too late. PSI is an average over the last 10 seconds,
// and it rises only once tasks already stall: on 2026-10-11, eight agents'
// runs growing 50 MB/s each filled an 18 GiB VM and its 4.5 GiB of zram in 20
// seconds while full pressure read 7, 11, then 5%. So the headroom is judged
// too: once the VM has been given all the memory it may have (short of its
// cap the host grows it), what it has available, how fast that falls and how
// fast memory pushed out comes back say how close it is to running out, now
// rather than ten seconds ago. See Judge.
//
// Not how full swap is, nor a share of memory: on 2026-10-12 the VM sat at
// its 17.8 GiB cap with 6.2 GiB available and its zram 97% full of idle
// agents' cold pages, PSI flat, for hours. Swap fills with cold pages and
// stays full; that is the normal state of a VM that has been up a while, not
// pressure.

// Policy is when the Fleet freezes and thaws machines, and when the VM's
// memory counts as short (Runs.Why). Pressures are PSI avg10 percentages;
// shares of memory are 0 to 1, of MemTotal.
type Policy struct {
	// Admit is the some pressure under which waiting runs start.
	Admit float64
	// Freeze is the full pressure at or over which, held for FreezeAfter,
	// runs are paused, one every Step.
	Freeze      float64
	FreezeAfter time.Duration
	// Calm is the full pressure under which, with some under Admit and held
	// for CalmAfter, paused runs resume, one every Step.
	Calm      float64
	CalmAfter time.Duration
	// Step is the least time between one pause or resume and the next, for
	// the 10-second average to show what the last one did.
	Step time.Duration
	// AdmitStep is the least time between two runs starting, for the same
	// reason, more briefly: a run's memory shows within a second or two.
	AdmitStep time.Duration
	// Grace is how long a waiter keeps its place once nobody is asking for
	// it, so a command that asks again finds its place kept.
	Grace time.Duration

	// Floor is the memory, in bytes, the VM keeps available, or FloorShare
	// of MemTotal when that is more: under it, at its cap with swap full,
	// the VM is Out.
	Floor      int64
	FloorShare float64
	// SwapFull is how full swap is when what is available is all that is
	// left: with room in swap, the kernel makes more, and PSI shows what
	// that costs. SwapBusy is how full it is when a fall toward the floor
	// already holds new runs back.
	SwapFull, SwapBusy float64
	// Span is the least time a Trend is measured over, so one allocation
	// isn't a ramp; Window the most it looks back.
	Span, Window time.Duration
	// At the rate available falls, reaching Floor within OutWithin is Out,
	// within ShortWithin Short, and within TightWithin Tight.
	OutWithin, ShortWithin, TightWithin time.Duration
	// Churn is the rate, in bytes a second, of memory coming back in
	// (Trend.Churn) at which, swap full, the working set doesn't fit: the
	// VM is Tight, and Short under Low.
	Churn float64
	// Low is, in Floors, the memory available under which, swap full, the
	// VM is Tight; Roomy, in Floors too, what paused runs resume only over
	// (so one resumed doesn't make it Tight again at once).
	Low, Roomy float64
	// HeadroomStep is the least time between two pauses for headroom.
	// MemAvailable is now, not an average, so it shows a pause's effect at
	// the next reading.
	HeadroomStep time.Duration
}

// Trend is how the VM's memory is moving, from the readings of the last
// Policy.Window, over at least Policy.Span: what tells a ramp from a VM
// that is merely full.
type Trend struct {
	// Fall is how fast MemAvailable falls, in bytes a second; 0 while it
	// holds or rises.
	Fall float64
	// Churn is how fast memory pushed out comes back in (Mem.Refaulted), in
	// bytes a second.
	Churn float64
}

// Headroom is how close the VM is to running out of memory. Low MemAvailable
// alone isn't close: while swap has room the kernel makes more, and PSI
// shows what that costs. Once swap is full, what is available is what is
// left, and how fast it falls how long it lasts. With no swap, it always
// was.
type Headroom int

const (
	// Room: short of its cap, swap with room, or plenty available and
	// nothing eating into it.
	Room Headroom = iota
	// Tight: at its cap, swap full, and either little available or it
	// falling, or the working set churning through swap. Runs wait.
	Tight
	// Short: at its cap, swap full, available falling to the floor within
	// ShortWithin, or churning under Low. Runs pause one by one.
	Short
	// Out: at its cap, swap full, under the floor or about to be. Every run
	// but the oldest pauses.
	Out
)

// Reason is why runs are held back, for the app and the agents to say: ""
// when nothing holds them.
type Reason string

const (
	// Stalling: PSI says tasks stall on memory.
	Stalling Reason = "stalling"
	// Falling: what is available falls toward the floor.
	Falling Reason = "falling"
	// Churning: memory pushed out keeps coming back in.
	Churning Reason = "churning"
	// Low: little is available, swap full.
	Low Reason = "low"
	// Easing: it eased; paused runs resume first, one at a time.
	Easing Reason = "easing"
	// HostBusy: the user's computer is loaded (Sample.HostBusy).
	HostBusy Reason = "host"
)

// floor is the memory the VM keeps available, in bytes.
func (p Policy) floor(m Mem) float64 {
	return max(float64(p.Floor), p.FloorShare*float64(m.Total))
}

// Headroom is how close m is to running out, going as tr says.
func (p Policy) Headroom(m Mem, tr Trend) Headroom {
	h, _ := p.Judge(m, tr)
	return h
}

// Judge is how close m is to running out, going as tr says, and why.
func (p Policy) Judge(m Mem, tr Trend) (Headroom, Reason) {
	floor := p.floor(m)
	if !m.AtCap() || floor <= 0 {
		return Room, ""
	}
	avail, swap := float64(m.Available), m.SwapUsed()
	// How long, in seconds, what is available lasts over the floor at the
	// rate it falls.
	lasts := math.Inf(1)
	if tr.Fall > 0 {
		lasts = max(0, avail-floor) / tr.Fall
	}
	within := func(d time.Duration) bool { return lasts < d.Seconds() }
	churning := p.Churn > 0 && tr.Churn >= p.Churn
	full := swap >= p.SwapFull
	switch {
	case full && avail < floor:
		return Out, Low
	case full && within(p.OutWithin):
		return Out, Falling
	case full && within(p.ShortWithin):
		return Short, Falling
	case full && churning && avail < p.Low*floor:
		return Short, Churning
	case full && avail < p.Low*floor:
		return Tight, Low
	case full && churning:
		return Tight, Churning
	case swap >= p.SwapBusy && within(p.TightWithin):
		return Tight, Falling
	}
	return Room, ""
}

// roomy reports whether m, going as tr says, has room enough for paused runs
// to resume.
func (p Policy) roomy(m Mem, tr Trend) bool {
	return p.Headroom(m, tr) == Room && (!m.AtCap() || float64(m.Available) >= p.Roomy*p.floor(m) || m.SwapUsed() < p.SwapFull)
}

// DefaultPolicy is the daemon's.
var DefaultPolicy = Policy{
	Admit:       10,
	Freeze:      20,
	FreezeAfter: 5 * time.Second,
	Calm:        5,
	CalmAfter:   10 * time.Second,
	Step:        10 * time.Second,
	AdmitStep:   2 * time.Second,
	Grace:       30 * time.Second,
	// From the 2026-10-11 trace (TestTheStallOf20261011PausesBeforeIt): at
	// its 18 GiB cap, zram full, the VM went from 7.8 GiB available to 5.9
	// to 2.3, ten seconds a reading, and stalled four seconds after the
	// last. 5.9 falling 1.9 in ten seconds reaches the 1.8 GiB floor in 22:
	// Short pauses at that reading, and at the next, 0.5 over the floor
	// falling 3.6, Out everything, seven minutes before the kernel killed.
	// And from 2026-10-12's (TestAFullSwapWithMemoryToSpareHoldsNothing):
	// 6.2 GiB available holding still, zram 97% full, is Room.
	Floor:        3 << 29, // 1.5 GiB
	FloorShare:   0.1,
	SwapFull:     0.9,
	SwapBusy:     0.5,
	Span:         10 * time.Second,
	Window:       30 * time.Second,
	OutWithin:    10 * time.Second,
	ShortWithin:  30 * time.Second,
	TightWithin:  2 * time.Minute,
	Churn:        64 << 20, // 64 MiB/s
	Low:          2,
	Roomy:        3,
	HeadroomStep: 2 * time.Second,
}

// Run is one heavy command of an agent's, from when it is asked for until it
// ends. Only the Fleet pauses one.
type Run struct {
	Agent   string // the agent's ref
	Key     string // the run's key, unique within its agent
	Command string // what it runs, for saying what was paused
	Started time.Time
	Paused  bool
	// PausedAt is when it was last paused.
	PausedAt time.Time
	// Why is why it was last paused.
	Why Reason
	// ForFleet: the fleet paused it (PauseAgent), and only the fleet resumes
	// it.
	ForFleet bool
	// Placed says its processes are in a cgroup of its own, which is what
	// pausing it freezes: a run that isn't is never paused.
	Placed bool
	// Until is when a run that isn't placed lapses unless asked for again
	// (a placed run lasts while its cgroup has processes).
	Until time.Time
}

// ID is the run's agent and key together.
func (r *Run) ID() string { return r.Agent + "/" + r.Key }

// Runs keeps the heavy commands going, oldest first, and the VM's memory as
// last observed. It holds none back. It isn't safe for concurrent use.
type Runs struct {
	Policy  Policy
	running []*Run
	last    Sample
	trend   Trend
	why     Reason
	trends  trends
}

// NewRuns is Runs judging memory by policy.
func NewRuns(policy Policy) *Runs { return &Runs{Policy: policy} }

// Ask is a run asking to start, or asking again: it starts at once, and is
// reported. ttl is how long a run that isn't placed lasts unless asked for
// again.
func (g *Runs) Ask(agent, key, command string, ttl time.Duration, now time.Time) *Run {
	if r := g.Get(agent, key); r != nil {
		r.Until = laterOf(r.Until, now.Add(ttl))
		return r
	}
	r := &Run{Agent: agent, Key: key, Command: command, Started: now, Until: now.Add(ttl)}
	g.running = append(g.running, r)
	return r
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Place marks a run's processes as in a cgroup of their own.
func (g *Runs) Place(agent, key string) bool {
	r := g.Get(agent, key)
	if r != nil {
		r.Placed = true
	}
	return r != nil
}

// Get is a run by agent and key, nil for none.
func (g *Runs) Get(agent, key string) *Run {
	for _, r := range g.running {
		if r.Agent == agent && r.Key == key {
			return r
		}
	}
	return nil
}

// End takes a run out and reports it, nil when there was none. A paused one
// is the daemon's to resume.
func (g *Runs) End(agent, key string) *Run {
	gone := g.remove(func(r *Run) bool { return r.Agent == agent && r.Key == key })
	if len(gone) == 0 {
		return nil
	}
	return gone[0]
}

// Drop takes out every run of an agent, as it stops or goes.
func (g *Runs) Drop(agent string) []*Run {
	return g.remove(func(r *Run) bool { return r.Agent == agent })
}

// Expire takes out the runs alive says are over, and reports them.
func (g *Runs) Expire(alive func(*Run) bool) []*Run {
	return g.remove(func(r *Run) bool { return !alive(r) })
}

// remove takes out the runs gone says, and reports them.
func (g *Runs) remove(gone func(*Run) bool) []*Run {
	var out []*Run
	kept := g.running[:0]
	for _, r := range g.running {
		if gone(r) {
			out = append(out, r)
			continue
		}
		kept = append(kept, r)
	}
	g.running = kept
	return out
}

// Running are the runs, oldest first.
func (g *Runs) Running() []*Run { return append([]*Run(nil), g.running...) }

// Observe takes the VM's memory at now: Last, Trend and Why report it after.
func (g *Runs) Observe(now time.Time, s Sample) {
	g.last = s
	g.trend = g.trends.observe(g.Policy, now, s)
	room, why := g.Policy.Judge(s.Mem, g.trend)
	switch {
	case room != Room:
		g.why = why
	case s.Full.Avg10 >= g.Policy.Freeze || s.Some.Avg10 >= g.Policy.Admit:
		g.why = Stalling
	case s.HostBusy:
		g.why = HostBusy
	default:
		g.why = ""
	}
}

// Last is the sample last observed.
func (g *Runs) Last() Sample { return g.last }

// Trend is how the VM's memory was moving at the last observation.
func (g *Runs) Trend() Trend { return g.trend }

// Why is how short the VM's memory was at the last observation, "" for not
// at all: running out (Judge's Low, Falling or Churning), its tasks stalling
// on it (Stalling), or the user's computer busy (HostBusy). Builds run fewer
// jobs while it is anything; nothing waits or is paused for it.
func (g *Runs) Why() Reason { return g.why }

// PauseAgent pauses, for the fleet, an agent's runs that go and are placed,
// rather than its whole machine (Fleet.toHold): it reports them, for the
// daemon to apply.
func (g *Runs) PauseAgent(agent string, now time.Time, why Reason) []*Run {
	var out []*Run
	for _, r := range g.running {
		if r.Agent == agent && r.Placed && !r.Paused {
			r.Paused, r.PausedAt, r.Why, r.ForFleet = true, now, why, true
			out = append(out, r)
		}
	}
	return out
}

// ResumeAgent resumes the runs the fleet paused of an agent, or of every
// agent for "": it reports them, for the daemon to apply.
func (g *Runs) ResumeAgent(agent string) []*Run {
	var out []*Run
	for _, r := range g.running {
		if r.ForFleet && (agent == "" || r.Agent == agent) {
			r.ForFleet = false
			if r.Paused {
				r.Paused = false
				out = append(out, r)
			}
		}
	}
	return out
}

// trends keeps the readings of the last Policy.Window, at least a second
// apart, oldest first, that a Trend is worked out from.
type trends struct {
	seen []reading
}

// reading is what a Trend is worked out from.
type reading struct {
	at                   time.Time
	available, refaulted int64
}

// observe keeps s's reading, and works out the Trend from those before it.
func (g *trends) observe(p Policy, now time.Time, s Sample) Trend {
	if !s.Mem.Known() {
		return Trend{}
	}
	drop := 0
	for drop < len(g.seen) && now.Sub(g.seen[drop].at) > p.Window {
		drop++
	}
	g.seen = append(g.seen[:0], g.seen[drop:]...)
	var tr Trend
	churnFrom := -1
	for i, r := range g.seen {
		age := now.Sub(r.at).Seconds()
		if now.Sub(r.at) < p.Span || age <= 0 {
			continue
		}
		// The steepest fall from any reading at least Span ago: a ramp that
		// began a while back still counts.
		tr.Fall = max(tr.Fall, float64(r.available-s.Mem.Available)/age)
		churnFrom = i
	}
	// Churn over the shortest span there is: the latest Span-old reading.
	if churnFrom >= 0 {
		r := g.seen[churnFrom]
		if d := s.Mem.Refaulted - r.refaulted; d > 0 {
			tr.Churn = float64(d) / now.Sub(r.at).Seconds()
		}
	}
	if n := len(g.seen); n == 0 || now.Sub(g.seen[n-1].at) >= time.Second {
		g.seen = append(g.seen, reading{now, s.Mem.Available, s.Mem.Refaulted})
	}
	return tr
}
