package daemon

import (
	"context"
	"net/http"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// agentDiskTTL is how long an agent's disk sizes are cached. Measuring means
// an Incus query and a walk of the whole worktree, node_modules and all, and
// the info card that shows them opens on every hover over the agents rail:
// disk moves slowly enough that a minute-old number says the same thing.
const agentDiskTTL = time.Minute

// agentDiskCache remembers each agent's disk sizes for agentDiskTTL, and
// measures an agent once however many cards ask at the same time: a second
// request while the first is still walking the worktree waits for its answer
// instead of starting a walk of its own.
type agentDiskCache struct {
	mu      sync.Mutex
	entries map[string]*agentDiskEntry
	now     func() time.Time
	measure func(ctx context.Context, a state.Agent) agent.AgentDisk
}

type agentDiskEntry struct {
	done chan struct{} // closed once disk and at are set
	disk agent.AgentDisk
	at   time.Time
}

func newAgentDiskCache(measure func(ctx context.Context, a state.Agent) agent.AgentDisk) *agentDiskCache {
	return &agentDiskCache{entries: map[string]*agentDiskEntry{}, now: time.Now, measure: measure}
}

func (c *agentDiskCache) get(ctx context.Context, a state.Agent) (agent.AgentDisk, time.Time) {
	ref := a.Ref()
	c.mu.Lock()
	e, ok := c.entries[ref]
	if ok {
		select {
		case <-e.done:
			if c.now().Sub(e.at) >= agentDiskTTL {
				ok = false
			}
		default: // still being measured: wait for it below
		}
	}
	if !ok {
		e = &agentDiskEntry{done: make(chan struct{})}
		c.entries[ref] = e
		c.mu.Unlock()
		// The measurement isn't tied to the request that happened to start it:
		// others may be waiting on it, and the answer is worth caching anyway.
		disk := c.measure(context.WithoutCancel(ctx), a)
		c.mu.Lock()
		e.disk, e.at = disk, c.now()
		close(e.done)
		c.mu.Unlock()
		return disk, e.at
	}
	c.mu.Unlock()
	select {
	case <-e.done:
		return e.disk, e.at
	case <-ctx.Done():
		return agent.AgentDisk{MachineErr: ctx.Err(), WorktreeErr: ctx.Err()}, c.now()
	}
}

// forget drops an agent's cached sizes, when it's destroyed, so a new agent
// could never be shown them (names aren't reused, but the map shouldn't grow
// with every agent a daemon has ever seen).
func (c *agentDiskCache) forget(ref string) {
	c.mu.Lock()
	delete(c.entries, ref)
	c.mu.Unlock()
}

// agentDisk serves an agent's machine and worktree sizes, for its info card.
func (s *Server) agentDisk(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	disk, at := s.disks.get(r.Context(), a)
	out := api.AgentDisk{MeasuredAt: at.UTC()}
	if disk.MachineErr == nil {
		out.Machine = &disk.Machine
	}
	if disk.WorktreeErr == nil {
		out.Worktree = &disk.Worktree
	}
	return writeJSON(w, http.StatusOK, out)
}
