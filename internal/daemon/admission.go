package daemon

import (
	"context"
	"sort"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// Admission (pressure.Admission decides, this applies it): every agent's
// machine boots only once the VM has the memory for it, whatever the
// experimental "pause whole agents" says. The user starts as many agents as
// they like; those that don't fit are On hold (state.HoldMemory), machine
// stopped, and boot by themselves, oldest first, as room frees. The mark is
// kept in the database, so a daemon that restarts starts them again in the
// same order (resumeHeld), and it is only ever a mark: nothing removes an
// agent for being on hold.
//
// The heavy commands' governor (pressure.go) comes first: while a build
// waits or is paused for memory, nothing new boots, as the work of agents
// already going matters more than a new agent's boot.
//
// With pausing whole agents on, its fleet does all of this instead
// (admitMachine in agentfreeze.go), as it did.

type admitState struct {
	mu sync.Mutex
	a  *pressure.Admission
	// started is closed as a waiting machine may boot, by ref.
	started map[string]chan struct{}
	// running is how many agents' machines ran at the last memory watch.
	running int
}

func newAdmitState() *admitState {
	return &admitState{a: pressure.NewAdmission(pressure.DefaultPolicy), started: map[string]chan struct{}{}}
}

// admitBoot waits until the VM has the memory to boot a's machine, the agent
// On hold meanwhile.
func (s *Server) admitBoot(ctx context.Context, a state.Agent) error {
	ref := a.Ref()
	if s.pressureEvery <= 0 && !s.lastMem().Known() {
		return nil // a test's daemon with no memory to go by, and no loop to step it
	}
	ad := s.admit
	ad.mu.Lock()
	if ad.a.Ask(ref, a.CreatedAt, time.Now()) {
		ad.mu.Unlock()
		s.unhold(ctx, a, state.HoldMemory)
		go s.measureBoot(context.WithoutCancel(ctx), a.Instance)
		return nil
	}
	ch := ad.started[ref]
	if ch == nil {
		ch = make(chan struct{})
		ad.started[ref] = ch
	}
	ad.mu.Unlock()
	// Room enough and nothing ahead: this step lets it boot now.
	s.stepAdmission(time.Now(), s.lastMem(), s.buildsHeld())
	select {
	case <-ch:
	default:
		s.logf("memory: %s is on hold: its machine waits for the memory to boot", ref)
		if err := s.store.SetHold(ctx, a.Project, a.Name, state.HoldMemory); err != nil {
			s.logf("memory: putting %s on hold: %v", ref, err)
		}
		s.refreshAgents(ctx)
		select {
		case <-ch:
		case <-ctx.Done():
			ad.mu.Lock()
			ad.a.Cancel(ref)
			if ad.started[ref] == ch {
				delete(ad.started, ref)
			}
			ad.mu.Unlock()
			return ctx.Err()
		}
	}
	s.unhold(context.WithoutCancel(ctx), a, state.HoldMemory)
	go s.measureBoot(context.WithoutCancel(ctx), a.Instance)
	s.refreshSoon()
	return nil
}

// unhold takes an agent off hold as its machine boots, whatever put it
// there: running, it is held for nothing.
func (s *Server) unhold(ctx context.Context, a state.Agent, _ string) {
	if a.Hold == "" {
		if cur, err := s.store.Agent(ctx, a.Project, a.Name); err != nil || cur.Hold == "" {
			return
		}
	}
	if err := s.store.SetHold(ctx, a.Project, a.Name, ""); err != nil {
		s.logf("memory: taking %s off hold: %v", a.Ref(), err)
	}
}

// bootWaits reports whether a machine asking to boot now would wait.
func (s *Server) bootWaits() bool {
	if s.fleetOn() {
		return true // the fleet decides aside (admitMachine)
	}
	ad := s.admit
	ad.mu.Lock()
	defer ad.mu.Unlock()
	return len(ad.a.Waiting()) > 0 || !ad.a.Fits(ad.a.Mem(), time.Now())
}

// stepAdmission lets the first machine in line boot when it fits. On the
// pressure loop: nothing here waits.
func (s *Server) stepAdmission(now time.Time, m pressure.Mem, buildsHeld bool) {
	ad := s.admit
	ad.mu.Lock()
	boot := ad.a.Step(now, m, buildsHeld, ad.running)
	for _, ref := range boot {
		if ch := ad.started[ref]; ch != nil {
			close(ch)
			delete(ad.started, ref)
		}
	}
	ad.mu.Unlock()
	for _, ref := range boot {
		s.logf("memory: %s's machine may boot: %s available of %s", ref, agent.HumanBytes(m.Available), agent.HumanBytes(m.Total))
	}
	if len(boot) > 0 {
		s.refreshSoon()
	}
}

// buildsHeld reports whether any heavy command waits or is paused for
// memory.
func (s *Server) buildsHeld() bool {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.g.Waiting()) > 0 {
		return true
	}
	for _, r := range h.g.Running() {
		if r.Paused {
			return true
		}
	}
	return false
}

// lastMem is the VM's memory as the pressure loop last read it.
func (s *Server) lastMem() pressure.Mem {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.g.Last().Mem
}

// countRunning is told, on the memory watch, how many machines run.
func (s *Server) countRunning(agents []state.Agent) {
	n := 0
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || a.Instance == "" {
			continue
		}
		if running, _ := s.machineCgroups.Running(a.Instance); running {
			n++
		}
	}
	s.admit.mu.Lock()
	s.admit.running = n
	s.admit.mu.Unlock()
}

// admitHold is what admission holds of an agent, nil for nothing: its place
// in line and why the line waits.
func (s *Server) admitHold(ref string) *api.MemoryHold {
	ad := s.admit
	ad.mu.Lock()
	defer ad.mu.Unlock()
	n, ok := ad.a.Place(ref)
	if !ok {
		return nil
	}
	out := api.MemoryHold{Machine: "waiting", Ahead: n}
	for _, w := range ad.a.Waiting() {
		if w.Agent == ref {
			out.Since = w.Asked
		}
	}
	m := ad.a.Mem()
	out.Available, out.Total = m.Available, m.Total
	switch ad.a.Why() {
	case pressure.WaitBuilds:
		out.Why = api.HoldWhyBuilds
	case pressure.WaitMemory:
		out.Why = api.HoldWhyMemory
	}
	return &out
}

// resumeHeld starts, as the daemon starts, every agent a daemon before it
// left On hold for memory, oldest first: each waits its turn in admission
// again. Those on hold for being idle stay stopped until touched.
func (s *Server) resumeHeld(ctx context.Context) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		s.logf("memory: agents on hold: %v", err)
		return
	}
	// A turn to carry on starts its agent itself (continueTurns).
	turns, _ := s.store.RunningTurns(ctx)
	resuming := map[string]bool{}
	for _, rt := range turns {
		resuming[rt.Project+"/"+rt.Agent] = true
	}
	var held []state.Agent
	for _, a := range agents {
		if a.Hold == state.HoldMemory && !a.IsLead() && a.Status == state.AgentReady && !resuming[a.Ref()] {
			held = append(held, a)
		}
	}
	if len(held) == 0 {
		return
	}
	sort.SliceStable(held, func(i, j int) bool { return held[i].CreatedAt.Before(held[j].CreatedAt) })
	// Judged on a reading of the VM's memory, not before one.
	for wait := 0; wait < 30 && !s.lastMem().Known() && s.pressureEvery > 0; wait++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	m := s.manager(s.cfg.Log)
	for _, a := range held {
		s.wakeAside(ctx, m, a, "its turn, on hold for memory before AgentBox restarted")
	}
}

// holdOf is Agent.Hold for an agent whose machine is in state: none while
// it runs, whatever the mark says (it is cleared as it boots).
func holdOf(a state.Agent, machine string) string { return holdOfInfo(a.Hold, machine) }

func holdOfInfo(hold, machine string) string {
	if machine == "running" || machine == "paused" {
		return ""
	}
	return hold
}
