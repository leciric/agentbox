package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// Pausing whole agents for memory (Settings → Experimental, off by default;
// pressure.Fleet decides, this applies it). With it on, memory pressure
// freezes agents' machines whole, newest first and never the oldest working
// one, and thaws them oldest first; a machine boots once the VM really has
// the memory for it; and heavy commands start at once, as what they would
// have been paused for is now done to their machines. With it off, nothing
// here runs and the heavy commands' governor (pressure.go) is as it was.
//
// A machine is frozen through its cgroup, written directly (its freezer is
// handed to the daemon's user as the sync loop first sees it, so the pressure
// loop needs no sudo when the VM is out of memory), and its memory pushed
// out to zram aside. Its processes keep their pipes and sockets: the chat's
// ACP connection from the daemon into the machine stays open, the tool's
// turn simply stops and carries on when it thaws, and tmux with it. A
// message for a frozen agent is held in its chat (chat.Manager.Hold) and
// handed over as it thaws, rather than resuming it.
//
// The pressure loop never waits on SQLite or Incus: which machines run and
// which work is gathered on the memory watch's loop (syncFleet), and what is
// remembered across a restart (the agents it froze, the boots measured) is
// written aside.

// bootMeasureAfter is how long after a machine boots its memory is read as
// what its boot took; bootCostsKept how many of those are kept.
const (
	bootMeasureAfter = 60 * time.Second
	bootCostsKept    = 9
)

// machineCgroups is what freezing whole machines does to their cgroups:
// agent's functions, or a test's.
type machineCgroups interface {
	// Running reports whether the machine's cgroup is there, and whether it
	// is frozen.
	Running(instance string) (running, frozen bool)
	Own(instance string) error
	Freeze(instance string, on bool) error
	Reclaim(instance string) error
	Memory(instance string) int64
}

type agentMachineCgroups struct{}

func (agentMachineCgroups) Running(instance string) (bool, bool) {
	return agent.MachineRunning(instance)
}
func (agentMachineCgroups) Own(instance string) error { return agent.OwnMachineFreezer(instance) }
func (agentMachineCgroups) Freeze(instance string, on bool) error {
	return agent.FreezeMachine(instance, on)
}
func (agentMachineCgroups) Reclaim(instance string) error { return agent.ReclaimMachine(instance) }
func (agentMachineCgroups) Memory(instance string) int64  { return agent.MachineMemory(instance) }

// fleetState is the fleet and what the daemon keeps beside it, under mu,
// which may be taken under heavyRuns.mu but never the other way round.
type fleetState struct {
	// step is held across a step and applying it, so two steps' freezes and
	// thaws aren't applied out of order.
	step sync.Mutex
	mu   sync.Mutex
	f    *pressure.Fleet
	// on is the setting as last read.
	on bool
	// instance is each agent's machine, by ref.
	instance map[string]string
	// started is closed as a waiting machine may boot, by ref.
	started map[string]chan struct{}
	// frozen is what was last saved of the agents the fleet froze.
	saved []string
}

func newFleetState() *fleetState {
	return &fleetState{
		f:        pressure.NewFleet(pressure.DefaultPolicy),
		instance: map[string]string{},
		started:  map[string]chan struct{}{},
	}
}

// fleetOn reports whether whole agents are paused for memory, as the setting
// was last read.
func (s *Server) fleetOn() bool {
	fl := s.frozen
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.on
}

// loadFleet reads the setting and the boots measured as the daemon starts, and thaws what a daemon before it froze and didn't thaw.
func (s *Server) loadFleet(ctx context.Context) {
	on, _ := s.store.Flag(ctx, state.SettingAgentFreeze)
	var costs []int64
	if v, err := s.store.Setting(ctx, state.SettingBootCosts); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &costs)
	}
	var left []string
	if v, err := s.store.Setting(ctx, state.SettingAgentFreezeFrozen); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &left)
	}
	fl := s.frozen
	fl.mu.Lock()
	fl.on = on
	fl.f.BootCost = pressure.MedianBootCost(costs)
	fl.mu.Unlock()
	for _, ref := range left {
		a, err := s.agentByRef(ctx, ref)
		if err != nil || a.Instance == "" {
			continue
		}
		s.logf("memory: thawing %s, frozen for memory before AgentBox restarted", ref)
		if err := s.machineCgroups.Freeze(a.Instance, false); err != nil {
			s.logf("memory: thawing %s: %v", ref, err)
		}
	}
	if len(left) > 0 {
		_ = s.store.SetSetting(ctx, state.SettingAgentFreezeFrozen, "")
	}
}

// setFleet turns pausing whole agents on or off. Off thaws every machine it
// froze and lets every waiting one boot; on starts every heavy command that
// waits and resumes the paused ones, as from now their machines are what
// pauses.
func (s *Server) setFleet(ctx context.Context, on bool) error {
	if err := s.store.SetFlag(ctx, state.SettingAgentFreeze, on); err != nil {
		return err
	}
	fl := s.frozen
	fl.mu.Lock()
	was := fl.on
	fl.on = on
	var thaw []string
	if was && !on {
		for _, m := range fl.f.Machines() {
			if m.Frozen {
				thaw = append(thaw, m.Agent)
			}
		}
		for ref, ch := range fl.started {
			close(ch)
			delete(fl.started, ref)
			fl.f.Cancel(ref)
		}
		fl.f.Sync(nil)
	}
	instances := map[string]string{}
	for _, ref := range thaw {
		instances[ref] = fl.instance[ref]
	}
	fl.mu.Unlock()
	for _, ref := range thaw {
		s.thawMachine(ctx, ref, instances[ref])
	}
	if was && !on {
		s.saveFleetFrozen(ctx, nil)
	}
	if on && !was {
		s.startAllHeavy()
	}
	s.refreshSoon()
	return nil
}

// startAllHeavy starts every heavy command waiting and resumes every paused
// one, as pausing whole agents takes over from the heavy commands' governor.
func (s *Server) startAllHeavy() {
	h := s.heavy
	h.mu.Lock()
	acts := h.g.StartAll(time.Now())
	type resume struct {
		instance, key string
	}
	var resumed []resume
	for _, r := range acts.Start {
		if ch := h.started[r.ID()]; ch != nil {
			close(ch)
			delete(h.started, r.ID())
		}
	}
	for _, r := range acts.Resume {
		resumed = append(resumed, resume{h.instance[r.ID()], r.Key})
	}
	h.mu.Unlock()
	for _, r := range resumed {
		if err := s.runCgroups.Freeze(r.instance, r.key, false); err != nil {
			s.logf("memory pressure: resuming %s's %s: %v", r.instance, r.key, err)
		}
	}
}

// syncFleet tells the fleet which machines run and which of them work, on
// the memory watch's loop: it may wait on SQLite. agents are every agent.
func (s *Server) syncFleet(ctx context.Context, agents []state.Agent) {
	if !s.fleetOn() {
		return
	}
	busy := map[string]bool{}
	h := s.heavy
	h.mu.Lock()
	for _, r := range h.g.Running() {
		busy[r.Agent] = true
	}
	h.mu.Unlock()
	type seen struct {
		a              state.Agent
		running, froze bool
	}
	var all []seen
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || a.Instance == "" {
			continue
		}
		running, frozen := s.machineCgroups.Running(a.Instance)
		if !running {
			continue
		}
		if err := s.machineCgroups.Own(a.Instance); err != nil {
			s.logOnce("freezer "+a.Instance, "memory: "+a.Ref()+": taking its freezer: "+err.Error())
		}
		all = append(all, seen{a, running, frozen})
	}
	fl := s.frozen
	fl.mu.Lock()
	defer fl.mu.Unlock()
	var ms []pressure.Machine
	for _, x := range all {
		ref := x.a.Ref()
		// Frozen by somebody else (paused by hand, or by the disk guard):
		// not the fleet's to thaw, and holding nothing it can free.
		if x.froze && !fl.f.Frozen(ref) {
			continue
		}
		fl.instance[ref] = x.a.Instance
		ms = append(ms, pressure.Machine{
			Agent:   ref,
			Since:   x.a.CreatedAt,
			Working: busy[ref] || s.chat.Busy(x.a),
			Frozen:  x.froze,
		})
	}
	fl.f.Sync(ms)
}

// stepFleet applies what the fleet makes of sample: machines frozen, thawed,
// let boot. On the pressure loop: nothing here waits on SQLite or Incus.
func (s *Server) stepFleet(ctx context.Context, now time.Time, sample pressure.Sample) {
	fl := s.frozen
	fl.step.Lock()
	defer fl.step.Unlock()
	fl.mu.Lock()
	if !fl.on {
		fl.mu.Unlock()
		return
	}
	acts := fl.f.Step(now, sample)
	why, trend := fl.f.Why(), fl.f.Trend()
	instance := func(ref string) string { return fl.instance[ref] }
	type change struct{ ref, instance string }
	var freeze, thaw []change
	for _, ref := range acts.Freeze {
		freeze = append(freeze, change{ref, instance(ref)})
	}
	for _, ref := range acts.Thaw {
		thaw = append(thaw, change{ref, instance(ref)})
	}
	for _, ref := range acts.Start {
		if ch := fl.started[ref]; ch != nil {
			close(ch)
			delete(fl.started, ref)
		}
	}
	var frozen []string
	for _, m := range fl.f.Machines() {
		if m.Frozen {
			frozen = append(frozen, m.Agent)
		}
	}
	changed := !equalStrings(frozen, fl.saved)
	fl.saved = frozen
	fl.mu.Unlock()

	for _, c := range thaw {
		s.thawMachine(ctx, c.ref, c.instance)
	}
	for _, c := range freeze {
		s.logf("memory: freezing %s's machine: %s: %s", c.ref, reasonWords(why, sample, trend), sample)
		if err := s.machineCgroups.Freeze(c.instance, true); err != nil {
			s.logf("memory: freezing %s: %v", c.ref, err)
			continue
		}
		go func(c change) {
			if err := s.machineCgroups.Reclaim(c.instance); err != nil {
				s.logf("memory: pushing %s's memory out: %v", c.ref, err)
			}
		}(c)
	}
	for _, ref := range acts.Start {
		s.logf("memory: %s's machine may boot: %s", ref, sample)
	}
	if changed {
		go s.saveFleetFrozen(context.WithoutCancel(ctx), frozen)
	}
	if !acts.Empty() {
		s.refreshSoon()
	}
}

// thawMachine thaws a machine the fleet froze, and hands its chat what was
// held for it meanwhile.
func (s *Server) thawMachine(ctx context.Context, ref, instance string) {
	s.logf("memory: thawing %s's machine", ref)
	if err := s.machineCgroups.Freeze(instance, false); err != nil {
		s.logf("memory: thawing %s: %v", ref, err)
	}
	go func() {
		a, err := s.agentByRef(context.WithoutCancel(ctx), ref)
		if err != nil {
			return
		}
		if err := s.chat.Release(a); err != nil {
			s.logf("memory: %s: handing over what was held for it: %v", ref, err)
		}
	}()
}

func (s *Server) saveFleetFrozen(ctx context.Context, refs []string) {
	v := ""
	if len(refs) > 0 {
		b, _ := json.Marshal(refs)
		v = string(b)
	}
	if err := s.store.SetSetting(ctx, state.SettingAgentFreezeFrozen, v); err != nil {
		s.logf("memory: remembering the agents frozen for memory: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// frozenForMemory reports whether the fleet froze an agent's machine.
func (s *Server) frozenForMemory(ref string) bool {
	fl := s.frozen
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.on && fl.f.Frozen(ref)
}

// admitMachine is agent.Manager.Admit: with pausing whole agents on, it
// waits until the VM has the memory to boot a's machine, the agent showing
// "waiting for memory, N ahead" meanwhile. Off, it lets it boot at once.
func (s *Server) admitMachine(ctx context.Context, a state.Agent) error {
	ref := a.Ref()
	fl := s.frozen
	fl.mu.Lock()
	if !fl.on {
		fl.mu.Unlock()
		return nil
	}
	fl.instance[ref] = a.Instance
	if fl.f.Ask(ref, time.Now()) {
		fl.mu.Unlock()
		go s.measureBoot(context.WithoutCancel(ctx), a.Instance)
		return nil
	}
	ch := fl.started[ref]
	if ch == nil {
		ch = make(chan struct{})
		fl.started[ref] = ch
	}
	fl.mu.Unlock()
	// Calm and room enough: this step lets it boot now.
	s.stepPressure(ctx)
	select {
	case <-ch:
	default:
		s.logf("memory: %s's machine waits for the memory to boot", ref)
		s.refreshAgents(ctx)
		select {
		case <-ch:
		case <-ctx.Done():
			fl.mu.Lock()
			fl.f.Cancel(ref)
			if fl.started[ref] == ch {
				delete(fl.started, ref)
			}
			fl.mu.Unlock()
			return ctx.Err()
		}
	}
	go s.measureBoot(context.WithoutCancel(ctx), a.Instance)
	s.refreshSoon()
	return nil
}

// measureBoot reads what a machine took once it has booted, and keeps it
// among the last boots measured, which the fleet's BootCost is the median
// of.
func (s *Server) measureBoot(ctx context.Context, instance string) {
	if s.pressureEvery <= 0 {
		return // a test's daemon: no machines to measure
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(bootMeasureAfter):
	}
	used := s.machineCgroups.Memory(instance)
	if used <= 0 {
		return
	}
	var costs []int64
	if v, err := s.store.Setting(ctx, state.SettingBootCosts); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &costs)
	}
	costs = append(costs, used)
	if len(costs) > bootCostsKept {
		costs = costs[len(costs)-bootCostsKept:]
	}
	b, _ := json.Marshal(costs)
	if err := s.store.SetSetting(ctx, state.SettingBootCosts, string(b)); err != nil {
		s.logf("memory: remembering what a boot took: %v", err)
	}
	fl := s.frozen
	fl.mu.Lock()
	fl.f.BootCost = pressure.MedianBootCost(costs)
	fl.mu.Unlock()
}

// machineHold is what pausing whole agents holds of an agent's machine, nil
// for nothing.
func (s *Server) machineHold(ref string) *api.MemoryHold {
	fl := s.frozen
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if !fl.on {
		return nil
	}
	n, frozen, ok := fl.f.Ahead(ref)
	if !ok {
		return nil
	}
	out := api.MemoryHold{Machine: "waiting", Ahead: n}
	if frozen {
		out.Machine = "paused"
		for _, m := range fl.f.Machines() {
			if m.Agent == ref {
				out.Since = m.FrozenAt
			}
		}
	} else {
		for _, w := range fl.f.Waiting() {
			if w.Agent == ref {
				out.Since = w.Asked
			}
		}
	}
	last, tr := fl.f.Last(), fl.f.Trend()
	out.Why = string(fl.f.Why())
	out.Available, out.Total = last.Mem.Available, last.Mem.Total
	switch fl.f.Why() {
	case pressure.Stalling:
		out.Stalled = last.Some.Avg10
	case pressure.Falling:
		out.Rate = int64(tr.Fall)
	case pressure.Churning:
		out.Rate = int64(tr.Churn)
	}
	return &out
}
