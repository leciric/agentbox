package pressure

import (
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

// Policy is when runs start, pause and resume. Pressures are PSI avg10
// percentages.
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
	last             PSI
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

// Last is the pressure the last step saw.
func (g *Governor) Last() PSI { return g.last }

// Step decides, from the pressure now, what starts, pauses and resumes.
func (g *Governor) Step(now time.Time, psi PSI) Actions {
	g.last = psi
	p := g.Policy
	high := psi.Full.Avg10 >= p.Freeze
	calm := psi.Full.Avg10 < p.Calm && psi.Some.Avg10 < p.Admit
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
	case high && now.Sub(g.highSince) >= p.FreezeAfter && now.Sub(g.changed) >= p.Step:
		if r := g.newestPausable(); r != nil {
			r.Paused, r.PausedAt = true, now
			g.changed = now
			out.Pause = append(out.Pause, r)
		}
	case calm && paused > 0 && now.Sub(g.calmSince) >= p.CalmAfter && now.Sub(g.changed) >= p.Step:
		for _, r := range g.running {
			if r.Paused {
				out.Resume = append(out.Resume, g.resume(r, now))
				break
			}
		}
	case paused == 0 && len(g.waiting) > 0 && psi.Some.Avg10 < p.Admit && now.Sub(g.admitted) >= p.AdmitStep:
		// Calm enough: the first waiter starts. Not while any run is paused,
		// which resumes first.
		out.Start = append(out.Start, g.start(now))
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
