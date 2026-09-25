package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Asking before a project's chat re-sends a context whose cache has expired.
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
// be worth it, the app shows a card: how long it has been idle, and roughly
// what the next message would re-send. The user chooses — compact first,
// while the cache may still be warm, or send as it is. Nothing is compacted
// without them: an earlier version compacted by itself when the timer fired,
// and a conversation is the user's to throw away. A message written while the
// card is up waits in the app for that choice (chatCacheChoice takes both).
//
// Every lead turn that ends sets the timer again and takes the card down,
// whoever started it — an agent's notice waking the chat included, which gets
// no card of its own and warms the cache like any other turn.
//
// Claude Code only. Codex and OpenCode don't expose a cache TTL to plan
// around, so their leads are left to rolloverIfNeeded.

// cacheCardFloor is the context below which an idle chat gets no card: a cold
// re-send of less than this costs less than consolidating it would.
const cacheCardFloor = 60_000

// worthACard reports whether a context of used tokens is big enough for the
// card.
func worthACard(used int64) bool { return used >= cacheCardFloor }

// cacheCardMargin is how long before the cache expires the card shows, at
// most: a tenth of the TTL, or five minutes, whichever is smaller. Enough for
// the consolidation to be sent while the cache is still there, if the user is.
const cacheCardMargin = 5 * time.Minute

// cacheCardDelay is how long after a turn ends the card shows, for a cache
// that lasts ttl.
func cacheCardDelay(ttl time.Duration) time.Duration {
	return ttl - min(ttl/10, cacheCardMargin)
}

// leadCache is what the daemon knows of a lead's prompt cache since its last
// turn ended. gen tells a timer that fires after it has been replaced that it
// is stale, since Stop can lose that race.
type leadCache struct {
	timer     interface{ Stop() bool }
	gen       uint64
	idleSince time.Time
	ttl       time.Duration
	why       string
	due       bool // the card is up
}

// leadIdle is a lead that has just finished a turn: its cache starts running
// out, and the card, if it was up, goes.
func (s *Server) leadIdle(a state.Agent) {
	if a.Role != state.RoleLead || a.AI != "claude" {
		return
	}
	ttl, why := s.leadCacheTTL(a)
	delay := cacheCardDelay(ttl)
	after := s.idleAfter
	if after == nil {
		after = func(d time.Duration, f func()) interface{ Stop() bool } { return time.AfterFunc(d, f) }
	}
	s.mu.Lock()
	if s.leadCaches == nil {
		s.leadCaches = map[string]*leadCache{}
	}
	was := s.leadCaches[a.Project]
	gen := uint64(1)
	if was != nil {
		if was.timer != nil {
			was.timer.Stop()
		}
		gen = was.gen + 1
	}
	s.leadCaches[a.Project] = &leadCache{
		gen: gen, idleSince: time.Now(), ttl: ttl, why: why,
		timer: after(delay, func() { s.cacheDue(a.Project, gen) }),
	}
	s.mu.Unlock()
	if was != nil && was.due {
		s.publishLeadCache(a.Project)
	}
}

// cacheDue is the timer firing: the card goes up if nothing has happened
// since it was set and the context is worth compacting.
func (s *Server) cacheDue(project string, gen uint64) {
	if !s.leadCacheIs(project, gen) || (s.runCtx != nil && s.runCtx.Err() != nil) {
		return
	}
	ctx := context.Background()
	if _, err := s.manager(nil).Lead(ctx, project); err != nil {
		return
	}
	// A project that turned compaction off doesn't want it offered either.
	p, err := s.store.Project(ctx, project)
	if err != nil || p.RolloverThreshold == state.RolloverOff {
		return
	}
	// Not ready is a turn running, or a session that has already stopped:
	// the turn ends and sets the timer again, and a stopped session has no
	// cache left to save.
	used, _, ready := s.chat.Context(agent.LeadRef(project))
	if !ready || !worthACard(used) {
		return
	}
	s.mu.Lock()
	lc := s.leadCaches[project]
	current := lc != nil && lc.gen == gen
	if current {
		lc.due = true
	}
	s.mu.Unlock()
	if current {
		s.logf("the %s chat has been idle for %s of its %s prompt cache (%s), with %d tokens of context: asking whether to compact it",
			project, time.Since(lc.idleSince).Round(time.Second), lc.ttl, lc.why, used)
		s.publishLeadCache(project)
	}
}

func (s *Server) leadCacheIs(project string, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	lc := s.leadCaches[project]
	return lc != nil && lc.gen == gen
}

// leadCacheState is a lead's cache as the API shows it.
func (s *Server) leadCacheState(project string) api.ChatCache {
	out := api.ChatCache{Project: project}
	s.mu.Lock()
	lc := s.leadCaches[project]
	if lc != nil {
		idle, due, expires := lc.idleSince, lc.idleSince.Add(cacheCardDelay(lc.ttl)), lc.idleSince.Add(lc.ttl)
		out.IdleSince, out.DueAt, out.ExpiresAt = &idle, &due, &expires
		out.TTLSeconds, out.TTLSource, out.Due = int64(lc.ttl/time.Second), lc.why, lc.due
	}
	s.mu.Unlock()
	out.ContextUsed, _, _ = s.chat.Context(agent.LeadRef(project))
	return out
}

func (s *Server) publishLeadCache(project string) {
	s.events.publish(api.EventChatCache, s.leadCacheState(project))
}

// settleLeadCache takes the card down once the user has chosen. After a
// compaction there is no cache left to track until the next turn ends, and
// forget drops what was known of it.
func (s *Server) settleLeadCache(project string, forget bool) {
	s.mu.Lock()
	lc := s.leadCaches[project]
	if lc == nil {
		s.mu.Unlock()
		return
	}
	was := lc.due
	lc.due = false
	if forget {
		if lc.timer != nil {
			lc.timer.Stop()
		}
		delete(s.leadCaches, project)
	}
	s.mu.Unlock()
	if was || forget {
		s.publishLeadCache(project)
	}
}

// chatCache is the project chat's cache, and whether its card is up.
func (s *Server) chatCache(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, s.leadCacheState(project))
}

// chatCacheChoice is the user answering the card. Compacting runs the same
// consolidation as the Compact button, and the message written while the
// card was up then starts the fresh session; sending as it is sends it the
// way any message is sent. It answers with the message, or nothing when there
// was none.
func (s *Server) chatCacheChoice(w http.ResponseWriter, r *http.Request) error {
	var req api.ChatCacheChoice
	if err := readJSON(r, &req); err != nil {
		return err
	}
	a, err := s.leadFromPath(r)
	if err != nil {
		return err
	}
	if req.Compact {
		if err := s.compactLead(r.Context(), a); err != nil {
			return err
		}
		s.settleLeadCache(a.Project, true)
	} else {
		s.settleLeadCache(a.Project, false)
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Images) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	a, err = s.ensureLeadFromPath(r)
	if err != nil {
		return err
	}
	item, err := s.chat.Send(a, req.Text, req.Images...)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, item)
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
