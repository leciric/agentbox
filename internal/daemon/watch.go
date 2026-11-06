package daemon

import (
	"cmp"
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

// usageAwayEvery is how often the usage event goes out while no client shows
// anything, or the computer is on battery or hot (presence.go), rather than
// every tick: each one samples every agent's CPU for a second.
const usageAwayEvery = 30 * time.Second

// watch publishes agent state changes and resource samples, but only while
// someone is subscribed to the event stream: the app always is.
//
// Each tick is one listing of Incus's instances, which the usage is measured
// against the last tick's and the agents' states are read from. It used to be
// four a tick: one for the states, two a second apart for the usage, and one
// more for balanceCPU, kicked whether anything had changed or not. With the
// app open that was two full listings a second, every instance's state
// gathered by Incus and decoded here, for the daemon's whole life: most of
// its CPU when nothing else happens.
func (s *Server) watch(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var usageAt time.Time
	var last *agent.UsageSample
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.events.subscribers() == 0 {
			last = nil // a rate over the time nobody watched isn't worth showing
			continue
		}
		if s.presence.away() && time.Since(usageAt) < usageAwayEvery {
			// Agent state still follows every tick; the usage sample (a second
			// of CPU measuring) waits, and its rate restarts from nothing.
			s.refreshAgents(ctx)
			last = nil
			continue
		}
		usageAt = time.Now()
		last = s.watchOnce(ctx, last)
	}
}

// watchOnce is one of watch's ticks, measured from last (nil: over a second),
// and returns what the next one measures from.
func (s *Server) watchOnce(ctx context.Context, last *agent.UsageSample) *agent.UsageSample {
	m := s.manager(nil)
	host, usage, now, err := m.UsageSince(ctx, last)
	if err != nil {
		s.refreshAgents(ctx)
		return nil
	}
	if statuses, err := m.ListFrom(ctx, now.Instances); err == nil && s.publishAgentChanges(statuses) {
		// Whatever changed changes how many agents share the VM's cores.
		s.kickCPU()
	}
	s.events.publish(api.EventUsage, toAPIUsage(host, usage))
	return now
}

// refreshAgents publishes an event for every agent whose state changed.
func (s *Server) refreshAgents(ctx context.Context) {
	// Whatever changed changes how many agents share the VM's cores.
	s.kickCPU()
	if s.events.subscribers() == 0 {
		return
	}
	statuses, err := s.manager(nil).List(ctx, "")
	if err != nil {
		return
	}
	s.publishAgentChanges(statuses)
}

// refreshAgentsBehind is refreshAgents for a request the user is waiting
// on: listing every machine again is Incus's slowest answer, and the reply
// doesn't need it, as the app hears what changed as events. Several asked
// for at once make one more listing, not one each.
func (s *Server) refreshAgentsBehind() {
	s.refreshAgain.Store(true)
	if !s.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			for s.refreshAgain.Swap(false) {
				s.refreshAgents(s.background())
			}
			s.refreshing.Store(false)
			// One asked for as this one finished would be lost: it runs now.
			if !s.refreshAgain.Load() || !s.refreshing.CompareAndSwap(false, true) {
				return
			}
		}
	}()
}

// agentRemoved tells the app an agent is gone from the live ones — it is a
// past agent now — without waiting for the next look at every agent.
func (s *Server) agentRemoved(ref string) {
	s.forgetActivity(ref)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lastStates, ref)
	s.events.publish(api.EventAgent, api.AgentChange{Ref: ref, Removed: true})
}

// forgetActivity drops what was kept of a removed agent's last activity,
// which nothing reads once it's gone and nothing else ever let go of.
func (s *Server) forgetActivity(ref string) {
	s.terminalMu.Lock()
	delete(s.terminalActivity, ref)
	s.terminalMu.Unlock()
	if s.idle != nil {
		s.idle.mu.Lock()
		delete(s.idle.touched, ref)
		s.idle.mu.Unlock()
	}
}

// publishAgentChanges reports whether any agent changed.
func (s *Server) publishAgentChanges(statuses []agent.Status) (changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, st := range statuses {
		change := api.AgentChange{Ref: st.Ref(), State: st.State, IP: st.IP, Memory: s.memoryHold(st.Ref()), Reclaimed: s.reclaimedOf(st.Ref(), st.State), WaitingForImage: s.agentsOnImage[st.Ref()]}
		seen[change.Ref] = true
		if !sameChange(s.lastStates[change.Ref], change) {
			s.lastStates[change.Ref] = change
			s.events.publish(api.EventAgent, change)
			changed = true
		}
	}
	for ref := range s.lastStates {
		if !seen[ref] {
			delete(s.lastStates, ref)
			s.events.publish(api.EventAgent, api.AgentChange{Ref: ref, Removed: true})
			changed = true
		}
	}
	return changed
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
//
// ?ping=1 makes its keepalive an event, "ping", rather than a comment, which
// a browser's EventSource never shows the page: the phone's web app
// (desktop/src/renderer/web/reconnect.ts) tells a stream that went silent,
// after the phone slept or a tunnel dropped, from one with nothing to say.
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

	beat := ": keepalive\n\n"
	if r.URL.Query().Get("ping") == "1" {
		beat = "event: ping\ndata: {}\n\n"
	}
	keepalive := time.NewTicker(cmp.Or(s.keepalive, 15*time.Second))
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, beat)
			flusher.Flush()
		case ev := <-events:
			send(ev)
		}
	}
}

// sameChange reports whether two changes say the same.
func sameChange(a, b api.AgentChange) bool {
	am, bm := a.Memory, b.Memory
	a.Memory, b.Memory = nil, nil
	if a != b || (am == nil) != (bm == nil) {
		return false
	}
	return am == nil || *am == *bm
}
