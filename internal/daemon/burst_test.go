package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

func (q *queueTest) agentRow(t *testing.T, project, name, size string) state.Agent {
	t.Helper()
	a, err := q.srv.store.Agent(context.Background(), project, name)
	if err != nil {
		t.Fatal(err)
	}
	a.Size = size
	return a
}

// Heavy phases share the pool left beside the baselines: a lease is given
// while it fits, shared by an agent's phases, waits when the pool is full,
// says why when the wait runs out, and goes to the next in line when the last
// phase holding it ends.
func TestBurstPool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// 16 GiB, less the 2 the VM keeps: 14, of which three baselines hold 6.
	q := newQueueTest(t, 16*gib, map[string]int64{"p": 2 * gib},
		runningInstances(agent.InstanceName("p", "a1"), agent.InstanceName("p", "a2"), agent.InstanceName("p", "a3")))
	q.addProject(t, "p")
	for _, n := range []string{"a1", "a2", "a3"} {
		q.addRunning(t, "p", n)
	}
	heavy, a2, a3 := q.agentRow(t, "p", "a1", agent.SizeHeavy), q.agentRow(t, "p", "a2", ""), q.agentRow(t, "p", "a3", "")
	s := q.srv

	lease, err := s.leaseBurst(ctx, heavy, "test", time.Minute, time.Second)
	if err != nil || !lease.Granted || lease.Bytes != 6*gib || lease.Env["GOFLAGS"] == "" {
		t.Fatalf("the heavy agent's lease = %+v, %v; want 6 GiB, with its env", lease, err)
	}
	// Its second phase shares the lease.
	if lease, err = s.leaseBurst(ctx, heavy, "browser", time.Minute, time.Second); err != nil || lease.Bytes != 6*gib {
		t.Fatalf("its second phase = %+v, %v", lease, err)
	}
	if lease, err = s.leaseBurst(ctx, a2, "test", time.Minute, time.Second); err != nil || !lease.Granted || lease.Bytes != 2*gib {
		t.Fatalf("a2's lease = %+v, %v; want its 2 GiB, which fit", lease, err)
	}
	// 6 baseline + 6 + 2 leased of 14: a3's 2 doesn't fit.
	lease, err = s.leaseBurst(ctx, a3, "test", time.Minute, 50*time.Millisecond)
	if err != nil || lease.Granted || lease.Why == "" {
		t.Fatalf("a3's lease with the pool full = %+v, %v; want a reason", lease, err)
	}
	t.Logf("why: %s", lease.Why)

	got := make(chan bool, 1)
	go func() {
		l, err := s.leaseBurst(ctx, a3, "test", time.Minute, 10*time.Second)
		got <- err == nil && l.Granted
	}()
	waitFor(t, "a3 in line", func() bool {
		s.burst.mu.Lock()
		defer s.burst.mu.Unlock()
		return len(s.burst.waiting) == 1
	})
	if s.burst.release(heavy.Ref(), "test") == 0 {
		t.Fatal("the heavy agent's lease went back with a phase still holding it")
	}
	s.dropBursts(ctx, "nobody") // a look at the line: still no room
	select {
	case <-got:
		t.Fatal("a3 got a lease while the heavy agent still held its own")
	case <-time.After(50 * time.Millisecond):
	}
	if s.burst.release(heavy.Ref(), "browser") != 0 {
		t.Fatal("the lease outlived its last phase")
	}
	s.grantBursts(ctx)
	if !<-got {
		t.Fatal("a3 didn't get the lease given back")
	}

	// A phase that's never released expires; an agent that stops gives
	// everything back.
	if s.burst.expire(time.Now().Add(2*time.Minute), func(string) bool { return true }); s.burst.leaseOf(a2.Ref()) != 0 {
		t.Error("a lease past its TTL wasn't dropped")
	}
	s.dropBursts(ctx, a3.Ref())
	if s.burst.leaseOf(a3.Ref()) != 0 {
		t.Error("a stopped agent kept its lease")
	}
}

// A lone burst bigger than the pool still runs, by itself.
func TestBurstBiggerThanThePool(t *testing.T) {
	t.Parallel()
	q := newQueueTest(t, 8*gib, map[string]int64{"p": 2 * gib}, runningInstances(agent.InstanceName("p", "a1")))
	q.addProject(t, "p")
	q.addRunning(t, "p", "a1")
	lease, err := q.srv.leaseBurst(context.Background(), q.agentRow(t, "p", "a1", agent.SizeHeavy), "k", time.Minute, time.Second)
	if err != nil || !lease.Granted {
		t.Errorf("a lone heavy lease on a small VM = %+v, %v", lease, err)
	}
}

// The in-agent API: an agent takes and gives back its lease over its own
// socket, and is known by it.
func TestBurstOverTheAgentAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newQueueTest(t, 16*gib, map[string]int64{"p": 2 * gib}, runningInstances(agent.InstanceName("p", "a1")))
	q.addProject(t, "p")
	q.addRunning(t, "p", "a1")
	a := q.agentRow(t, "p", "a1", "")
	if err := q.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	inAgent := api.NewClient(q.srv.agentSocketPath(a.Instance))
	if _, err := inAgent.AcquireBurst(ctx, api.BurstRequest{}); err == nil {
		t.Error("a lease with no key was given")
	}
	lease, err := inAgent.AcquireBurst(ctx, api.BurstRequest{Key: "go-test", TTLSeconds: 60})
	if err != nil || !lease.Granted || lease.Bytes != 2*gib {
		t.Fatalf("lease = %+v, %v", lease, err)
	}
	if q.srv.burst.leaseOf("p/a1") != 2*gib {
		t.Error("the lease isn't the calling agent's")
	}
	left, err := inAgent.ReleaseBurst(ctx, "go-test")
	if err != nil || left.Granted || len(left.Env) != 0 {
		t.Errorf("after release = %+v, %v; want nothing held", left, err)
	}
}
