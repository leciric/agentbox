package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// Compacting a project's chat before its prompt cache expires.
//
// rolloverIfNeeded compacts when the context is full, just before a turn. That
// misses the other way a long conversation gets expensive: left idle past
// Claude's prompt-cache TTL, the next message re-sends the whole context
// uncached — the ledger showed cache writes of 105k–135k tokens after gaps of
// nine hours and more. Compacting then doesn't help, because the hidden
// consolidation prompt reads everything cold too.
//
// So when a lead's turn ends, a timer is set a little before the cache would
// expire. If the chat is still idle when it fires, and has enough context to
// be worth it, it is compacted while the cache is still warm: the
// consolidation reads it cheaply, and the next message starts a fresh session
// with a short recap instead of a cold 100k-token one. Every lead turn that
// ends sets the timer again, which is what keeps a chat in use from ever being
// compacted by it.
//
// Claude Code only. Codex and OpenCode don't expose a cache TTL to plan
// around, so their leads are left to rolloverIfNeeded.

// idleRolloverFloor is the context below which an idle chat is left alone: a
// cold re-send of less than this costs less than consolidating it would.
const idleRolloverFloor = 40_000

// idleRolloverMargin is how long before the cache expires the timer fires, at
// most: a tenth of the TTL, or five minutes, whichever is smaller. Enough for
// the consolidation to be sent while the cache is still there.
const idleRolloverMargin = 5 * time.Minute

// idleRolloverDelay is how long after a turn ends to compact, for a cache
// that lasts ttl.
func idleRolloverDelay(ttl time.Duration) time.Duration {
	return ttl - min(ttl/10, idleRolloverMargin)
}

// idleTimer is a lead's pending idle rollover. gen tells a timer that fires
// after it has been replaced that it is stale, since Stop can lose that race.
type idleTimer struct {
	timer interface{ Stop() bool }
	gen   uint64
}

// leadIdle schedules the idle rollover of a lead that has just finished a
// turn, replacing any it already had.
func (s *Server) leadIdle(a state.Agent) {
	if a.Role != state.RoleLead || a.AI != "claude" {
		return
	}
	ttl, why := s.leadCacheTTL(a)
	delay := idleRolloverDelay(ttl)
	after := s.idleAfter
	if after == nil {
		after = func(d time.Duration, f func()) interface{ Stop() bool } { return time.AfterFunc(d, f) }
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleTimers == nil {
		s.idleTimers = map[string]idleTimer{}
	}
	was := s.idleTimers[a.Project]
	if was.timer != nil {
		was.timer.Stop()
	}
	gen := was.gen + 1
	s.idleTimers[a.Project] = idleTimer{gen: gen, timer: after(delay, func() { s.idleRollover(a.Project, gen) })}
	s.logf("the %s chat is idle: compacting it in %s unless it is used, before its %s prompt cache (%s) expires", a.Project, delay.Round(time.Second), ttl, why)
}

// idleRollover is the timer firing: it compacts the lead if nothing has
// happened since it was set.
func (s *Server) idleRollover(project string, gen uint64) {
	s.mu.Lock()
	current := s.idleTimers[project].gen == gen
	if current {
		delete(s.idleTimers, project)
	}
	s.mu.Unlock()
	if !current || (s.runCtx != nil && s.runCtx.Err() != nil) {
		return
	}
	ctx := context.Background()
	if s.runCtx != nil {
		ctx = context.WithoutCancel(s.runCtx)
	}
	a, err := s.manager(nil).Lead(ctx, project)
	if err != nil {
		return
	}
	p, err := s.store.Project(ctx, project)
	if err != nil || p.RolloverThreshold == state.RolloverOff {
		return
	}
	// Not ready is a turn running, or a session that has already stopped:
	// the turn ends and sets the timer again, and a stopped session has no
	// cache worth saving.
	used, _, ready := s.chat.Context(agent.LeadRef(project))
	if !ready || used < idleRolloverFloor {
		return
	}
	s.logf("compacting the %s chat before its prompt cache expires: %d tokens used", project, used)
	if err := s.compactLead(ctx, a); err != nil && !errors.Is(err, chat.ErrBusy) {
		s.logf("compacting the %s chat: %v", project, err)
	}
}

// leadCacheTTL is how long the lead's prompt cache lasts, and what said so:
// what its settings.json sets, and otherwise what Claude Code would choose.
func (s *Server) leadCacheTTL(a state.Agent) (time.Duration, string) {
	m := s.manager(nil)
	if ttl := m.LeadPromptCacheTTL(a); ttl > 0 {
		return ttl, "set in its settings"
	}
	// The lead runs on an account's OAuth token, a subscription. With none it
	// is on nothing Claude Code gives the hour to.
	if token, err := m.Creds.ClaudeToken(a.ClaudeAccount); err != nil || token == "" {
		return 5 * time.Minute, "no subscription"
	}
	account, err := m.Creds.ClaudeAccountOf(a.ClaudeAccount)
	if err != nil {
		return time.Hour, "subscription"
	}
	readings, err := s.store.ClaudeLimits(context.Background())
	if err != nil {
		return time.Hour, "subscription"
	}
	for _, r := range readings {
		if r.Account != account {
			continue
		}
		var reading acp.RateLimit
		if json.Unmarshal(r.Reading, &reading) == nil && pastLimits(reading, time.Now()) {
			return 5 * time.Minute, "subscription past its limits"
		}
	}
	return time.Hour, "subscription"
}

// pastLimits reports whether a subscription has gone past its usage limits,
// where Claude Code drops its cache to five minutes: refused, or paying for
// overage. A reading whose limit has reset since says nothing about now.
func pastLimits(r acp.RateLimit, now time.Time) bool {
	if r.ResetsAt > 0 && !now.Before(time.Unix(r.ResetsAt, 0)) {
		return false
	}
	return r.Status == "rejected" || r.IsUsingOverage
}
