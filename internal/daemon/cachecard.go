package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
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
// A lead that is idle because its agents are working is waiting on them, not
// on the user: their notices will wake it, and that turn would re-send the
// whole context cold. So when the timer fires and one of the project's agents
// is still working, the lead's session is sent a keepalive instead of the card
// (chat.Keepalive): a hidden prompt that reads the cache back and restarts its
// TTL, for a few thousand tokens against the 100k a cold re-send writes. It
// shows nothing, wakes no one and doesn't make the chat read busy. Only on an
// hour's cache: on five minutes a ping every four and a half costs more than
// the rewrite it saves. At most maxKeepalives in a row, then the card, so an
// agent stuck working all night isn't paid for by the hour; any turn that
// really runs — the user's message, an agent's notice — starts the count again.
//
// By then the lead has usually slept (DefaultLeadSleep is 15 minutes), and the
// keepalive wakes it: a fresh Claude Code resuming the same session. That reads
// the cache back rather than writing it, because nothing in the prefix moves
// on a wake. The resume asks for nothing of its own (no MCP servers or system
// prompt: the lead's HOME gives those, and the wake doesn't write it), no turn
// runs, so nothing re-renders the brief or moves the worktree, and Claude Code
// keeps the context it announced in the transcript (CLAUDE.md, git status,
// the date) and appends what changed since rather than re-rendering it. A
// brief rewritten by a message the lead read while awake lands as such a
// delta, at the end. What does move the prefix — a newer Claude Code, a
// connector added — moves it for the notice's turn that wakes it too.
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

// maxKeepalives is how many keepalives a lead's cache gets in a row before
// the card: four hours of waiting on an hour's cache.
const maxKeepalives = 4

// keepaliveTimeout bounds a keepalive's round trip.
const keepaliveTimeout = 2 * time.Minute

// keepsAlive reports whether a cache that lasts ttl, with sent keepalives in a
// row already, gets another one rather than the card.
func keepsAlive(ttl time.Duration, sent int) bool {
	return ttl >= time.Hour && sent < maxKeepalives
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
	// keepalives is how many keepalives in a row the cache has had since a
	// turn last ran.
	keepalives int
}

// leadCacheIdle is a lead that has just finished a turn: its cache starts
// running out, and the card, if it was up, goes.
func (s *Server) leadCacheIdle(a state.Agent) {
	// The Home chat has no project memory to compact into, so no card.
	if a.Role != state.RoleLead || a.AI != "claude" || a.IsHome() {
		return
	}
	s.armLeadCache(a, 0, 0)
}

// armLeadCache sets the timer for a lead's cache, with keepalives in a row
// behind it. A keepalive's re-arm passes the generation it fired on, after,
// and arms nothing if anything has happened since: a turn that ended in the
// meantime has armed it already, and counts from nought.
func (s *Server) armLeadCache(a state.Agent, keepalives int, after uint64) {
	ttl, why := s.leadCacheTTL(a)
	delay := cacheCardDelay(ttl)
	schedule := s.idleAfter
	if schedule == nil {
		schedule = func(d time.Duration, f func()) interface{ Stop() bool } { return time.AfterFunc(d, f) }
	}
	s.mu.Lock()
	if s.leadCaches == nil {
		s.leadCaches = map[string]*leadCache{}
	}
	was := s.leadCaches[a.Project]
	if after != 0 && (was == nil || was.gen != after) {
		s.mu.Unlock()
		return
	}
	gen := uint64(1)
	if was != nil {
		if was.timer != nil {
			was.timer.Stop()
		}
		gen = was.gen + 1
	}
	s.leadCaches[a.Project] = &leadCache{
		gen: gen, idleSince: time.Now(), ttl: ttl, why: why, keepalives: keepalives,
		timer: schedule(delay, func() { s.cacheDue(a.Project, gen) }),
	}
	s.mu.Unlock()
	if was != nil && was.due {
		s.publishLeadCache(a.Project)
	}
}

// cacheDue is the timer firing: if nothing has happened since it was set and
// the context is worth saving, the lead gets a keepalive while its agents
// work, and the card otherwise.
func (s *Server) cacheDue(project string, gen uint64) {
	if !s.leadCacheIs(project, gen) || (s.runCtx != nil && s.runCtx.Err() != nil) {
		return
	}
	gate := s.cacheGate
	if gate == nil {
		gate = s.leadCacheWorthSaving
	}
	lead, used, ok := gate(project)
	if !ok || !worthACard(used) {
		return
	}
	s.mu.Lock()
	lc := s.leadCaches[project]
	keep := lc != nil && lc.gen == gen && keepsAlive(lc.ttl, lc.keepalives)
	s.mu.Unlock()
	if keep && s.agentsAtWork(project) && s.keepLeadCacheAlive(lead, gen, used) {
		return
	}
	s.showCacheCard(project, gen, used)
}

// leadCacheWorthSaving is cacheDue's gate: the project's lead, and its
// context, when there is a cache that a card or a keepalive could save.
func (s *Server) leadCacheWorthSaving(project string) (state.Agent, int64, bool) {
	ctx := context.Background()
	lead, err := s.manager(nil).Lead(ctx, project)
	if err != nil {
		return state.Agent{}, 0, false
	}
	// A project that turned compaction off doesn't want it offered either.
	p, err := s.store.Project(ctx, project)
	if err != nil || p.RolloverThreshold == state.RolloverOff {
		return state.Agent{}, 0, false
	}
	// Not ready is a turn running, or a session that has already stopped:
	// the turn ends and sets the timer again, and a stopped session has no
	// cache left to save.
	used, _, ready := s.chat.Context(agent.LeadRef(project))
	if !ready {
		return state.Agent{}, 0, false
	}
	return lead, used, true
}

// agentsAtWork says whether one of a project's agents is working: in a turn,
// starting one, waiting on an answer, or with background work that will wake
// it (chatBusy).
func (s *Server) agentsAtWork(project string) bool {
	if s.projectWorking != nil {
		return s.projectWorking(project)
	}
	agents, err := s.store.Agents(context.Background(), project)
	if err != nil {
		return false
	}
	for _, a := range agents {
		if !a.IsLead() && s.chatBusy(a.Ref()) {
			return true
		}
	}
	return false
}

// keepLeadCacheAlive sends the lead a keepalive and sets the timer again,
// counting it. It reports whether the cache was kept, and false when the
// card should go up instead.
func (s *Server) keepLeadCacheAlive(lead state.Agent, gen uint64, used int64) bool {
	ctx := s.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, keepaliveTimeout)
	defer cancel()
	send := s.leadKeepalive
	if send == nil {
		send = s.chat.Keepalive
	}
	s.mu.Lock()
	lc := s.leadCaches[lead.Project]
	if lc == nil || lc.gen != gen {
		s.mu.Unlock()
		return true // something replaced it: nothing to do
	}
	n := lc.keepalives + 1
	s.mu.Unlock()
	if err := send(ctx, lead); err != nil {
		// Busy with a turn that started since: its end sets the timer
		// again. Busy with a hidden prompt, nothing will, so the card.
		if _, _, ready := s.chat.Context(lead.Ref()); errors.Is(err, chat.ErrBusy) && !ready {
			return true
		}
		s.logf("keeping the %s chat's prompt cache warm: %v", lead.Project, err)
		return false
	}
	s.logf("the %s chat has been idle for %s while its agents work, with %d tokens of context: sent keepalive %d of %d",
		lead.Project, time.Since(lc.idleSince).Round(time.Second), used, n, maxKeepalives)
	s.armLeadCache(lead, n, gen)
	return true
}

// showCacheCard puts the card up, if the timer that fired is still current.
func (s *Server) showCacheCard(project string, gen uint64, used int64) {
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
	if project != state.HomeProject {
		if _, err := s.store.Project(r.Context(), project); err != nil {
			return err
		}
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
	if strings.TrimSpace(req.Text) == "" && len(req.Images) == 0 && len(req.Files) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	a, err = s.ensureLeadFromPath(r)
	if err != nil {
		return err
	}
	item, err := s.chat.SendMessage(a, api.ChatMessageRequest{Text: req.Text, Images: req.Images, Files: req.Files})
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
