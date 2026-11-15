package daemon

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// An agent's live resources: its CPU, memory and disk, sampled every
// agentResourceEvery from its cgroup, but only while a client's lease asks
// for them (presence.go), and kept for agentResourceWindow in memory, for the
// sparklines on its page. Nothing is stored: a restart forgets them, and an
// agent nobody has watched for a window is forgotten too.

const (
	agentResourceEvery  = 2 * time.Second
	agentResourceWindow = 10 * time.Minute
)

type agentSampler struct {
	every  time.Duration // 0 runs no loop: tests call sample themselves
	window time.Duration
	now    func() time.Time
	// read takes one reading of an agent's machine (agent.ReadAgentCounters).
	read func(instance string) (agent.AgentCounters, bool)

	mu     sync.Mutex
	series map[string]*agentSeries // by ref
}

type agentSeries struct {
	instance string
	last     agent.AgentCounters // the reading the next point is a delta from
	points   []api.AgentResourcePoint
}

func newAgentSampler() *agentSampler {
	return &agentSampler{every: agentResourceEvery, window: agentResourceWindow, now: time.Now, read: agent.ReadAgentCounters, series: map[string]*agentSeries{}}
}

// sampleAgentResources is the loop Run starts. A tick with no agent watched
// reads nothing.
func (s *Server) sampleAgentResources(ctx context.Context) {
	if s.agentSamples.every <= 0 {
		return
	}
	ticker := time.NewTicker(s.agentSamples.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleWatched(ctx)
		}
	}
}

// sampleWatched takes one sample of every agent a lease asks for, and
// publishes each.
func (s *Server) sampleWatched(ctx context.Context) {
	refs := s.presence.sampling()
	instances := make(map[string]string, len(refs))
	for _, ref := range refs {
		project, name, ok := strings.Cut(ref, "/")
		if !ok {
			continue
		}
		a, err := s.store.Agent(ctx, project, name)
		if err != nil || a.IsLead() {
			continue
		}
		instances[ref] = a.Instance
	}
	for ref, point := range s.agentSamples.sample(instances) {
		s.events.publish(api.EventAgentResources, api.AgentResourcesEvent{Ref: ref, Point: point})
	}
}

// sample reads every agent in instances (ref to instance), and gives the new
// point of each that has one. An agent's first reading, or its first after a
// gap, is only the base the next is measured from. Series older than the
// window are forgotten.
func (a *agentSampler) sample(instances map[string]string) map[string]api.AgentResourcePoint {
	now := a.now()
	readings := make(map[string]agent.AgentCounters, len(instances))
	for ref, instance := range instances {
		if c, ok := a.read(instance); ok {
			c.At = now
			readings[ref] = c
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]api.AgentResourcePoint{}
	for ref, c := range readings {
		sr, ok := a.series[ref]
		if !ok || sr.instance != instances[ref] {
			sr = &agentSeries{instance: instances[ref]}
			a.series[ref] = sr
		}
		// A reading more than two ticks old (nobody watched in between) would
		// average over the gap: it only starts the series again.
		if !sr.last.At.IsZero() && now.Sub(sr.last.At) <= 2*a.every+a.every/2 {
			r := agent.Rates(sr.last, c)
			p := api.AgentResourcePoint{At: now.UTC(), CPU: r.CPU, Memory: r.Memory, Swap: r.Swap, DiskRead: r.DiskRead, DiskWrite: r.DiskWrite}
			sr.points = append(sr.points, p)
			out[ref] = p
		}
		sr.last = c
	}
	cut := now.Add(-a.window)
	for ref, sr := range a.series {
		old := 0
		for old < len(sr.points) && !sr.points[old].At.After(cut) {
			old++
		}
		sr.points = slices.Delete(sr.points, 0, old)
		if len(sr.points) == 0 && sr.last.At.Before(cut) {
			delete(a.series, ref)
		}
	}
	return out
}

// history is an agent's points, oldest first.
func (a *agentSampler) history(ref string) []api.AgentResourcePoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	sr, ok := a.series[ref]
	if !ok {
		return []api.AgentResourcePoint{}
	}
	return slices.Clone(sr.points)
}

// agentResources is GET /v1/agents/{project}/{agent}/resources.
func (s *Server) agentResources(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	ref := a.Ref()
	return writeJSON(w, http.StatusOK, api.AgentResourceHistory{
		Ref:             ref,
		IntervalSeconds: int(cmp.Or(s.agentSamples.every, agentResourceEvery) / time.Second),
		WindowSeconds:   int(s.agentSamples.window / time.Second),
		Sampling:        slices.Contains(s.presence.sampling(), ref),
		Points:          s.agentSamples.history(ref),
	})
}
