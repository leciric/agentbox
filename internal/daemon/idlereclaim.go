package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// Idle reclaim ("reclaim idle agents' memory", Settings.IdleReclaim, off by
// default): most of what fills the VM is what agents hold while nobody uses
// them — their AI tool, the desktop's Xvnc and Chromium, dev servers,
// watchers, Docker — which the governor (pressure.go) can't touch, as it
// holds back only heavy commands. Two steps give it back:
//
//   - An agent whose chat has been idle IdleFreezeSeconds (no turn, no
//     background work, no job, no question waiting, no recording, nobody on
//     its desktop or terminal) has what it left running frozen and its
//     machine's memory pushed out to swap (agent.FreezeIdle, the governor's
//     freezer and memory.reclaim). It wakes as soon as anything touches it:
//     a message or tell_agent, a turn starting by itself, its preview, its
//     desktop or its terminal opened, a heavy command, stopping it. What
//     can't be frozen without the chat is stopped instead, and the chat says
//     so, without starting a turn.
//   - An agent that finished (its last turn judged done or idle) and stayed
//     idle IdleStopSeconds has its machine stopped, as auto-stop idle agents
//     would after hours, without the retired mark: the next message or
//     tell_agent starts it again (tellAgentMessage).
//
// This is kept to small functions of its own beside the governor's: what it
// freezes is never a heavy run's cgroup, and an agent with a heavy run held
// or going is left alone.

// idleReclaimInterval is how often idle agents are looked at, when the
// setting is on. Waits are counted in minutes.
const idleReclaimInterval = time.Minute

// idleCgroups is what idle reclaim does to an agent's machine: agent's
// functions, or a test's.
type idleCgroups interface {
	Freeze(instance string) (*agent.IdleFreeze, error)
	Thaw(instance string, f *agent.IdleFreeze) error
	Reclaim(instance string) (before, after int64, err error)
}

type agentIdleCgroups struct{}

func (agentIdleCgroups) Freeze(instance string) (*agent.IdleFreeze, error) {
	return agent.FreezeIdle(instance)
}
func (agentIdleCgroups) Thaw(instance string, f *agent.IdleFreeze) error {
	return agent.ThawIdle(instance, f)
}
func (agentIdleCgroups) Reclaim(instance string) (int64, int64, error) {
	return agent.ReclaimIdle(instance)
}

// idleState is idle reclaim's, under mu.
type idleState struct {
	// step is held through a step, so the loop's and the one a change of
	// the setting starts don't both freeze the same machine.
	step sync.Mutex
	mu   sync.Mutex
	// frozen is what was frozen in each agent's machine, by ref.
	frozen map[string]*idleFrozen
	// stopped are the agents whose machines it stopped, by ref.
	stopped map[string]time.Time
	// touched is when each agent was last touched from outside its chat (its
	// preview), and open how many of its views (desktop, terminal) are open:
	// either keeps it from freezing.
	touched map[string]time.Time
	open    map[string]int
	// recovered is whether what a daemon before this one froze was woken,
	// on its first step.
	recovered bool
}

type idleFrozen struct {
	instance string
	f        *agent.IdleFreeze
	at       time.Time
}

func newIdleState() *idleState {
	return &idleState{
		frozen:  map[string]*idleFrozen{},
		stopped: map[string]time.Time{},
		touched: map[string]time.Time{},
		open:    map[string]int{},
	}
}

// runIdleReclaim steps idle reclaim until ctx ends.
func (s *Server) runIdleReclaim(ctx context.Context) {
	ticker := time.NewTicker(idleReclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.stepIdleReclaim(ctx, time.Now())
		}
	}
}

// stepIdleReclaim looks at every running agent once, as of now: freezes the
// idle, stops the finished, and wakes what shouldn't stay frozen. Off, it
// only wakes what it froze before it was turned off.
func (s *Server) stepIdleReclaim(ctx context.Context, now time.Time) {
	s.idle.step.Lock()
	defer s.idle.step.Unlock()
	on, freezeAfter, stopAfter, err := s.store.IdleReclaim(ctx)
	if err != nil {
		s.logf("idle reclaim: %v", err)
		return
	}
	s.idle.mu.Lock()
	first := !s.idle.recovered
	s.idle.recovered = true
	s.idle.mu.Unlock()
	if !on {
		s.wakeAllIdle()
		if !first {
			return
		}
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("idle reclaim: %v", err)
		return
	}
	changed := false
	m := s.manager(s.cfg.Log)
	for _, p := range projects {
		statuses, err := m.List(ctx, p.Name)
		if err != nil {
			s.logf("idle reclaim in %s: %v", p.Name, err)
			continue
		}
		questions, _ := s.store.Questions(ctx, p.Name, true)
		waiting := map[string]bool{}
		for _, q := range questions {
			if q.Waiting() {
				waiting[q.Agent] = true
			}
		}
		for _, st := range statuses {
			if st.IsLead() || st.Status != state.AgentReady {
				continue
			}
			if st.State != "running" {
				s.forgetFrozen(st.Ref()) // stopped or paused by something else
				continue
			}
			if s.frozenForMemory(st.Ref()) {
				continue // the fleet's (agentfreeze.go): not for idle reclaim to touch
			}
			if first {
				// What a daemon before this one froze and didn't get to
				// wake: this one knows nothing of it.
				if err := s.idleCgroups.Thaw(st.Instance, nil); err != nil {
					s.logf("idle reclaim: waking what an earlier daemon froze in %s: %v", st.Ref(), err)
				}
			}
			if !on {
				continue
			}
			if s.stepIdleAgent(ctx, m, st, waiting[st.Name], freezeAfter, stopAfter, now) {
				changed = true
			}
		}
	}
	if changed {
		s.refreshSoon()
	}
}

// stepIdleAgent is stepIdleReclaim for one running agent; it says whether
// anything changed.
func (s *Server) stepIdleAgent(ctx context.Context, m *agent.Manager, st agent.Status, question bool, freezeAfter, stopAfter time.Duration, now time.Time) bool {
	ref := st.Ref()
	idle := s.idle
	idle.mu.Lock()
	touched := idle.touched[ref]
	viewed := idle.open[ref] > 0
	_, frozen := idle.frozen[ref]
	idle.mu.Unlock()
	if viewed || question || s.chatBusy(ref) || s.heavyHeld(ref) {
		return frozen && s.wakeIdle(ref) // a turn the hook missed, say
	}
	o := s.outcomeOf(ctx, st.Project, ref)
	finished := o.kind == api.AgentOutcomeDone || o.kind == api.AgentOutcomeIdle
	if frozen && !finished {
		// Nothing more to do until it is touched: no need to ask its
		// machine what it is doing.
		return false
	}
	since, busy := s.autoStopIdleSince(ctx, m, st, question, now)
	if touched.After(since) {
		since = touched
	}
	if busy || since.IsZero() {
		return frozen && s.wakeIdle(ref)
	}
	idleFor := now.Sub(since)
	if finished && idleFor >= stopAfter {
		return s.stopFinishedIdle(ctx, m, st.Agent, idleFor)
	}
	if frozen || idleFor < freezeAfter {
		return false
	}
	return s.freezeIdle(ctx, st.Agent, idleFor)
}

// freezeIdle freezes what an idle agent left running and pushes its memory
// out, aside: gigabytes take a while.
func (s *Server) freezeIdle(ctx context.Context, a state.Agent, idleFor time.Duration) bool {
	f, err := s.idleCgroups.Freeze(a.Instance)
	if err != nil {
		s.logf("idle reclaim: freezing %s: %v", a.Ref(), err)
		return false
	}
	s.idle.mu.Lock()
	s.idle.frozen[a.Ref()] = &idleFrozen{instance: a.Instance, f: f, at: time.Now()}
	s.idle.mu.Unlock()
	go func() {
		before, after, err := s.idleCgroups.Reclaim(a.Instance)
		if err != nil {
			s.logf("idle reclaim: pushing %s's memory out: %v", a.Ref(), err)
		}
		s.logf("idle reclaim: froze %s after %s idle (%s), its machine down from %s to %s",
			a.Ref(), idleFor.Round(time.Minute), frozenWords(f.Frozen), agent.HumanBytes(before), agent.HumanBytes(after))
	}()
	if len(f.Stopped) > 0 {
		go s.tellIdleStopped(context.WithoutCancel(ctx), a, f.Stopped)
	}
	s.captureEvent(ctx, a.Project, a.Name, "idle_frozen", map[string]any{"processes": len(f.Frozen), "stopped": len(f.Stopped)}, "")
	return true
}

// tellIdleStopped says in an agent's chat, without starting a turn, which of
// its processes idle reclaim stopped because they couldn't be frozen: it, or
// whoever writes next, reads it before carrying on.
func (s *Server) tellIdleStopped(_ context.Context, a state.Agent, stopped []agent.IdleProcess) {
	var what []string
	for _, p := range stopped {
		what = append(what, fmt.Sprintf("`%s` (%s)", headWords(p.Command, 80), agent.HumanBytes(p.RSS)))
	}
	text := "While this chat was idle, AgentBox stopped " + strings.Join(what, ", ") +
		" to give the VM its memory back: it couldn't be paused and resumed later without pausing this chat too. " +
		"Start it again if you still need it."
	if err := s.chat.Notice(a, text, chat.NoticeOptions{}); err != nil {
		s.logf("idle reclaim: telling %s what it stopped: %v", a.Ref(), err)
	}
}

// stopFinishedIdle stops the machine of an agent that finished and stayed
// idle, as auto-stop idle agents does, but without the retired mark: the
// next message starts it again.
func (s *Server) stopFinishedIdle(ctx context.Context, m *agent.Manager, a state.Agent, idleFor time.Duration) bool {
	s.chat.Stop(a.Ref(), "the agent finished and went idle")
	if err := s.stopAgent(ctx, m, a); err != nil {
		s.logf("idle reclaim: stopping %s: %v", a.Ref(), err)
		return false
	}
	s.idle.mu.Lock()
	s.idle.stopped[a.Ref()] = time.Now()
	s.idle.mu.Unlock()
	s.logf("idle reclaim: stopped %s's machine, %s after it finished", a.Ref(), idleFor.Round(time.Minute))
	s.captureEvent(ctx, a.Project, a.Name, "agent_retired", map[string]any{"how": "idle_reclaim", "branch": a.Branch}, "")
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentIdleStopped, Summary: fmt.Sprintf("Machine stopped %s after it finished, to free its memory; a message starts it again", idleDurationWords(idleFor)), At: time.Now(),
	})
	return true
}

// wakeIdle wakes what idle reclaim froze in an agent's machine, at once;
// it says whether anything was frozen. Every way of touching an agent calls
// it: it costs a map lookup when nothing is.
func (s *Server) wakeIdle(ref string) bool {
	s.idle.mu.Lock()
	fr := s.idle.frozen[ref]
	delete(s.idle.frozen, ref)
	s.idle.mu.Unlock()
	if fr == nil {
		return false
	}
	start := time.Now()
	if err := s.idleCgroups.Thaw(fr.instance, fr.f); err != nil {
		s.logf("idle reclaim: waking %s: %v", ref, err)
	} else {
		s.logf("idle reclaim: woke %s after %s frozen, in %s", ref, time.Since(fr.at).Round(time.Second), time.Since(start).Round(time.Millisecond))
	}
	s.refreshSoon()
	return true
}

// touchIdle is an agent touched from outside its chat (a request to its
// preview): it wakes, and its idle time starts again.
func (s *Server) touchIdle(ref string) {
	s.idle.mu.Lock()
	s.idle.touched[ref] = time.Now()
	s.idle.mu.Unlock()
	s.wakeIdle(ref)
}

// viewIdle is one of an agent's views (its desktop, its terminal) opened: it
// wakes, and stays awake until the returned func closes the view.
func (s *Server) viewIdle(ref string) func() {
	s.idle.mu.Lock()
	s.idle.open[ref]++
	s.idle.mu.Unlock()
	s.wakeIdle(ref)
	return func() {
		s.idle.mu.Lock()
		defer s.idle.mu.Unlock()
		if s.idle.open[ref]--; s.idle.open[ref] <= 0 {
			delete(s.idle.open, ref)
		}
		s.idle.touched[ref] = time.Now()
	}
}

// startedIdle forgets that idle reclaim stopped an agent's machine, as it
// starts again.
func (s *Server) startedIdle(ref string) {
	s.idle.mu.Lock()
	delete(s.idle.stopped, ref)
	s.idle.mu.Unlock()
}

// forgetFrozen forgets what was frozen in a machine that is no longer
// running: stopping it ended all of it.
func (s *Server) forgetFrozen(ref string) {
	s.idle.mu.Lock()
	delete(s.idle.frozen, ref)
	s.idle.mu.Unlock()
}

// wakeAllIdle wakes every agent idle reclaim froze, as it is turned off.
func (s *Server) wakeAllIdle() {
	s.idle.mu.Lock()
	refs := make([]string, 0, len(s.idle.frozen))
	for ref := range s.idle.frozen {
		refs = append(refs, ref)
	}
	clear(s.idle.stopped)
	s.idle.mu.Unlock()
	for _, ref := range refs {
		s.wakeIdle(ref)
	}
}

// reclaimedOf is how idle reclaim holds an agent's memory (Agent.Reclaimed):
// "" for not at all.
func (s *Server) reclaimedOf(ref, machine string) string {
	s.idle.mu.Lock()
	defer s.idle.mu.Unlock()
	if _, ok := s.idle.frozen[ref]; ok && machine == "running" {
		return api.ReclaimedFrozen
	}
	if _, ok := s.idle.stopped[ref]; ok && machine == "stopped" {
		return api.ReclaimedStopped
	}
	return ""
}

// heavyHeld says whether the governor has any of an agent's heavy commands,
// going, paused or waiting: that agent isn't idle.
func (s *Server) heavyHeld(ref string) bool {
	h := s.heavy
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.g.Running() {
		if r.Agent == ref {
			return true
		}
	}
	for _, r := range h.g.Waiting() {
		if r.Agent == ref {
			return true
		}
	}
	return false
}

// frozenWords names the biggest of the processes frozen, for the log.
func frozenWords(ps []agent.IdleProcess) string {
	if len(ps) == 0 {
		return "nothing to freeze"
	}
	var names []string
	for _, p := range ps[:min(len(ps), 4)] {
		names = append(names, fmt.Sprintf("%s %s", p.Name, agent.HumanBytes(p.RSS)))
	}
	more := ""
	if len(ps) > 4 {
		more = fmt.Sprintf(" and %d more", len(ps)-4)
	}
	return fmt.Sprintf("%d processes: %s%s", len(ps), strings.Join(names, ", "), more)
}
