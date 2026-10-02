package agent

import (
	"context"
	"time"

	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// Slots: how many of a project's agents may run at once before its queued
// ones wait. A queued agent is started only into a free slot, and nothing
// already running is ever stopped or paused to make one; an agent created
// without queueing starts at once, as it always has, and simply takes a slot.
//
// Auto slots come from memory. The budget is what agents may use — the VM's
// memory (SlotBudget) — less a reserve, so the agents'
// peaks never quite add up to all of it. Each project's agents are assumed to
// peak where its latest agents did (state.TypicalMemoryPeak), and the budget
// is shared between the projects that have work, running or queued, fairly in
// memory rather than in agents: a project whose agents peak at 6 GiB and one
// whose agents peak at 2 GiB each get about half the budget, so about three
// times as many of the second's. A project pinned to a number gets that number,
// and the memory those agents need is set aside before the rest is shared.

// SlotProject is one project, as far as sharing slots goes.
type SlotProject struct {
	Name string
	// Peak is the memory one of its agents is expected to reach, in bytes.
	Peak int64
	// Pinned is the user's own number of slots; 0 is auto.
	Pinned int
	// Running is how many of its agents hold memory now: running, paused, or
	// being made. Queued is how many wait.
	Running int
	Queued  int
}

// Active reports whether the project has work that wants slots.
func (p SlotProject) Active() bool { return p.Running+p.Queued > 0 }

// SlotReserve is what the budget keeps free of the agents' expected peaks: an
// eighth of it, and at least 2 GiB. A peak is a median, and agents above it
// need somewhere to go.
func SlotReserve(budget int64) int64 {
	return max(budget/8, int64(2)<<30)
}

// SplitSlots shares usable bytes of memory into slots between projects. Only
// active projects share the budget, but every project gets an answer: an idle
// one is given what it would get if it had work now, alongside the active
// ones, which is what the app shows before anything is queued.
//
// Every active project gets at least one slot, however large its peak, so
// nothing waits forever; beyond that a project is only given a slot its peak
// fits in. Slots go first to the projects with the least memory given so far,
// up to what each has work for, and then whatever is left goes the same way
// regardless of work, so a project's number says what it could run.
func SplitSlots(usable int64, projects []SlotProject) map[string]int {
	out := make(map[string]int, len(projects))
	var active []SlotProject
	for _, p := range projects {
		if p.Active() {
			active = append(active, p)
		}
	}
	for name, n := range splitActive(usable, active) {
		out[name] = n
	}
	for _, p := range projects {
		if p.Active() {
			continue
		}
		with := append(append([]SlotProject(nil), active...), SlotProject{Name: p.Name, Peak: p.Peak, Pinned: p.Pinned, Queued: 1})
		out[p.Name] = splitActive(usable, with)[p.Name]
	}
	return out
}

// splitActive is SplitSlots for projects that all count as having work.
func splitActive(usable int64, projects []SlotProject) map[string]int {
	out := make(map[string]int, len(projects))
	pool := usable
	type share struct {
		SlotProject
		given int
		full  bool // no further slot of its peak fits
	}
	var auto []*share
	for _, p := range projects {
		if p.Pinned > 0 {
			out[p.Name] = p.Pinned
			// What its agents will hold: the pinned number, or fewer when it
			// has less work than that, or more when agents made without the
			// queue already went past it.
			pool -= int64(max(min(p.Pinned, p.Running+p.Queued), p.Running)) * max(p.Peak, 1)
			continue
		}
		auto = append(auto, &share{SlotProject: p})
	}
	// Smallest memory given first; then the smaller peak, then the name, so
	// the same inputs always give the same answer.
	next := func(capped bool) *share {
		var best *share
		for _, s := range auto {
			if s.full || (capped && s.given >= s.Running+s.Queued) {
				continue
			}
			if best == nil {
				best = s
				continue
			}
			mine, theirs := int64(s.given)*s.Peak, int64(best.given)*best.Peak
			if mine < theirs || (mine == theirs && (s.Peak < best.Peak || (s.Peak == best.Peak && s.Name < best.Name))) {
				best = s
			}
		}
		return best
	}
	// Everyone's first slot comes first, fitting or not.
	for _, s := range auto {
		s.given = 1
		pool -= max(s.Peak, 1)
	}
	for _, capped := range []bool{true, false} {
		for s := next(capped); s != nil; s = next(capped) {
			if max(s.Peak, 1) > pool {
				s.full = true
				continue
			}
			s.given++
			pool -= max(s.Peak, 1)
		}
	}
	for _, s := range auto {
		out[s.Name] = s.given
	}
	return out
}

// SlotBudget is the memory auto slots are shared from, before the reserve.
// In the VM, that's the VM's memory: nothing but agents runs in it. On a host
// set up by an earlier release, agents share the host with its own apps, which
// keep a third of its memory and never less than 6 GiB, or half of a host too
// small for that.
func SlotBudget() int64 {
	memory := HostMemory()
	if hostos.InVM() {
		return memory
	}
	const gib = int64(1) << 30
	if agents := memory - max(memory/3, 6*gib); agents >= gib {
		return agents
	}
	return memory / 2
}

// defaultSlotPeak is the peak assumed for a project nothing is known about,
// before any of its agents was seen.
const defaultSlotPeak = int64(4) << 30

// ProjectPeak is the memory one of a project's agents is expected to reach:
// what its latest agents did, or defaultSlotPeak before any was seen.
func (m *Manager) ProjectPeak(ctx context.Context, project string) (peak int64, learned bool, err error) {
	peak, err = m.Store.TypicalMemoryPeak(ctx, project)
	if err != nil || peak > 0 {
		return peak, peak > 0, err
	}
	return defaultSlotPeak, false, nil
}

// ProjectShape is what one of a project's agents is expected to use, its
// baseline and its burst (MemoryShape), with the defaults for what wasn't
// seen yet.
func (m *Manager) ProjectShape(ctx context.Context, project string) (Shape, error) {
	baseline, burst, err := m.Store.MemoryShape(ctx, project)
	if err != nil {
		return Shape{}, err
	}
	return learnedShape(baseline, burst), nil
}

// SampleUsage measures every agent's CPU and memory over interval (Usage,
// so memory without the file cache the kernel can drop) and keeps the most
// each running agent was seen at: what ProjectPeak learns from, and, apart
// for its baseline and its heavy phases (bursting says which an agent is in),
// what ProjectShape does. It returns the readings.
func (m *Manager) SampleUsage(ctx context.Context, now time.Time, interval time.Duration, bursting func(ref string) bool) ([]AgentUsage, error) {
	_, agents, err := m.Usage(ctx, interval)
	if err != nil {
		return nil, err
	}
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || a.State != "running" {
			continue
		}
		if err := m.Store.RecordUsagePeak(ctx, a.Project, a.Name, a.Memory, a.CPU, now); err != nil {
			return agents, err
		}
		if err := m.Store.RecordPhasePeak(ctx, a.Project, a.Name, a.Memory, bursting != nil && bursting(a.Ref()), now); err != nil {
			return agents, err
		}
	}
	return agents, nil
}
