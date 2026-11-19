package daemon

import (
	"net/http"
	"sync"
	"time"

	"agentbox/internal/state"
)

// The agent the user is looking at. The app says so as it opens an agent,
// and again while it stays open (PUT /v1/agents/{project}/{agent}/viewing):
// idle reclaim leaves that agent alone, and one On hold for being idle
// starts again, as opening it is touching it. One agent at a time: the last
// the app said.

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
	if !a.IsLead() && a.Status == state.AgentReady && a.Hold == state.HoldIdle {
		// Opened after it sat idle: it starts again, as a message would.
		s.wakeAside(r.Context(), s.manager(s.cfg.Log), a, "opening it in the app")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
