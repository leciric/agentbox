package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// watch publishes agent state changes and resource samples, but only while
// someone is subscribed to the event stream.
func (s *Server) watch(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.events.subscribers() == 0 {
			continue
		}
		s.refreshAgents(ctx)
		if host, usage, err := s.manager(nil).Usage(ctx, time.Second); err == nil {
			s.events.publish(api.EventUsage, toAPIUsage(host, usage))
		}
	}
}

// refreshAgents publishes an event for every agent whose state changed.
func (s *Server) refreshAgents(ctx context.Context) {
	if s.events.subscribers() == 0 {
		return
	}
	statuses, err := s.manager(nil).List(ctx, "")
	if err != nil {
		return
	}
	s.publishAgentChanges(statuses)
}

func (s *Server) publishAgentChanges(statuses []agent.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, st := range statuses {
		change := api.AgentChange{Ref: st.Ref(), State: st.State, IP: st.IP}
		seen[change.Ref] = true
		if s.lastStates[change.Ref] != change {
			s.lastStates[change.Ref] = change
			s.events.publish(api.EventAgent, change)
		}
	}
	for ref := range s.lastStates {
		if !seen[ref] {
			delete(s.lastStates, ref)
			s.events.publish(api.EventAgent, api.AgentChange{Ref: ref, Removed: true})
		}
	}
}

// agentStates returns the last known state of every agent, sorted by ref.
func (s *Server) agentStates() []api.AgentChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	changes := make([]api.AgentChange, 0, len(s.lastStates))
	for _, change := range s.lastStates {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Ref < changes[j].Ref })
	return changes
}

// eventStream serves the event stream as server-sent events. A new subscriber
// first gets the current state of every agent.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming isn't supported on this connection")
	}
	// Agent states aren't tracked while nobody is subscribed. Bring them up to
	// date before subscribing, so the next check doesn't report them again.
	statuses, err := s.manager(nil).List(r.Context(), "")
	if err == nil {
		s.publishAgentChanges(statuses)
	}
	events, unsubscribe := s.events.subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	send := func(ev api.Event) {
		data, _ := json.Marshal(ev)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		flusher.Flush()
	}
	_, _ = fmt.Fprint(w, ": connected\n\n")
	if err == nil {
		for _, change := range s.agentStates() {
			data, _ := json.Marshal(change)
			send(api.Event{Type: api.EventAgent, Time: time.Now(), Data: data})
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case ev := <-events:
			send(ev)
		}
	}
}
