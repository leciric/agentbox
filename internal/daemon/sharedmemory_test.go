package daemon

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// fakeShared stands in for the agents' and leads' parents and a lead chat's
// cgroup, recording what the daemon writes and kills.
type fakeShared struct {
	mu sync.Mutex
	// agentsTotals and leadsTotals are what each parent was written for,
	// one a look.
	agentsTotals, leadsTotals []int64
	agents, leads             agent.MemoryCounts
	chatKills                 int64
	killed                    []string
	removed                   []string
}

func (f *fakeShared) install(s *sharedCgroups) {
	s.on = func() bool { return true }
	s.agentsDir, s.leadsDir = "/fake/agentbox.agents", "/fake/agentbox.leads"
	s.setAgents = func(total int64) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.agentsTotals = append(f.agentsTotals, total)
		return nil
	}
	s.setLeads = func(total int64) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.leadsTotals = append(f.leadsTotals, total)
		return nil
	}
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
	p.srv.vmMemoryNow = func() int64 { return 16 << 30 }
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
	if !slices.Equal(f.agentsTotals, []int64{16 << 30}) || !slices.Equal(f.leadsTotals, []int64{16 << 30}) {
		t.Errorf("the parents were written for %v and %v, want the VM's 16 GiB each", f.agentsTotals, f.leadsTotals)
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

// The agents' parent is sized from what the VM has now, not from the cap it
// may grow to, and follows it at the next look as the host plugs memory in
// or takes it back; the leads' stays a share of what the VM can reach. On 20
// Nov 2026 a VM at 13 of its 16 GiB had the agents at high 13 and max 14 GiB,
// all of it, and the leads and the daemon stalled on the file cache.
func TestAgentsLimitsFollowWhatTheVMHasNow(t *testing.T) {
	t.Parallel()
	const gib = int64(1) << 30
	f := &fakeShared{}
	p := newPressureTest(t, func(s *Server) { f.install(s.shared) })
	var mu sync.Mutex
	has := 13 * gib
	p.srv.vmMemory = func() int64 { return 16 * gib }
	p.srv.vmMemoryNow = func() int64 {
		mu.Lock()
		defer mu.Unlock()
		return has
	}
	resize := func(n int64) {
		mu.Lock()
		has = n
		mu.Unlock()
	}
	ctx := context.Background()
	look := func(what string, wantHigh, wantMax, wantLeads int64) {
		t.Helper()
		p.srv.watchMemory(ctx)
		f.mu.Lock()
		total, leads := f.agentsTotals[len(f.agentsTotals)-1], f.leadsTotals[len(f.leadsTotals)-1]
		f.mu.Unlock()
		l := agent.AgentsLimits(total, 0)
		if l.High != wantHigh || l.Max != wantMax {
			t.Errorf("%s: the agents' parent was written for %s: high %s, max %s; want %s, %s", what,
				agent.HumanBytes(total), agent.HumanBytes(l.High), agent.HumanBytes(l.Max), agent.HumanBytes(wantHigh), agent.HumanBytes(wantMax))
		}
		if leads != wantLeads {
			t.Errorf("%s: the leads' parent was written for %s, want %s", what, agent.HumanBytes(leads), agent.HumanBytes(wantLeads))
		}
	}

	// Under its cap, with no word from the host: 3 and 2 GiB of the 13 it
	// has are left to the rest (it was 13 and 14 GiB, from the cap).
	look("a 16 GiB cap, 13 GiB plugged", 10*gib, 11*gib, 16*gib)
	// The host plugs more in: the limits follow at the next look.
	resize(15 * gib)
	look("grown to 15 GiB", 12*gib, 13*gib, 16*gib)
	// The host takes some back.
	resize(10 * gib)
	look("shrunk to 10 GiB", 7*gib, 8*gib, 16*gib)
	// The host can spare only 14 GiB: the leads' share follows that; the
	// agents' still follows what the VM has.
	p.srv.hostLoad.mu.Lock()
	p.srv.hostLoad.last, p.srv.hostLoad.got = api.HostLoad{VMCeiling: 14 * gib}, time.Now()
	p.srv.hostLoad.mu.Unlock()
	look("held at 10 GiB by a host that can spare 14", 7*gib, 8*gib, 14*gib)
	// A small VM keeps #787's floors: half and three quarters of it.
	resize(4 * gib)
	look("a 4 GiB VM", 2*gib, 3*gib, 14*gib)
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
