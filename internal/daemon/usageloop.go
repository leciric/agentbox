package daemon

import (
	"context"
	"time"

	"agentbox/internal/agent"
)

// The usage loop: every half a minute it samples what each agent uses, for
// the lead recheck and the app, and rechecks the leads that are due
// (leadrecheck.go).

// usageInterval is how often running agents' usage is sampled.
const usageInterval = 30 * time.Second

// runUsage is the usage loop.
func (s *Server) runUsage(ctx context.Context) {
	if s.usageEvery <= 0 {
		return // a test drives what it needs itself
	}
	tick := func() {
		s.sampleUsage(ctx)
		if on, _, _ := s.store.LeadRecheck(ctx); on {
			s.recheckLeads(ctx, time.Now())
		}
	}
	tick()
	ticker := time.NewTicker(s.usageEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// usageSample is how long each sample of the agents' CPU is taken over.
const usageSample = time.Second

// sampleUsage measures what every agent uses now, and keeps it.
func (s *Server) sampleUsage(ctx context.Context) {
	_, agents, err := s.manager(nil).Usage(ctx, usageSample)
	if err != nil {
		s.logf("sampling usage: %v", err)
	}
	now := make(map[string]agent.AgentUsage, len(agents))
	for _, a := range agents {
		now[a.Ref()] = a
	}
	s.mu.Lock()
	s.usageNow = now
	s.mu.Unlock()
}

// lastUsage is what an agent was using when last sampled.
func (s *Server) lastUsage(ref string) (agent.AgentUsage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.usageNow[ref]
	return u, ok
}

// hasMachine reports whether an agent in this state has a machine that holds
// memory, or is getting one.
func hasMachine(state string) bool {
	switch state {
	case "running", "paused", "initializing":
		return true
	}
	return false
}
