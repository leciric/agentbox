package daemon

import (
	"context"
	"time"

	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// While the VM's memory is short, the builds and tests agents start run
// fewer jobs at once: every machine's worker counts (agent.workersScript,
// which each shell reads as it starts) are halved while its
// agent.MemoryShortFile says so. The pressure loop decides, as it holds heavy
// commands back for the same reasons, and this tells the running machines
// aside from it: as it changes, and again every minute while it lasts, for
// machines started since. Short starts at once and ends once it has been
// calm for memoryShortCalm, so a run of readings either side of the line
// doesn't flip it every two seconds.

const (
	memoryShortCalm  = 2 * time.Minute
	memoryShortAgain = time.Minute
)

// memoryShort is the pressure loop's side: only it calls note.
type memoryShort struct {
	short     bool
	calmSince time.Time
	// changed carries the latest state to tellMemoryShort.
	changed chan bool
}

func newMemoryShort() *memoryShort { return &memoryShort{changed: make(chan bool, 1)} }

// note takes one reading's reason runs are held back for ("" when nothing
// holds them). It never blocks: a state tellMemoryShort hasn't taken yet is
// replaced by the newer one.
func (m *memoryShort) note(now time.Time, why pressure.Reason) {
	short := why != "" && why != pressure.Easing
	switch {
	case short:
		m.calmSince = time.Time{}
		if m.short {
			return
		}
	case !m.short:
		return
	case m.calmSince.IsZero():
		m.calmSince = now
		return
	case now.Sub(m.calmSince) < memoryShortCalm:
		return
	}
	m.short = short
	for {
		select {
		case m.changed <- short:
			return
		default:
			select {
			case <-m.changed:
			default:
			}
		}
	}
}

// tellMemoryShort writes what note decides into the running machines. It
// starts by telling them memory isn't short, which a daemon that stopped
// while it was may have left them believing.
func (s *Server) tellMemoryShort(ctx context.Context) {
	short := false
	told := s.setMemoryShort(ctx, short, nil)
	again := time.NewTicker(memoryShortAgain)
	defer again.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case short = <-s.memoryShort.changed:
			if short {
				s.logf("memory pressure: memory is short; builds and tests started from now on run half as many jobs")
			} else {
				s.logf("memory pressure: memory eased; builds and tests run as many jobs as before")
			}
			told = s.setMemoryShort(ctx, short, nil)
		case <-again.C:
			if short {
				told = s.setMemoryShort(ctx, short, told)
			}
		}
	}
}

// setMemoryShort tells the running machines whether memory is short, but for
// those in told, which it returns with the ones it told added.
func (s *Server) setMemoryShort(ctx context.Context, short bool, told map[string]bool) map[string]bool {
	if told == nil {
		told = map[string]bool{}
	}
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return told
	}
	instances, err := s.cfg.Incus.Instances(ctx)
	if err != nil {
		return told
	}
	running := map[string]bool{}
	for _, inst := range instances {
		running[inst.Name] = inst.Status == "Running"
	}
	m := s.manager(s.cfg.Log)
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady || !running[a.Instance] || told[a.Instance] {
			continue
		}
		if err := m.SetMemoryShort(ctx, a, short); err != nil {
			if ctx.Err() == nil {
				s.logOnce("memory-short:"+a.Ref(), "memory pressure: telling "+a.Ref()+" memory is short: "+err.Error())
			}
			continue
		}
		told[a.Instance] = true
	}
	return told
}
