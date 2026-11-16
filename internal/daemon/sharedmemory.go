package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// The shared ceilings (agent/sharedmemory.go): every agent's machine in
// agentbox.agents and every lead chat in agentbox.leads, each parent with a
// memory.max from the VM's size, written by watchMemory on every look so they
// follow the VM as it grows or shrinks. A kill under either names the limit
// it was for: an agent is told every agent together reached theirs (rather
// than that the VM ran out), and a lead chat's turn fails with "killed: out
// of memory" and the leads' limit, its whole process tree killed, the next
// message starting it again.

// sharedHitFor is how long after a parent reached its memory.max a kill
// below it is taken to be for it: the kernel counts the parent's limit
// reached before it kills, and the watch may read the two on either side of
// a look.
const sharedHitFor = 10 * time.Second

// sharedCgroups is the daemon's side of the ceilings: agent's functions, or
// a test's, and what the watch has seen, under mu.
type sharedCgroups struct {
	on        func() bool
	setAgents func(total int64) error
	setLeads  func(total int64) error
	events    func(dir string) (agent.MemoryCounts, bool)
	place     func(ref string, pid int) (string, error)
	kills     func(dir string) (int64, bool)
	kill      func(dir string) error
	remove    func(dir string, wait time.Duration) error
	agentsDir string
	leadsDir  string

	mu sync.Mutex
	// agents and leads are each parent's counts at the last look, and
	// agentsHit and leadsHit when it last reached its memory.max.
	agents, leads           agent.MemoryCounts
	agentsKnown, leadsKnown bool
	agentsHit, leadsHit     time.Time
	// swept is whether the lead chats a daemon before this one left were
	// cleared.
	swept bool
	// chats is each running lead chat's cgroup, by directory.
	chats map[string]*leadChat
}

// leadChat is a lead chat's cgroup while its process runs.
type leadChat struct {
	ref string
	// killed is whether the watch killed it for memory.
	killed bool
	// read is closed once the process exited and what killed it was read:
	// kills, the cgroup's oom_kill count, and leadsHit, whether the leads'
	// limit was reached then.
	read chan struct{}
	// leadsOOM is the leads' parent's count of its limit reached as the
	// chat started.
	leadsOOM int64
	kills    int64
	leadsHit bool
	limit    int64
}

func newSharedCgroups() *sharedCgroups {
	return &sharedCgroups{
		on:        agent.SharedCgroups,
		setAgents: agent.SetAgentsGroup,
		setLeads:  agent.SetLeadsGroup,
		events:    agent.GroupEvents,
		place:     agent.PlaceLeadChat,
		kills:     agent.LeadChatKills,
		kill:      agent.KillCgroup,
		remove:    agent.RemoveLeadChat,
		agentsDir: agent.AgentsDir,
		leadsDir:  agent.LeadsDir,
		chats:     map[string]*leadChat{},
	}
}

// watchShared writes both parents' limits for a VM of total bytes, notes
// when either reached its memory.max, and kills a lead chat the kernel
// killed a process of. It reports whether the agents' parent reached its
// limit lately, and that limit, for the agents' notices.
func (s *Server) watchShared(total int64) (agentsHit bool, agentsLimit int64) {
	sc := s.shared
	if !sc.on() {
		return false, 0
	}
	if err := sc.setAgents(total); err != nil {
		s.logOnce("agentbox.agents", fmt.Sprintf("memory: the agents' shared limit: %v", err))
	}
	if err := sc.setLeads(total); err != nil {
		s.logOnce("agentbox.leads", fmt.Sprintf("memory: the lead chats' shared limit: %v", err))
	}
	now := time.Now()
	agents, aok := sc.events(sc.agentsDir)
	leads, lok := sc.events(sc.leadsDir)
	sc.mu.Lock()
	if aok {
		if sc.agentsKnown && agents.OwnLimit > sc.agents.OwnLimit {
			sc.agentsHit = now
		}
		sc.agents, sc.agentsKnown = agents, true
	}
	if lok {
		if sc.leadsKnown && leads.OwnLimit > sc.leads.OwnLimit {
			sc.leadsHit = now
		}
		sc.leads, sc.leadsKnown = leads, true
	}
	agentsHit = !sc.agentsHit.IsZero() && now.Sub(sc.agentsHit) <= sharedHitFor
	sweep := !sc.swept
	sc.swept = true
	var kill []string
	for dir, c := range sc.chats {
		if n, ok := sc.kills(dir); ok && n > 0 && !c.killed {
			c.killed = true
			kill = append(kill, dir)
		}
	}
	refs := map[string]string{}
	for _, dir := range kill {
		refs[dir] = sc.chats[dir].ref
	}
	sc.mu.Unlock()
	if sweep {
		s.sweepLeadChats()
	}
	// The kernel killed one process of the tree (all of it, with
	// memory.oom.group); the rest goes with it, so the adapter ends rather
	// than wait on a tool that is gone.
	for _, dir := range kill {
		s.logf("memory: %s's chat had a process killed for memory; killing what is left of it", refs[dir])
		if err := sc.kill(dir); err != nil {
			s.logf("memory: killing %s's chat: %v", refs[dir], err)
		}
	}
	return agentsHit, agents.Limit
}

// sweepLeadChats removes the cgroups lead chats of an earlier daemon left
// behind, once they are empty: they ended with it.
func (s *Server) sweepLeadChats() {
	entries, err := os.ReadDir(s.shared.leadsDir)
	if err != nil {
		return
	}
	s.shared.mu.Lock()
	defer s.shared.mu.Unlock()
	for _, e := range entries {
		dir := filepath.Join(s.shared.leadsDir, e.Name())
		if _, ours := s.shared.chats[dir]; e.IsDir() && !ours {
			_ = s.shared.remove(dir, 0)
		}
	}
}

// capLeadChat puts a lead chat's adapter, started as pid, in a cgroup of its
// own under the leads' limit, has its turn say so when memory killed it, and
// clears the cgroup when it exits.
func (s *Server) capLeadChat(a state.Agent, proc *chat.Process, pid int) {
	sc := s.shared
	if !sc.on() {
		return
	}
	dir, err := sc.place(a.Ref(), pid)
	if dir == "" {
		if err != nil {
			s.logOnce("lead cgroup "+a.Ref(), fmt.Sprintf("memory: %s's chat runs outside the lead chats' limit: %v", a.Ref(), err))
		}
		return
	}
	if err != nil {
		s.logOnce("lead cgroup "+a.Ref(), fmt.Sprintf("memory: %s's chat: %v", a.Ref(), err))
	}
	c := &leadChat{ref: a.Ref(), read: make(chan struct{})}
	if leads, ok := sc.events(sc.leadsDir); ok {
		c.leadsOOM = leads.OwnLimit
	}
	sc.mu.Lock()
	sc.chats[dir] = c
	sc.mu.Unlock()
	go func() {
		_ = proc.Wait()
		kills, _ := sc.kills(dir)
		leads, ok := sc.events(sc.leadsDir)
		sc.mu.Lock()
		c.kills = kills
		// Reached since the chat started, since the last look, or lately.
		c.leadsHit = (ok && leads.OwnLimit > c.leadsOOM) ||
			(ok && sc.leadsKnown && leads.OwnLimit > sc.leads.OwnLimit) ||
			(!sc.leadsHit.IsZero() && time.Since(sc.leadsHit) <= sharedHitFor)
		c.limit = leads.Limit
		close(c.read)
		sc.mu.Unlock()
		if err := sc.remove(dir, 5*time.Second); err != nil {
			s.logf("memory: clearing %s's chat's cgroup: %v", a.Ref(), err)
		}
		sc.mu.Lock()
		delete(sc.chats, dir)
		sc.mu.Unlock()
	}()
	proc.Killed = func() string {
		select {
		case <-c.read:
		case <-time.After(5 * time.Second):
			return ""
		}
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if c.kills == 0 && !c.killed {
			return ""
		}
		return leadKilledWhy(c.leadsHit, c.limit, s.vmMemory())
	}
}

// leadKilledWhy is what a lead chat's turn says when memory killed its tool:
// which limit, and that the next message starts it again.
func leadKilledWhy(leadsHit bool, limit, total int64) string {
	why := "the VM ran out of memory"
	if leadsHit && limit > 0 {
		why = fmt.Sprintf("the project chats together reached their limit of %s", agent.HumanBytes(limit))
		if total > 0 {
			why += fmt.Sprintf(" of this VM's %s", agent.HumanBytes(total))
		}
	}
	return "killed: out of memory (" + why + "), with everything it had started. " +
		"Send a message to start it again; if a command it ran needs that much, have an agent run it in its machine."
}
