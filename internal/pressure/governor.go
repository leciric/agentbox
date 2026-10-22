package pressure

import (
	"math"
	"time"
)

// The governor. An agent's heavy command (a test run, a build: a run) asks
// before it starts, and starts at once while the VM is calm. Once memory
// pressure is up, new runs wait, holding only what their agents hold idle,
// and start in the order they asked as it falls. When pressure stays high
// anyway, from runs already going, the newest of those is paused (its cgroup
// frozen, its memory pushed out to swap by the daemon), then the next newest,
// and they resume, oldest first, once it has stayed calm.
//
// One run always makes progress: the oldest one is never paused, and when it
// ends the next oldest resumes at once; when nothing runs, the first waiter
// starts whatever the pressure. So the VM never fills with paused runs that
// can't finish, and every run does in turn. A run too big for the VM even
// alone is the agent's memory.max's to stop, not this.
//
// Pressure alone comes too late. PSI is an average over the last 10 seconds,
// and it rises only once tasks already stall: on 2026-10-11, eight agents'
// runs growing 50 MB/s each filled an 18 GiB VM and its 4.5 GiB of zram in 20
// seconds while full pressure read 7, 11, then 5%, and the VM then thrashed
// for seven minutes until the kernel killed in every agent; full pressure
// read 66% only after. So the governor also watches headroom: once the VM
// has been given all the memory it may have (short of its cap the host grows
// it), what it has available, how fast that falls and how fast memory pushed
// out comes back say how close it is to running out, now rather than ten
// seconds ago. See Judge.
//
// Not how full swap is, nor a share of memory: on 2026-10-12 the VM sat at
// its 17.8 GiB cap with 6.2 GiB available and its zram 97% full of idle
// agents' cold pages, PSI flat, for hours, which #273's rules (under 35%
// available with swap 90% full) took for running out, and paused and held
// tests with 6 GiB free. Swap fills with cold pages and stays full; that is
// the normal state of a VM that has been up a while, not pressure.

// Policy is when runs start, pause and resume. Pressures are PSI avg10
// percentages; shares of memory are 0 to 1, of MemTotal.
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

// Run is one heavy command of an agent's: waiting to start, running, or
// paused.
type Run struct {
	Agent   string // the agent's ref
	Key     string // the run's key, unique within its agent
	Command string // what it runs, for saying what was paused
	Asked   time.Time
	Started time.Time // zero while it waits
	Paused  bool
	// PausedAt is when it was last paused.
	PausedAt time.Time
	// Why is why it was last paused.
	Why Reason
	// Placed says its processes are in a cgroup of its own, which is what
	// pausing it freezes: a run that isn't is never paused.
	Placed bool
	// Until is when it lapses unless asked for again: a waiter's place, or
	// a run that isn't placed (a placed run lasts while its cgroup has
	// processes).
	Until time.Time
}

// ID is the run's agent and key together.
func (r *Run) ID() string { return r.Agent + "/" + r.Key }

// Actions is what a step decided, for the daemon to apply.
type Actions struct {
	Start, Pause, Resume []*Run
}

// Empty reports whether there is nothing to apply.
func (a Actions) Empty() bool { return len(a.Start)+len(a.Pause)+len(a.Resume) == 0 }

// Governor keeps the runs. It isn't safe for concurrent use.
type Governor struct {
	Policy Policy
	// running are the started runs, paused or not, in the order they
	// started; waiting the rest, in the order they asked.
	running, waiting []*Run
	highSince        time.Time // since when full pressure has been at Freeze or over
	calmSince        time.Time // since when it has been calm
	changed          time.Time // the last pause or resume
	admitted         time.Time // the last start
	last             Sample
	trend            Trend
	why              Reason
	trends           trends
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

// NewGovernor is a governor with policy.
func NewGovernor(policy Policy) *Governor { return &Governor{Policy: policy} }

// Ask is a run asking to start, or asking again: it reports the run, and
// whether it has started. A run that waits starts in a later Step. ttl is how
// long a run that isn't placed lasts unless asked for again.
func (g *Governor) Ask(agent, key, command string, ttl time.Duration, now time.Time) (*Run, bool) {
	if r := g.find(g.running, agent, key); r != nil {
		r.Until = laterOf(r.Until, now.Add(ttl))
		return r, true
	}
	if r := g.find(g.waiting, agent, key); r != nil {
		r.Until = now.Add(max(g.Policy.Grace, ttl))
		return r, false
	}
	r := &Run{Agent: agent, Key: key, Command: command, Asked: now, Until: now.Add(max(g.Policy.Grace, ttl))}
	g.waiting = append(g.waiting, r)
	return r, false
}

// Keep extends a waiter's place to grace from now, while its asker still
// waits.
func (g *Governor) Keep(agent, key string, ttl time.Duration, now time.Time) {
	if r := g.find(g.waiting, agent, key); r != nil {
		r.Until = laterOf(r.Until, now.Add(max(g.Policy.Grace, ttl)))
	}
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Place marks a running run's processes as in a cgroup of their own.
func (g *Governor) Place(agent, key string) bool {
	r := g.find(g.running, agent, key)
	if r != nil {
		r.Placed = true
	}
	return r != nil
}

// Get is a run by agent and key, running or waiting.
func (g *Governor) Get(agent, key string) *Run {
	if r := g.find(g.running, agent, key); r != nil {
		return r
	}
	return g.find(g.waiting, agent, key)
}

func (g *Governor) find(runs []*Run, agent, key string) *Run {
	for _, r := range runs {
		if r.Agent == agent && r.Key == key {
			return r
		}
	}
	return nil
}

// End takes a run out, running or waiting, and reports it, nil when there was
// none. A paused one is the daemon's to resume.
func (g *Governor) End(agent, key string) *Run {
	gone := g.remove(func(r *Run) bool { return r.Agent == agent && r.Key == key })
	if len(gone) == 0 {
		return nil
	}
	return gone[0]
}

// Drop takes out every run of an agent, as it stops or goes.
func (g *Governor) Drop(agent string) []*Run {
	return g.remove(func(r *Run) bool { return r.Agent == agent })
}

// Expire takes out the waiters whose place lapsed and the runs alive says are
// over, and reports them.
func (g *Governor) Expire(now time.Time, alive func(*Run) bool) []*Run {
	return g.remove(func(r *Run) bool {
		if r.Started.IsZero() {
			return now.After(r.Until)
		}
		return !alive(r)
	})
}

// remove takes out the runs gone says, and reports them.
func (g *Governor) remove(gone func(*Run) bool) []*Run {
	var out []*Run
	keep := func(runs []*Run) []*Run {
		kept := runs[:0]
		for _, r := range runs {
			if gone(r) {
				out = append(out, r)
				continue
			}
			kept = append(kept, r)
		}
		return kept
	}
	g.running = keep(g.running)
	g.waiting = keep(g.waiting)
	return out
}

// Running are the started runs, oldest first; Waiting the rest, in order.
func (g *Governor) Running() []*Run { return append([]*Run(nil), g.running...) }
func (g *Governor) Waiting() []*Run { return append([]*Run(nil), g.waiting...) }

// Last is the sample the last step saw.
func (g *Governor) Last() Sample { return g.last }

// Trend is how the VM's memory was moving at the last step.
func (g *Governor) Trend() Trend { return g.trend }

// Why is why, at the last step, runs were held back: waiters kept waiting
// and paused runs kept paused; "" when nothing held them.
func (g *Governor) Why() Reason { return g.why }

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

// Step decides, from the sample now, what starts, pauses and resumes.
func (g *Governor) Step(now time.Time, s Sample) Actions {
	g.last = s
	p := g.Policy
	psi := s.PSI
	tr := g.trends.observe(p, now, s)
	g.trend = tr
	room, why := p.Judge(s.Mem, tr)
	high := psi.Full.Avg10 >= p.Freeze
	calm := psi.Full.Avg10 < p.Calm && psi.Some.Avg10 < p.Admit && p.roomy(s.Mem, tr)
	if !high {
		g.highSince = time.Time{}
	} else if g.highSince.IsZero() {
		g.highSince = now
	}
	if !calm {
		g.calmSince = time.Time{}
	} else if g.calmSince.IsZero() {
		g.calmSince = now
	}
	stalling := high || psi.Some.Avg10 >= p.Admit
	switch {
	case room != Room:
		g.why = why
	case stalling:
		g.why = Stalling
	default:
		g.why = ""
	}
	var out Actions

	// The oldest run always runs: when the one that was ended, the next
	// resumes now, whatever the pressure.
	if len(g.running) > 0 && g.running[0].Paused {
		out.Resume = append(out.Resume, g.resume(g.running[0], now))
	}
	paused := g.paused()
	switch {
	case paused == 0 && len(g.running) == 0 && len(g.waiting) > 0:
		// Nothing runs: the first waiter starts, whatever the pressure, or
		// nothing ever would.
		out.Start = append(out.Start, g.start(now))
	case room == Out:
		// Running out now: everything that could grow stops at once.
		for r := g.newestPausable(); r != nil; r = g.newestPausable() {
			g.pause(r, now, why)
			out.Pause = append(out.Pause, r)
		}
	case room == Short && now.Sub(g.changed) >= p.HeadroomStep:
		if r := g.newestPausable(); r != nil {
			g.pause(r, now, why)
			out.Pause = append(out.Pause, r)
		}
	case high && now.Sub(g.highSince) >= p.FreezeAfter && now.Sub(g.changed) >= p.Step:
		if r := g.newestPausable(); r != nil {
			g.pause(r, now, Stalling)
			out.Pause = append(out.Pause, r)
		}
	case calm && paused > 0 && now.Sub(g.calmSince) >= p.CalmAfter && now.Sub(g.changed) >= p.Step:
		for _, r := range g.running {
			if r.Paused {
				out.Resume = append(out.Resume, g.resume(r, now))
				break
			}
		}
	case paused == 0 && len(g.waiting) > 0 && psi.Some.Avg10 < p.Admit && room == Room && now.Sub(g.admitted) >= p.AdmitStep:
		// Calm enough: the first waiter starts. Not while any run is paused,
		// which resumes first.
		out.Start = append(out.Start, g.start(now))
	}
	if g.why == "" && g.paused() > 0 {
		g.why = Easing
	}
	return out
}

func (g *Governor) paused() int {
	n := 0
	for _, r := range g.running {
		if r.Paused {
			n++
		}
	}
	return n
}

func (g *Governor) pause(r *Run, now time.Time, why Reason) {
	r.Paused, r.PausedAt, r.Why = true, now, why
	g.changed = now
}

func (g *Governor) resume(r *Run, now time.Time) *Run {
	r.Paused = false
	g.changed = now
	return r
}

func (g *Governor) start(now time.Time) *Run {
	r := g.waiting[0]
	g.waiting = g.waiting[1:]
	r.Started = now
	g.running = append(g.running, r)
	g.admitted = now
	return r
}

// newestPausable is the newest run that is running, placed and not the
// oldest, or nil.
func (g *Governor) newestPausable() *Run {
	for i := len(g.running) - 1; i > 0; i-- {
		if r := g.running[i]; !r.Paused && r.Placed {
			return r
		}
	}
	return nil
}

// StartAll starts every waiting run and resumes every paused one, as
// pausing whole machines (Fleet) takes over from the governor: it reports
// them, for the daemon to apply.
func (g *Governor) StartAll(now time.Time) Actions {
	var out Actions
	for _, r := range g.running {
		if r.Paused {
			out.Resume = append(out.Resume, g.resume(r, now))
		}
	}
	for len(g.waiting) > 0 {
		out.Start = append(out.Start, g.start(now))
	}
	return out
}
