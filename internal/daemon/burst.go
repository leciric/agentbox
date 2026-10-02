package daemon

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The burst pool. Admission counts each agent's baseline only (admission.go);
// its heavy phases (a test run, a build, the browser, a recording) take its
// burst from the pool as a lease and give it back when they end. Inside the
// agent, a Claude Code hook takes and gives back leases around its heavy tool
// calls, and `agentbox heavy -- <cmd>` around anything else (cli/heavy.go).
//
// An agent holds one lease however many of its phases run at once: each
// phase holds a key on it, and the lease goes back with the last key. A key
// that is neither renewed nor released expires (its TTL), the backstop for a
// phase that crashed; stopping or destroying the agent gives everything back.
// Agents waiting for a lease get one in the order they asked: none passes
// another, so a heavy agent's big burst isn't starved by small ones.

const (
	burstTTL     = 15 * time.Minute
	burstMaxTTL  = time.Hour
	burstMaxWait = 10 * time.Minute
)

type burstLease struct {
	project string
	bytes   int64
	keys    map[string]time.Time // each phase's key, until it expires
}

type burstWaiter struct {
	ref, project, key string
	bytes             int64
	ttl               time.Duration
	granted           chan struct{}
}

type burstPool struct {
	mu      sync.Mutex
	leases  map[string]*burstLease // by agent ref
	waiting []*burstWaiter         // in the order they asked
}

func newBurstPool() *burstPool {
	return &burstPool{leases: map[string]*burstLease{}}
}

// leaseOf is what ref holds of the pool now, 0 for nothing.
func (p *burstPool) leaseOf(ref string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l := p.leases[ref]; l != nil {
		return l.bytes
	}
	return 0
}

// renew adds or renews key on ref's lease, if it holds one.
func (p *burstPool) renew(ref, key string, ttl time.Duration, now time.Time) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.leases[ref]
	if l == nil {
		return 0, false
	}
	l.keys[key] = now.Add(ttl)
	return l.bytes, true
}

// release gives key back, and the lease with its last key. It reports what
// ref holds afterwards.
func (p *burstPool) release(ref, key string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.leases[ref]
	if l == nil {
		return 0
	}
	delete(l.keys, key)
	if len(l.keys) == 0 {
		delete(p.leases, ref)
		return 0
	}
	return l.bytes
}

// drop gives back everything ref holds or waits for.
func (p *burstPool) drop(ref string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.leases, ref)
	kept := p.waiting[:0]
	for _, w := range p.waiting {
		if w.ref != ref {
			kept = append(kept, w)
		}
	}
	p.waiting = kept
}

// expire drops the keys whose TTL ran out, and the leases of agents that no
// longer hold memory (holding says which still do). It reports whether
// anything went back.
func (p *burstPool) expire(now time.Time, holding func(ref string) bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	for ref, l := range p.leases {
		for key, until := range l.keys {
			if now.After(until) {
				delete(l.keys, key)
			}
		}
		if len(l.keys) == 0 || !holding(ref) {
			delete(p.leases, ref)
			changed = true
		}
	}
	return changed
}

// withdraw takes w out of the wait, and reports whether it was granted first.
func (p *burstPool) withdraw(w *burstWaiter) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, o := range p.waiting {
		if o == w {
			p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
			return false
		}
	}
	return true
}

// grantBursts gives leases to the agents waiting, in order, while each fits
// (agent.BurstFits), and stops at the first that doesn't.
func (s *Server) grantBursts(ctx context.Context) {
	s.queueMu.Lock()
	plan, err := s.planAdmission(ctx, nil)
	s.queueMu.Unlock()
	if err != nil {
		s.logf("burst pool: %v", err)
		return
	}
	p := s.burst
	p.mu.Lock()
	defer p.mu.Unlock()
	holders := append([]agent.Holder(nil), plan.holders...)
	for len(p.waiting) > 0 {
		w := p.waiting[0]
		if l := p.leases[w.ref]; l != nil {
			// A key of an agent that got its lease meanwhile shares it.
			l.keys[w.key] = time.Now().Add(w.ttl)
		} else {
			if !agent.BurstFits(plan.total, holders, w.bytes) {
				return
			}
			p.leases[w.ref] = &burstLease{project: w.project, bytes: w.bytes, keys: map[string]time.Time{w.key: time.Now().Add(w.ttl)}}
			holders = withLease(holders, w.ref, w.project, w.bytes)
		}
		p.waiting = p.waiting[1:]
		close(w.granted)
	}
}

// withLease is holders with ref's lease set to bytes.
func withLease(holders []agent.Holder, ref, project string, bytes int64) []agent.Holder {
	for i := range holders {
		if holders[i].Ref == ref {
			holders[i].Lease = bytes
			return holders
		}
	}
	return append(holders, agent.Holder{Ref: ref, Project: project, Lease: bytes})
}

// burstWhy says in one line why a lease of bytes waited, from what the pool
// holds now.
func (s *Server) burstWhy(ctx context.Context, bytes int64, waited time.Duration) string {
	s.queueMu.Lock()
	plan, err := s.planAdmission(ctx, nil)
	s.queueMu.Unlock()
	if err != nil {
		return fmt.Sprintf("waited %s for %s GB of burst memory", waited.Round(time.Second), agent.GB(bytes))
	}
	free, leased, agents := agent.BurstFree(plan.total, plan.holders)
	return fmt.Sprintf("waited %s for %s GB of burst memory: %d %s hold %s GB of it and %s GB is free; try again later",
		waited.Round(time.Second), agent.GB(bytes), agents, plural(agents, "agent", "agents"), agent.GB(leased), agent.GB(free))
}

// burstEnv is the environment ref's commands run with while it holds bytes.
func burstEnv(bytes int64) map[string]string {
	if bytes <= 0 {
		return nil
	}
	return agent.BurstEnv(bytes, runtime.NumCPU())
}

// acquireBurst is POST /v1/self/burst: a lease for the calling agent, now if
// it holds one or the pool has room, else when it does, for as long as the
// request asks to wait.
func (s *Server) acquireBurst(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req api.BurstRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		key := strings.TrimSpace(req.Key)
		if key == "" {
			return fmt.Errorf("a lease needs a key naming its phase")
		}
		ttl := burstTTL
		if req.TTLSeconds > 0 {
			ttl = min(time.Duration(req.TTLSeconds)*time.Second, burstMaxTTL)
		}
		wait := burstMaxWait
		if req.WaitSeconds > 0 {
			wait = min(time.Duration(req.WaitSeconds)*time.Second, burstMaxWait)
		}
		ctx := r.Context()
		a, err := s.store.AgentByInstance(ctx, instance)
		if err != nil {
			return err
		}
		lease, err := s.leaseBurst(ctx, a, key, ttl, wait)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, lease)
	}
}

func (s *Server) leaseBurst(ctx context.Context, a state.Agent, key string, ttl, wait time.Duration) (api.BurstLease, error) {
	ref := a.Ref()
	if bytes, ok := s.burst.renew(ref, key, ttl, time.Now()); ok {
		return api.BurstLease{Granted: true, Bytes: bytes, Env: burstEnv(bytes)}, nil
	}
	shape, err := s.projectShape(ctx, a.Project)
	if err != nil {
		return api.BurstLease{}, err
	}
	bytes := agent.Burst(a.Size, shape)
	waiter := &burstWaiter{ref: ref, project: a.Project, key: key, bytes: bytes, ttl: ttl, granted: make(chan struct{})}
	s.burst.mu.Lock()
	s.burst.waiting = append(s.burst.waiting, waiter)
	s.burst.mu.Unlock()
	s.grantBursts(ctx)
	start := time.Now()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-waiter.granted:
	case <-timer.C:
	case <-ctx.Done():
	}
	if !s.burst.withdraw(waiter) {
		if ctx.Err() != nil {
			return api.BurstLease{}, ctx.Err()
		}
		return api.BurstLease{Why: s.burstWhy(ctx, bytes, time.Since(start))}, nil
	}
	held := s.burst.leaseOf(ref)
	return api.BurstLease{Granted: true, Bytes: held, Env: burstEnv(held)}, nil
}

// releaseBurst is DELETE /v1/self/burst/{key}.
func (s *Server) releaseBurst(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return err
		}
		held := s.burst.release(a.Ref(), r.PathValue("key"))
		if held == 0 {
			s.grantBursts(r.Context())
		}
		return writeJSON(w, http.StatusOK, api.BurstLease{Granted: held > 0, Bytes: held, Env: burstEnv(held)})
	}
}

// dropBursts gives back all an agent holds of the pool, as it stops or goes.
func (s *Server) dropBursts(ctx context.Context, ref string) {
	s.burst.drop(ref)
	s.grantBursts(ctx)
}

// expireBursts is the queue tick's sweep: keys past their TTL, and leases of
// agents no longer running.
func (s *Server) expireBursts(ctx context.Context) {
	s.queueMu.Lock()
	plan, err := s.planAdmission(ctx, nil)
	s.queueMu.Unlock()
	if err != nil {
		return
	}
	holding := map[string]bool{}
	for _, h := range plan.holders {
		holding[h.Ref] = true
	}
	if s.burst.expire(time.Now(), func(ref string) bool { return holding[ref] }) {
		s.grantBursts(ctx)
	}
}
