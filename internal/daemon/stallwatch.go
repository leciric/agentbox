package daemon

import (
	"context"
	"fmt"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// The stall watch: an agent whose turn is running but no longer getting
// anywhere. Its chat says "running" until the turn's prompt is answered or the
// adapter's output closes, and an adapter behind a hung `incus exec`, in a
// frozen or unreachable machine, or wedged on its own, does neither — so the
// agent went on looking busy, its dot green, and the lead's recheck, which
// only looks at idle agents, never brought it up.
//
// A turn is stalled once, for stallAfter, nothing has shown it getting on:
//
//   - nothing from its adapter: every ACP message it sends while the turn
//     runs, a subagent's included, restarts the turn's clock (chat.Progress);
//   - and, while one of its tool calls runs, no work on its machine either:
//     a build or a test suite can rightly run for longer than stallAfter
//     with nothing to say, and its CPU is what shows it working. That is read
//     from the machine's cgroup, not from Incus, so it answers when incusd
//     doesn't. With no tool call running, CPU proves nothing: whatever the
//     machine runs beside the AI tool — a browser, a dev server — keeps it
//     busy while the tool itself is stuck.
//
// A turn waiting on a person — a permission request, a question or a
// credential request — is never stalled, nor is one on a paused machine. A
// stalled turn is marked on the chat's session (ChatSession.StalledSince),
// which the app shows instead of "Working", and the project's chat is woken
// with a line naming the agent. The mark comes off the moment the turn shows
// progress again, and nothing is stopped or killed: a false alarm costs the
// lead a look, where a wrong kill would cost the agent its work.

const (
	// stallAfter is how long a turn may go without progress. Claude Code's
	// and OpenCode's shell tools stop a command after ten minutes at most,
	// and the model streams every step it takes, so a healthy turn is never
	// silent for that long; the rest is room for the quiet a tool leaves
	// running — an automatic compaction, the API's own retries.
	stallAfter = 15 * time.Minute
	// stallInterval is how often the watch looks, and so what each CPU
	// reading is taken over.
	stallInterval = time.Minute
	// stallBusyCPU is the share of one core, over a stallInterval, that
	// counts as the machine doing work: well above what an idle Node process
	// and a shell use, well below any build, test or install.
	stallBusyCPU = 0.05
)

// stallTrack is what the watch keeps about one agent's turn between looks.
type stallTrack struct {
	turn   string
	cpu    time.Duration // the machine's CPU time at the last reading
	cpuAt  time.Time     // when that was; zero when it couldn't be read
	busyAt time.Time     // when the machine was last seen working, this turn
}

// watchStalls looks for stalled turns every stallInterval.
func (s *Server) watchStalls(ctx context.Context) {
	ticker := time.NewTicker(stallInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.checkStalls(ctx, now)
		}
	}
}

// stallFrom is when a turn last showed progress: what its adapter last sent,
// or, while a tool call runs, when its machine was last seen working, if that
// is later.
func stallFrom(p chat.Progress, busyAt time.Time) time.Time {
	if p.ToolRunning && busyAt.After(p.LastProgress) {
		return busyAt
	}
	return p.LastProgress
}

// checkStalls does one look, as of now. It asks nothing of Incus: the agents
// come from the state database and each machine's CPU from its cgroup, so the
// watch goes on when incusd hangs, which is one of the things it is for.
func (s *Server) checkStalls(ctx context.Context, now time.Time) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		s.logf("stall watch: %v", err)
		return
	}
	asking := map[string]bool{} // refs with a question waiting
	read := map[string]bool{}   // projects whose questions were read
	turns := map[string]bool{}
	for _, a := range agents {
		if a.IsLead() {
			continue // on the host, with nobody above it to tell
		}
		ref := a.Ref()
		p, ok := s.chat.Progress(ref)
		if !ok {
			continue
		}
		turns[ref] = true
		busyAt := s.machineBusyAt(ref, a.Instance, p.Turn, now)
		if !read[a.Project] {
			read[a.Project] = true
			if questions, err := s.store.Questions(ctx, a.Project, true); err == nil {
				for _, q := range questions {
					if q.Waiting() {
						asking[a.Project+"/"+q.Agent] = true
					}
				}
			}
		}
		from := stallFrom(p, busyAt)
		stalled := !p.Waiting && !asking[ref] && a.PausedAt.IsZero() && now.Sub(from) >= stallAfter
		switch {
		case stalled && p.Stalled == nil:
			if s.chat.SetStalled(a, p.Turn, &from) {
				s.agentStalled(ctx, a, p, from, now)
			}
		case !stalled && p.Stalled != nil:
			if s.chat.SetStalled(a, p.Turn, nil) {
				s.logf("stall watch: %s is getting on again", ref)
			}
		}
	}
	s.mu.Lock()
	for ref := range s.stalls {
		if !turns[ref] {
			delete(s.stalls, ref)
		}
	}
	s.mu.Unlock()
}

// machineBusyAt reads the machine's CPU time and says when, during this turn,
// the machine was last seen working: using at least stallBusyCPU of a core
// since the previous reading. Zero means not once yet.
func (s *Server) machineBusyAt(ref, instance, turn string, now time.Time) time.Time {
	used, ok := s.cpuTime(instance)
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.stalls[ref]
	if t == nil || t.turn != turn {
		t = &stallTrack{turn: turn}
		s.stalls[ref] = t
	}
	if ok && !t.cpuAt.IsZero() && used >= t.cpu {
		if elapsed := now.Sub(t.cpuAt); elapsed > 0 && float64(used-t.cpu) >= stallBusyCPU*float64(elapsed) {
			t.busyAt = now
		}
	}
	if ok {
		t.cpu, t.cpuAt = used, now
	} else {
		t.cpuAt = time.Time{}
	}
	return t.busyAt
}

// agentStalled acts on a turn that has just been found stalled: the log, the
// project's memory and the agent's own thread say so, and the project's chat
// is woken to look at it.
func (s *Server) agentStalled(ctx context.Context, a state.Agent, p chat.Progress, from, now time.Time) {
	quiet := now.Sub(from)
	s.logf("stall watch: %s has made no progress for %s, %s into its turn (a tool call running: %t)",
		a.Ref(), shortDuration(quiet), shortDuration(now.Sub(p.StartedAt)), p.ToolRunning)
	s.captureEvent(ctx, a.Project, a.Name, "agent_stalled", map[string]any{
		"quiet_minutes": int(quiet / time.Minute), "turn_minutes": int(now.Sub(p.StartedAt) / time.Minute), "tool_running": p.ToolRunning,
	}, "")
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentStalled, Summary: "No progress for " + shortDuration(quiet), At: now,
	})
	s.stallTell(ctx, a.Project, stallNotice(a, p, from, now))
}

// stallNotice is what the project's chat is told about a stalled agent.
func stallNotice(a state.Agent, p chat.Progress, from, now time.Time) string {
	name := a.Name
	if a.Title != "" {
		name = fmt.Sprintf("%s (%q)", a.Name, a.Title)
	}
	silent := "its AI tool has sent nothing"
	if p.ToolRunning {
		silent = "a tool call is running, but its AI tool has sent nothing and its machine has done no work"
	}
	return fmt.Sprintf("[stall] %s looks stuck: its turn has run %s, and for the last %s %s. "+
		"Its machine may be locked up or out of reach (Incus hung, the machine frozen), or its AI tool wedged. "+
		"Look with read_agent. A message to it won't get through while the turn is stuck: if it stays stuck, tell the user, "+
		"who can stop the turn from its chat or restart the agent. Never retire it with uncommitted or unpushed work.",
		name, shortDuration(now.Sub(p.StartedAt)), shortDuration(now.Sub(from)), silent)
}

// agentLost acts on an agent whose AI tool exited in the middle of a turn —
// killed (exit status 137 or 143), crashed, or cut off along with its machine.
// The turn has failed, so the agent no longer looks busy, but it looks idle
// rather than stopped, and it stopped in the middle of its work: the project's
// chat is woken to see to it, the way it is for a stall.
func (s *Server) agentLost(a state.Agent, why string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.runCtx), 30*time.Second)
	defer cancel()
	s.captureEvent(ctx, a.Project, a.Name, "agent_chat_lost", map[string]any{"why": why}, "")
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentChatLost, Summary: headWords(why, 200), At: time.Now(),
	})
	s.stallTell(ctx, a.Project, lostNotice(a, why))
}

// lostNotice is what the project's chat is told about an agent whose AI tool
// exited mid-turn.
func lostNotice(a state.Agent, why string) string {
	name := a.Name
	if a.Title != "" {
		name = fmt.Sprintf("%s (%q)", a.Name, a.Title)
	}
	return fmt.Sprintf("[chat lost] %s stopped in the middle of its turn: %s. Nothing is running on it now. "+
		"Look with read_agent; tell_agent starts its AI tool again with your message, to carry on from where it stopped.",
		name, headWords(why, 300))
}
