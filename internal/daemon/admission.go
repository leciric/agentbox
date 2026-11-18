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
// already going matters more than a new agent's boot. While the user's
// computer is busy (hostload.go), nothing boots either, but for that alone
// and only as long as it lasts: builds the host held back aren't held for
// the VM's memory, so they don't keep machines waiting once it calms down.
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
	held := s.bootHeld(time.Now())
	s.stepAdmission(time.Now(), s.lastMem(), held)
	select {
	case <-ch:
	default:
		if held == pressure.WaitHost || s.hostLoad.holdBoots(time.Now()) {
			s.logf("memory: %s is on hold: its machine boots once this computer, outside the VM, is calm again", ref)
		} else {
			s.logf("memory: %s is on hold: its machine waits for the memory to boot", ref)
		}
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
			// It no longer waits: On hold, it would show as waiting with
			// nothing to boot it (resumeHeld runs only as the daemon starts).
			s.unhold(context.WithoutCancel(ctx), a, state.HoldMemory)
			s.refreshAgents(context.WithoutCancel(ctx))
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
	now := time.Now()
	return len(ad.a.Waiting()) > 0 || !ad.a.Fits(ad.a.Mem(), now) || s.hostLoad.holdBoots(now)
}

// stepAdmission lets the first machine in line boot when it fits. On the
// pressure loop: nothing here waits.
func (s *Server) stepAdmission(now time.Time, m pressure.Mem, held string) {
	ad := s.admit
	ad.mu.Lock()
	ad.a.HostShort = s.hostLoad.holdBoots(now)
	boot := ad.a.Step(now, m, held, ad.running)
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

// bootHeld is what a machine's boot waits behind now, besides the VM's
// memory: the user's computer being busy (pressure.WaitHost), heavy commands
// held for memory (pressure.WaitBuilds), or "" for neither.
func (s *Server) bootHeld(now time.Time) string {
	switch {
	case s.hostLoad.busy(now):
		return pressure.WaitHost
	case s.buildsHeld():
		return pressure.WaitBuilds
	}
	return ""
}

// buildsHeld reports whether any heavy command waits or is paused for
// memory: not when what holds them is the user's computer (bootHeld).
func (s *Server) buildsHeld() bool {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.g.Why() == pressure.HostBusy {
		return false
	}
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
	case pressure.WaitHost:
		out.Why = api.HoldWhyHost
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
