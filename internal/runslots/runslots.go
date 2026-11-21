// Package runslots is how many agents may be in their run phase at once.
//
// An agent works in two phases (20 Nov 2026, the user's decision). In the
// code phase it edits, researches and runs targeted tests and builds: that
// never waits for anything. In the run phase it runs the app, docker
// compose, Playwright, its desktop, browser or Android emulator, and records
// its evidence: that is what fills the VM's memory, and it takes a run slot,
// waiting its turn in a first-come, first-served queue while none is free.
//
// A slot is memory, not a count fixed in advance. The agents together may use
// Budget.Pool (the VM less what the daemon, Incus and the lead chats keep);
// what the agents in their code phase use now comes off it, and each slot
// held reserves its project's learned run peak, or what its machine uses if
// that is more. A run is granted when its project's peak fits in what is
// left. The peak is learned per project from each run (Learn): the highest
// the holder's machine reached while it held the slot, starting from
// DefaultPeak.
//
// A slot is a lease: it lasts LeaseTTL from its last heartbeat (Beat), which
// is anything the agent does in its run phase, and expires otherwise; the
// daemon then stops what the run phase started. A place in the queue lasts
// WaitTTL from the last time its agent asked (an agent waiting asks every few
// seconds), so one that stopped waiting doesn't hold up the line.
//
// The package holds no lock and reads nothing: the daemon
// (internal/daemon/runslots.go) measures the VM and the machines, keeps a
// Pool under its own lock, and acts on what Step returns.
package runslots

import (
	"slices"
	"time"
)

const (
	// DefaultPeak is a project's run peak until one of its runs is measured:
	// a dev server, a browser on it and Playwright come to a couple of GiB.
	DefaultPeak = int64(2) << 30
	// MinPeak is the least a learned peak goes down to: a run measured
	// while it hardly started mustn't let a dozen in at once.
	MinPeak = int64(512) << 20
	// LeaseTTL is how long a slot lasts after its last heartbeat.
	LeaseTTL = 10 * time.Minute
	// WaitTTL is how long a place in the queue lasts after its agent last
	// asked.
	WaitTTL = time.Minute
)

// Budget is the memory runs share, as the daemon last measured it.
type Budget struct {
	// Pool is what every agent's machine may use together.
	Pool int64
	// CodeUse is what the machines of agents holding no slot use now.
	CodeUse int64
}

// Lease is a slot held.
type Lease struct {
	Agent   string // project/agent
	Project string
	// Key names the run phase's cgroup in the agent's machine.
	Key     string
	Granted time.Time
	Beat    time.Time
	Until   time.Time
	// Reserved is the project's run peak when it was granted.
	Reserved int64
	// Use is what its machine uses now, and Peak the most it used since it
	// was granted, as the daemon looked.
	Use, Peak int64
	// What the run phase started that the daemon stops when it ends: the
	// browser, the emulator, a recording, and the containers that weren't
	// running when it was granted (Containers, known when ContainersKnown).
	Browser, Android, Recording bool
	Containers                  []string
	ContainersKnown             bool
}

// reserve is what a lease holds of the budget.
func (l *Lease) reserve() int64 { return max(l.Reserved, l.Use) }

// Waiter is a place in the queue.
type Waiter struct {
	Agent   string
	Project string
	Since   time.Time
	Seen    time.Time
}

// Status is where an agent stands.
type Status struct {
	// State is "holding", "waiting" or "" for neither.
	State string
	// Ahead is, waiting, how many are before it in the queue; asked with
	// Try and not queued, how many are queued.
	Ahead int
	// Lease is a copy of its lease, holding; Waiter of its place, waiting.
	Lease  *Lease
	Waiter *Waiter
}

const (
	Holding = "holding"
	Waiting = "waiting"
)

// Peak is a project's learned run peak.
type Peak struct {
	Bytes int64
	Runs  int
	At    time.Time
}

// Pool is the slots, the queue and the projects' peaks.
type Pool struct {
	leases map[string]*Lease
	queue  []*Waiter
	peaks  map[string]Peak
	budget Budget
	// key makes each lease's key, given the agent and when it was granted.
	key func(agent string, now time.Time) string
}

// New is an empty pool.
func New() *Pool {
	return &Pool{
		leases: map[string]*Lease{},
		peaks:  map[string]Peak{},
		key: func(_ string, now time.Time) string {
			return "slot-" + now.UTC().Format("20060102T150405.000000000")
		},
	}
}

// SetPeak sets a project's learned peak, as the daemon kept it.
func (p *Pool) SetPeak(project string, peak Peak) {
	if peak.Bytes > 0 {
		p.peaks[project] = peak
	}
}

// PeakOf is a project's run peak: learned, or DefaultPeak.
func (p *Pool) PeakOf(project string) int64 {
	if pk, ok := p.peaks[project]; ok && pk.Bytes > 0 {
		return pk.Bytes
	}
	return DefaultPeak
}

// Peaks is every learned peak, by project.
func (p *Pool) Peaks() map[string]Peak {
	out := make(map[string]Peak, len(p.peaks))
	for k, v := range p.peaks {
		out[k] = v
	}
	return out
}

// Learn takes in one run's measured peak: a run above what was learned
// raises it at once, as the next one may need as much; one below lowers it a
// quarter of the way, so a run that did less than usual doesn't let too many
// in. Never under MinPeak. It answers the peak now; observed 0 (nothing
// measured) changes nothing.
func (p *Pool) Learn(project string, observed int64, now time.Time) Peak {
	pk, known := p.peaks[project]
	if observed <= 0 {
		return pk
	}
	switch {
	case !known || pk.Bytes <= 0 || observed >= pk.Bytes:
		pk.Bytes = observed
	default:
		pk.Bytes -= (pk.Bytes - observed) / 4
	}
	pk.Bytes = max(pk.Bytes, MinPeak)
	pk.Runs++
	pk.At = now
	p.peaks[project] = pk
	return pk
}

// Measure sets the budget the next grants are made against.
func (p *Pool) Measure(b Budget) { p.budget = b }

// Budget is the budget last measured.
func (p *Pool) Budget() Budget { return p.budget }

// Observe records what a holder's machine uses now.
func (p *Pool) Observe(agent string, use int64) {
	if l := p.leases[agent]; l != nil && use > 0 {
		l.Use = use
		l.Peak = max(l.Peak, use)
	}
}

// Holds reports whether agent holds a slot.
func (p *Pool) Holds(agent string) bool { return p.leases[agent] != nil }

// Free is the memory left for another run: the pool, less the code phase's
// use and every slot's reservation. It may be below 0.
func (p *Pool) Free() int64 {
	free := p.budget.Pool - p.budget.CodeUse
	for _, l := range p.leases {
		free -= l.reserve()
	}
	return free
}

// fits reports whether a run of project may start now: its peak fits in
// what is free, or no slot is held at all, so one run always can.
func (p *Pool) fits(project string) bool {
	return len(p.leases) == 0 || p.Free() >= p.PeakOf(project)
}

// grant gives agent a slot.
func (p *Pool) grant(agent, project string, now time.Time) *Lease {
	l := &Lease{
		Agent: agent, Project: project, Key: p.key(agent, now),
		Granted: now, Beat: now, Until: now.Add(LeaseTTL),
		Reserved: p.PeakOf(project),
	}
	p.leases[agent] = l
	return l
}

// Up asks for a slot for agent, of project: one it holds is renewed, one
// that is free is granted, and otherwise it keeps or takes its place at the
// back of the queue. Nobody is granted past someone queued before them.
// granted says this call granted it.
func (p *Pool) Up(agent, project string, now time.Time) (st Status, granted bool) {
	if l := p.leases[agent]; l != nil {
		p.beat(l, now)
		return p.Status(agent), false
	}
	if i := p.place(agent); i >= 0 {
		p.queue[i].Seen = now
		return p.Status(agent), false
	}
	if len(p.queue) == 0 && p.fits(project) {
		p.grant(agent, project, now)
		return p.Status(agent), true
	}
	p.queue = append(p.queue, &Waiter{Agent: agent, Project: project, Since: now, Seen: now})
	return p.Status(agent), false
}

// Try is Up that never queues: a slot held is renewed, one free with nobody
// queued is granted, and otherwise the agent stays where it was. A starting
// browser or emulator asks this way, from a call that can't wait.
func (p *Pool) Try(agent, project string, now time.Time) (st Status, granted bool) {
	if p.leases[agent] != nil || p.place(agent) >= 0 {
		if l := p.leases[agent]; l != nil {
			p.beat(l, now)
		}
		return p.Status(agent), false
	}
	if len(p.queue) == 0 && p.fits(project) {
		p.grant(agent, project, now)
		return p.Status(agent), true
	}
	return Status{Ahead: len(p.queue)}, false
}

// Beat renews agent's slot, reporting whether it holds one.
func (p *Pool) Beat(agent string, now time.Time) bool {
	l := p.leases[agent]
	if l != nil {
		p.beat(l, now)
	}
	return l != nil
}

func (p *Pool) beat(l *Lease, now time.Time) {
	l.Beat = now
	if until := now.Add(LeaseTTL); until.After(l.Until) {
		l.Until = until
	}
}

// Mark records what agent's run phase started, on its lease: f changes it.
// It reports whether the agent holds one.
func (p *Pool) Mark(agent string, f func(*Lease)) bool {
	l := p.leases[agent]
	if l != nil {
		f(l)
	}
	return l != nil
}

// place is agent's index in the queue, -1 for none.
func (p *Pool) place(agent string) int {
	return slices.IndexFunc(p.queue, func(w *Waiter) bool { return w.Agent == agent })
}

// Status is where agent stands.
func (p *Pool) Status(agent string) Status {
	if l := p.leases[agent]; l != nil {
		c := *l
		c.Containers = slices.Clone(l.Containers)
		return Status{State: Holding, Lease: &c}
	}
	if i := p.place(agent); i >= 0 {
		w := *p.queue[i]
		return Status{State: Waiting, Ahead: i, Waiter: &w}
	}
	return Status{}
}

// Down gives agent's slot back, or its place in the queue: the lease it
// held, or nil.
func (p *Pool) Down(agent string) *Lease {
	if l := p.leases[agent]; l != nil {
		delete(p.leases, agent)
		return l
	}
	if i := p.place(agent); i >= 0 {
		p.queue = slices.Delete(p.queue, i, i+1)
	}
	return nil
}

// Step expires the leases nobody renewed and the places nobody asked for,
// then grants what fits from the front of the queue, in order, stopping at
// the first that doesn't.
func (p *Pool) Step(now time.Time) (expired, granted []*Lease, dropped []*Waiter) {
	for agent, l := range p.leases {
		if !now.Before(l.Until) {
			expired = append(expired, l)
			delete(p.leases, agent)
		}
	}
	slices.SortFunc(expired, func(a, b *Lease) int { return a.Granted.Compare(b.Granted) })
	p.queue = slices.DeleteFunc(p.queue, func(w *Waiter) bool {
		gone := now.Sub(w.Seen) >= WaitTTL
		if gone {
			dropped = append(dropped, w)
		}
		return gone
	})
	return expired, p.Grant(now), dropped
}

// Grant grants what fits from the front of the queue, in order, stopping at
// the first that doesn't: as a slot is given back, the next in line has it
// at once.
func (p *Pool) Grant(now time.Time) (granted []*Lease) {
	for len(p.queue) > 0 && p.fits(p.queue[0].Project) {
		w := p.queue[0]
		p.queue = p.queue[1:]
		granted = append(granted, p.grant(w.Agent, w.Project, now))
	}
	return granted
}

// Leases is every slot held, oldest first; copies.
func (p *Pool) Leases() []Lease {
	out := make([]Lease, 0, len(p.leases))
	for _, l := range p.leases {
		c := *l
		c.Containers = slices.Clone(l.Containers)
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Lease) int { return a.Granted.Compare(b.Granted) })
	return out
}

// Queue is the queue, front first; copies.
func (p *Pool) Queue() []Waiter {
	out := make([]Waiter, len(p.queue))
	for i, w := range p.queue {
		out[i] = *w
	}
	return out
}

// Counts is the slots as the app shows them.
type Counts struct {
	// InUse is the slots held, Free how many more runs fit, Total both,
	// and Queued the agents waiting.
	InUse, Free, Total, Queued int
	// Size is the run peak the free slots are counted in.
	Size int64
}

// Counts counts the slots: Free in runs of the project at the front of the
// queue, or with nobody queued of the largest peak among projects (one run
// of any of them fits in each free slot), DefaultPeak when there are none.
// With nobody queued and no slot held, one is always free.
func (p *Pool) Counts(projects []string) Counts {
	c := Counts{InUse: len(p.leases), Queued: len(p.queue)}
	if len(p.queue) > 0 {
		c.Size = p.PeakOf(p.queue[0].Project)
	} else {
		for _, project := range projects {
			c.Size = max(c.Size, p.PeakOf(project))
		}
		if c.Size == 0 {
			c.Size = DefaultPeak
		}
	}
	if free := p.Free(); free > 0 {
		c.Free = int(free / c.Size)
	}
	if c.InUse == 0 && c.Queued == 0 {
		c.Free = max(c.Free, 1)
	}
	c.Total = c.InUse + c.Free
	return c
}

// FreeFor is how many runs of project fit now: with nobody queued and no
// slot held, at least one.
func (p *Pool) FreeFor(project string) int {
	n := 0
	if free := p.Free(); free > 0 {
		n = int(free / p.PeakOf(project))
	}
	if len(p.leases) == 0 && len(p.queue) == 0 {
		n = max(n, 1)
	}
	return n
}
