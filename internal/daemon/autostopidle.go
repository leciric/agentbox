package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// "auto-stop idle agents" stops a running or paused agent, keeping its
// worktree and branch, once nothing has happened on it for as long as the
// installation's idle time: no chat turn in progress, no running job, no
// pending question or credential request, no terminal input and no
// recording. A paused agent counts as idle from when it was paused
// (state.Agent.PausedAt), since it still holds its RAM and swap either way.

// autoStopIdleInterval is how often the daemon checks for agents idle long
// enough to stop, when the setting is on. Idle times are counted in hours, so
// checking any more often than this buys nothing.
const autoStopIdleInterval = 5 * time.Minute

// sweepIdleAgents periodically stops agents "auto-stop idle agents" has found
// idle for as long as the setting allows.
func (s *Server) sweepIdleAgents(ctx context.Context) {
	s.stopIdleAgents(ctx, time.Now())
	s.firstSweeps.Done()
	ticker := time.NewTicker(autoStopIdleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.stopIdleAgents(ctx, time.Now())
		}
	}
}

// stopIdleAgents does one pass over every project, as of now. Split out from
// sweepIdleAgents so tests can fake the clock instead of waiting on real
// idle times.
func (s *Server) stopIdleAgents(ctx context.Context, now time.Time) {
	on, idleTime, err := s.store.AutoStopIdle(ctx)
	if err != nil {
		s.logf("auto-stop idle agents: %v", err)
		return
	}
	if !on {
		return
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("auto-stop idle agents: %v", err)
		return
	}
	stopped := 0
	for _, p := range projects {
		stopped += s.stopIdleIn(ctx, p.Name, idleTime, now)
	}
	if stopped > 0 {
		s.refreshAgents(ctx)
	}
}

// stopIdleIn stops every idle agent in one project, as of now.
func (s *Server) stopIdleIn(ctx context.Context, project string, idleTime time.Duration, now time.Time) int {
	m := s.manager(s.cfg.Log)
	statuses, err := m.List(ctx, project)
	if err != nil {
		s.logf("auto-stop idle agents in %s: %v", project, err)
		return 0
	}
	questions, err := s.store.Questions(ctx, project, true)
	if err != nil {
		s.logf("auto-stop idle agents in %s: %v", project, err)
		return 0
	}
	waiting := make(map[string]bool, len(questions))
	for _, q := range questions {
		if q.Waiting() {
			waiting[q.Agent] = true
		}
	}
	stopped := 0
	for _, st := range statuses {
		if st.IsLead() || (st.State != "running" && st.State != "paused") {
			continue // nothing to stop, or already stopped
		}
		since, busy := s.autoStopIdleSince(ctx, m, st, waiting[st.Name], now)
		if busy || since.IsZero() || now.Sub(since) < idleTime {
			continue
		}
		if err := s.stopIdleAgent(ctx, m, st.Agent, idleTime); err != nil {
			s.logf("auto-stop idle agents: stopping %s: %v", st.Ref(), err)
			continue
		}
		stopped++
	}
	return stopped
}

// autoStopIdleSince says since when an agent has been idle, or that it is
// busy right now and so not idle at all. A paused agent is idle from
// PausedAt, since being paused already rules out everything else this checks
// for; a running one is idle from the latest of its last chat turn, its last
// job activity and its last terminal keystroke, falling back to when it was
// created if none of those ever happened.
func (s *Server) autoStopIdleSince(ctx context.Context, m *agent.Manager, st agent.Status, waitingQuestion bool, now time.Time) (since time.Time, busy bool) {
	if st.State == "paused" {
		if st.PausedAt.IsZero() {
			// Paused by something outside this feature, or before it existed:
			// start the clock now rather than treating it as idle forever.
			return now, false
		}
		return st.PausedAt, false
	}
	if waitingQuestion {
		return time.Time{}, true
	}
	if chat := s.chat.State(st.Ref()); chat == api.ChatRunning || chat == api.ChatWaiting || chat == api.ChatStarting {
		return time.Time{}, true
	}
	if running, last, err := s.store.JobActivityFor(ctx, st.Ref()); err == nil {
		if running {
			return time.Time{}, true
		}
		if last.After(since) {
			since = last
		}
	}
	if rec, err := m.Recording(ctx, st.Agent); err == nil && rec.Recording {
		return time.Time{}, true
	}
	if item, ok, err := s.store.LastChatItem(ctx, st.Project, st.Name); err == nil && ok {
		var stored struct {
			UpdatedAt time.Time `json:"updatedAt"`
		}
		if json.Unmarshal(item.Data, &stored) == nil && stored.UpdatedAt.After(since) {
			since = stored.UpdatedAt
		}
	}
	if at, ok := s.lastTerminalInput(st.Ref()); ok && at.After(since) {
		since = at
	}
	if since.IsZero() {
		since = st.CreatedAt
	}
	return since, false
}

// stopIdleAgent stops one idle agent, keeping its worktree and branch, and
// records why: a memory event alongside every other retiring of an agent, and
// an api.AgentEvent the app can show in the agent's own view.
func (s *Server) stopIdleAgent(ctx context.Context, m *agent.Manager, a state.Agent, idleTime time.Duration) error {
	s.chat.Stop(a.Ref(), "the agent went idle")
	if err := m.Stop(ctx, a); err != nil {
		return err
	}
	s.captureEvent(ctx, a.Project, a.Name, "agent_retired", map[string]any{"how": "auto_stop_idle", "branch": a.Branch}, "")
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentIdleStopped, Summary: fmt.Sprintf("Stopped after %s idle", idleDurationWords(idleTime)), At: time.Now(),
	})
	return nil
}

// idleDurationWords is an idle time in words, like "2h" or "1h30m", for the
// event summary "Stopped after 2h idle".
func idleDurationWords(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}
