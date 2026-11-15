package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/chat"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// fakeShared stands in for the agents' and leads' parents and a lead chat's
// cgroup, recording what the daemon writes and kills.
type fakeShared struct {
	mu            sync.Mutex
	totals        []int64 // what each parent was written for, agents then leads
	agents, leads agent.MemoryCounts
	chatKills     int64
	killed        []string
	removed       []string
}

func (f *fakeShared) install(s *sharedCgroups) {
	s.on = func() bool { return true }
	s.agentsDir, s.leadsDir = "/fake/agentbox.agents", "/fake/agentbox.leads"
	s.setAgents = func(total int64) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.totals = append(f.totals, total)
		return nil
	}
	s.setLeads = s.setAgents
	s.events = func(dir string) (agent.MemoryCounts, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if dir == s.agentsDir {
			return f.agents, true
		}
		return f.leads, true
	}
	s.place = func(ref string, pid int) (string, error) { return "/fake/agentbox.leads/chat." + ref, nil }
	s.kills = func(string) (int64, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.chatKills, true
	}
	s.kill = func(dir string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.killed = append(f.killed, dir)
		return nil
	}
	s.remove = func(dir string, _ time.Duration) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.removed = append(f.removed, dir)
		return nil
	}
}

// An agent killed while every agent together was at the agents' shared
// limit is told that, not that the VM ran out; both parents get the VM's
// size on every look.
func TestSharedLimitKillTold(t *testing.T) {
	t.Parallel()
	f := &fakeShared{agents: agent.MemoryCounts{Limit: 12 << 30}}
	p := newPressureTest(t, func(s *Server) { f.install(s.shared) })
	ctx := context.Background()
	var mu sync.Mutex
	counts := agent.MemoryCounts{Kills: 1, Limit: 8 << 30}
	p.srv.vmMemory = func() int64 { return 16 << 30 }
	p.srv.memoryEvents = func(string) (agent.MemoryCounts, bool) {
		mu.Lock()
		defer mu.Unlock()
		return counts, true
	}
	oomEvents := func() []memory.Event {
		events, err := p.srv.memory().Events(ctx, "hello-stack", memory.EventFilter{Types: []string{"oom_kill"}})
		if err != nil {
			t.Fatal(err)
		}
		return events
	}
	p.srv.watchMemory(ctx)
	f.mu.Lock()
	if len(f.totals) != 2 || f.totals[0] != 16<<30 || f.totals[1] != 16<<30 {
		t.Errorf("the parents were written for %v, want the VM's 16 GiB each", f.totals)
	}
	f.agents.OwnLimit = 1 // every agent together reached it
	f.mu.Unlock()
	mu.Lock()
	counts.Kills = 2
	mu.Unlock()
	p.srv.watchMemory(ctx)
	waitFor(t, "the kill to be told", func() bool { return len(oomEvents()) > 0 })
	ev := string(oomEvents()[0].Payload)
	for _, want := range []string{`"cause":"agents"`, `"shared_limit":12884901888`, `"own_limit":false`} {
		if !strings.Contains(ev, want) {
			t.Errorf("the kill reads %s, lacks %s", ev, want)
		}
	}
}

// A lead chat whose process memory killed has the rest of its cgroup killed
// at the next look, its turn told so with the leads' limit, and its cgroup
// cleared once it exits.
func TestLeadChatKilledForMemory(t *testing.T) {
	t.Parallel()
	f := &fakeShared{leads: agent.MemoryCounts{Limit: 4 << 30}}
	p := newPressureTest(t, func(s *Server) { f.install(s.shared) })
	p.srv.vmMemory = func() int64 { return 16 << 30 }
	p.srv.watchMemory(context.Background())

	exited := make(chan struct{})
	proc := &chat.Process{Wait: func() error { <-exited; return nil }}
	lead := state.Agent{Project: "hello", Name: state.LeadName}
	p.srv.capLeadChat(lead, proc, 4242)
	if proc.Killed == nil {
		t.Fatal("a placed lead chat says nothing of what killed it")
	}
	// The kernel kills a process of the chat for the leads' limit.
	f.mu.Lock()
	f.chatKills, f.leads.OwnLimit = 1, 1
	f.mu.Unlock()
	p.srv.watchMemory(context.Background())
	f.mu.Lock()
	killed := append([]string(nil), f.killed...)
	f.mu.Unlock()
	if want := "/fake/agentbox.leads/chat." + lead.Ref(); len(killed) != 1 || killed[0] != want {
		t.Fatalf("killed %q, want its cgroup %q", killed, want)
	}
	p.srv.watchMemory(context.Background())
	if f.mu.Lock(); len(f.killed) != 1 {
		t.Errorf("killed again on the next look: %q", f.killed)
	}
	f.mu.Unlock()
	close(exited)
	why := proc.Killed()
	for _, want := range []string{"killed: out of memory", "the project chats together reached their limit of 4.0 GiB of this VM's 16.0 GiB", "Send a message to start it again"} {
		if !strings.Contains(why, want) {
			t.Errorf("Killed() = %q, lacks %q", why, want)
		}
	}
	waitFor(t, "its cgroup to be cleared", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.removed) > 0
	})
}

// A lead chat that exits with nothing killed says nothing of memory.
func TestLeadChatExitingByItselfIsNotKilled(t *testing.T) {
	t.Parallel()
	f := &fakeShared{}
	p := newPressureTest(t, func(s *Server) { f.install(s.shared) })
	exited := make(chan struct{})
	proc := &chat.Process{Wait: func() error { <-exited; return nil }}
	p.srv.capLeadChat(state.Agent{Project: "hello", Name: state.LeadName}, proc, 1)
	close(exited)
	if why := proc.Killed(); why != "" {
		t.Errorf("Killed() = %q, want nothing", why)
	}
}

func TestLeadKilledWhy(t *testing.T) {
	if got := leadKilledWhy(false, 4<<30, 16<<30); !strings.Contains(got, "killed: out of memory (the VM ran out of memory)") {
		t.Errorf("the VM's kill reads %q", got)
	}
	if got := leadKilledWhy(true, 4<<30, 0); !strings.Contains(got, "reached their limit of 4.0 GiB)") {
		t.Errorf("the leads' kill without the VM's size reads %q", got)
	}
}
