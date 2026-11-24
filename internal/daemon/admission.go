package daemon

import (
	"context"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// Admission (pressure.Admission decides, this applies it): an agent's
// machine boots when it is asked to, never held for the VM's memory, for
// builds, or for the user's computer being busy or under its reserve
// (19 Nov 2026, the user's decision; pressure/admission.go says why). Two
// things are left: machines asked for at once boot BootStep apart, oldest
// agent first, and none boots while the user's computer, outside the VM, is
// below its hard floor (api.HostLoad.HoldBoots, from the host supervisor,
// which stops agents meanwhile). Waiting for either, the agent is On hold
// (state.HoldMemory), machine stopped: the mark is kept in the database, so
// a daemon that restarts starts it again (continueTurns, restart.go), and it
// is only ever a mark.

type admitState struct {
	mu sync.Mutex
	a  *pressure.Admission
	// started is closed as a waiting machine may boot, by ref.
	started map[string]chan struct{}
}

func newAdmitState() *admitState {
	return &admitState{a: pressure.NewAdmission(), started: map[string]chan struct{}{}}
}

// admitBoot waits until a's machine may boot: its turn, and the user's
// computer over its hard floor. The agent is On hold meanwhile.
func (s *Server) admitBoot(ctx context.Context, a state.Agent) error {
	ref := a.Ref()
	if s.pressureEvery <= 0 && !s.lastMem().Known() {
		return nil // a test's daemon with no loop to step it
	}
	ad := s.admit
	ad.mu.Lock()
	if ad.a.Ask(ref, a.CreatedAt, time.Now()) {
		ad.mu.Unlock()
		s.unhold(ctx, a, state.HoldMemory)
		return nil
	}
	ch := ad.started[ref]
	if ch == nil {
		ch = make(chan struct{})
		ad.started[ref] = ch
	}
	ad.mu.Unlock()
	// Its turn and the host over its floor: this step lets it boot now.
	s.stepAdmission(time.Now())
	select {
	case <-ch:
	default:
		ad.mu.Lock()
		why := ad.a.Why()
		ad.mu.Unlock()
		if why == pressure.WaitHost {
			s.logf("memory: %s is on hold: its machine boots once this computer, outside the VM, is over its hard floor again", ref)
		} else {
			s.logf("memory: %s's machine boots in turn, a few seconds after the one before it", ref)
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
			// nothing to boot it (continueTurns runs only as the daemon starts).
			s.unhold(context.WithoutCancel(ctx), a, state.HoldMemory)
			s.refreshAgents(context.WithoutCancel(ctx))
			return ctx.Err()
		}
	}
	s.unhold(context.WithoutCancel(ctx), a, state.HoldMemory)
	s.refreshSoon()
	return nil
}

// unhold takes an agent off hold as its machine boots, whatever put it
// there: running, it is held for nothing.
func (s *Server) unhold(ctx context.Context, a state.Agent, _ string) {
	was := a.Hold
	if cur, err := s.store.Agent(ctx, a.Project, a.Name); err == nil {
		was = cur.Hold
	} else if was == "" {
		return
	}
	if was == "" {
		return
	}
	if err := s.store.SetHold(ctx, a.Project, a.Name, ""); err != nil {
		s.logf("memory: taking %s off hold: %v", a.Ref(), err)
	}
	if was == state.HoldRestart {
		// Started by hand or by a message: one fewer waits for Resume all.
		s.publishRestartHold(ctx)
	}
}

// bootWaits reports whether a machine asking to boot now would wait: for
// its turn, or for the user's computer to be over its hard floor.
func (s *Server) bootWaits() bool {
	now := time.Now()
	short := s.hostLoad.holdBoots(now)
	ad := s.admit
	ad.mu.Lock()
	defer ad.mu.Unlock()
	ad.a.HostShort = short
	return len(ad.a.Waiting()) > 0 || ad.a.Holds(now) != ""
}

// stepAdmission lets the first machine in line boot in its turn. On the
// pressure loop: nothing here waits.
func (s *Server) stepAdmission(now time.Time) {
	short := s.hostLoad.holdBoots(now)
	ad := s.admit
	ad.mu.Lock()
	ad.a.HostShort = short
	boot := ad.a.Step(now)
	for _, ref := range boot {
		if ch := ad.started[ref]; ch != nil {
			close(ch)
			delete(ad.started, ref)
		}
	}
	ad.mu.Unlock()
	for _, ref := range boot {
		s.logf("memory: %s's machine may boot", ref)
	}
	if len(boot) > 0 {
		s.refreshSoon()
	}
}

// admitHold is what admission holds of an agent's machine, nil for
// nothing: that it waits to boot, since when, and for what (its turn, or
// the user's computer). No place in line: a turn is seconds, and the host's
// floor holds every machine alike.
func (s *Server) admitHold(ref string) *api.MemoryHold {
	ad := s.admit
	ad.mu.Lock()
	defer ad.mu.Unlock()
	if !ad.a.Waits(ref) {
		return nil
	}
	out := api.MemoryHold{Machine: "waiting", Why: api.HoldWhyTurn}
	for _, w := range ad.a.Waiting() {
		if w.Agent == ref {
			out.Since = w.Asked
		}
	}
	if ad.a.Why() == pressure.WaitHost {
		out.Why = api.HoldWhyHost
	}
	return &out
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
