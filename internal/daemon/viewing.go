package daemon

import (
	"context"
	"net/http"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// The agent the user is looking at. The app says so as it opens an agent,
// and again while it stays open (PUT /v1/agents/{project}/{agent}/viewing):
// that agent's builds go first in the governor's line, idle reclaim leaves
// it alone, and one On hold for being idle starts again, as opening it is
// touching it. One agent at a time: the last the app said.

// viewingFor is how long the app's word that it shows an agent lasts; the
// app says it again well inside it.
const viewingFor = 90 * time.Second

type viewingState struct {
	mu  sync.Mutex
	ref string
	at  time.Time
}

// viewed is the agent the app shows, "" for none lately.
func (s *Server) viewed() string {
	s.view.mu.Lock()
	defer s.view.mu.Unlock()
	if s.view.ref == "" || time.Since(s.view.at) > viewingFor {
		return ""
	}
	return s.view.ref
}

// viewAgent is PUT /v1/agents/{project}/{agent}/viewing.
func (s *Server) viewAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	ref := a.Ref()
	s.view.mu.Lock()
	s.view.ref, s.view.at = ref, time.Now()
	s.view.mu.Unlock()
	s.touchIdle(ref)
	h := s.heavy
	h.mu.Lock()
	h.g.Prefer(ref)
	h.mu.Unlock()
	if !a.IsLead() && a.Status == state.AgentReady && a.Hold == state.HoldIdle {
		// Opened after it sat idle: it starts again, as a message would.
		s.wakeAside(r.Context(), s.manager(s.cfg.Log), a, "opening it in the app")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// reclaimIdleSoon pushes every idle agent's memory out to swap, aside, as a
// build is about to wait for memory: what idle machines hold is given back
// before anybody's work waits. At most once every idleReclaimSoonEvery.
func (s *Server) reclaimIdleSoon(ctx context.Context) {
	s.idle.mu.Lock()
	if time.Since(s.idle.reclaimedSoon) < idleReclaimSoonEvery {
		s.idle.mu.Unlock()
		return
	}
	s.idle.reclaimedSoon = time.Now()
	s.idle.mu.Unlock()
	go func() {
		ctx := context.WithoutCancel(ctx)
		// It is idle reclaim's: off, as it is by default, nothing is
		// pushed out. Before 2026-11-06 it ran whatever the setting said.
		if on, _, _, err := s.store.IdleReclaim(ctx); err != nil || !on {
			return
		}
		agents, err := s.store.Agents(ctx, "")
		if err != nil {
			return
		}
		viewed := s.viewed()
		var freed int64
		n := 0
		for _, a := range agents {
			ref := a.Ref()
			if a.IsLead() || a.Status != state.AgentReady || a.Instance == "" || ref == viewed {
				continue
			}
			if running, frozen := s.machineCgroups.Running(a.Instance); !running || frozen {
				continue
			}
			if s.chatBusy(ref) || s.heavyHeld(ref) {
				continue
			}
			// Pushed out lately: what it holds now is what it pages back in.
			// Asked again every 30 seconds while builds wait, it was
			// reclaimed over and over.
			s.idle.mu.Lock()
			last := s.idle.reclaimed[ref]
			recent := time.Since(last) < idleReclaimAgain
			if !recent {
				s.idle.reclaimed[ref] = time.Now()
			}
			s.idle.mu.Unlock()
			if recent || !s.mayReclaim(ctx, a.Instance) {
				continue
			}
			before, after, err := s.idleCgroups.Reclaim(s.background(), a.Instance)
			if err != nil {
				continue
			}
			freed += max(before-after, 0)
			n++
		}
		if n > 0 {
			s.logf("memory: a build waits: pushed %d idle agents' memory out first, %s freed", n, agent.HumanBytes(freed))
		}
	}()
}

// idleReclaimSoonEvery is the least time between two reclaimIdleSoon, and
// idleReclaimAgain the least between two reclaims of one agent by it.
const (
	idleReclaimSoonEvery = 30 * time.Second
	idleReclaimAgain     = 10 * time.Minute
)
