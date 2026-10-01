package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// memoryevents.go is the daemon's one door into a project's memory
// (D72): every chokepoint that
// already knows something happened calls one of these, so filling memory in
// is a one-line addition rather than a new concern at each call site. None of
// them can fail the action they're capturing — a memory row that didn't get
// written is worth a log line, not a broken agent lifecycle.

// captureEvent appends one event, marshalling payload as its JSON. A store
// that refuses the write, or a payload that won't marshal, costs nothing more
// than a log line: nothing here blocks the chokepoint that called it.
func (s *Server) captureEvent(ctx context.Context, project, agent, eventType string, payload any, artifactID string) {
	raw, err := json.Marshal(payload)
	if err != nil {
		s.logf("memory: encoding a %s event for %s: %v", eventType, project, err)
		return
	}
	if _, err := s.memory().AppendEvent(ctx, memory.Event{
		Project: project, Agent: agent, Type: eventType, Payload: raw, ArtifactID: artifactID,
	}); err != nil {
		s.logf("memory: recording a %s event for %s: %v", eventType, project, err)
	}
}

// captureArtifact records a reference to something already stored elsewhere —
// media, a worktree file, a pull request — never the thing itself. It answers
// with ok=false when the row couldn't be written, so a caller that would
// otherwise link an event to it can skip that rather than link to nothing.
func (s *Server) captureArtifact(ctx context.Context, project, agent, artifactType, path string, metadata any) (memory.Artifact, bool) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		s.logf("memory: encoding %s artifact metadata for %s: %v", artifactType, project, err)
		return memory.Artifact{}, false
	}
	out, err := s.memory().AddArtifact(ctx, memory.Artifact{
		Project: project, Agent: agent, Type: artifactType, Path: path, Metadata: raw,
	})
	if err != nil {
		s.logf("memory: recording a %s artifact for %s: %v", artifactType, project, err)
		return memory.Artifact{}, false
	}
	return out, true
}

// addActiveAgent and removeActiveAgent keep working memory's activeAgents
// current, a merge patch at a time: creating an agent adds its name, retiring
// or genuinely finishing removes it. Both read-modify-write the same document,
// so two of these racing is possible but rare, and worth no more than the
// entry it might drop — an agent that's still on the project shows up again
// the next time either runs.
func (s *Server) addActiveAgent(ctx context.Context, project, agent string) {
	s.editActiveAgents(ctx, project, func(names []string) []string {
		if slices.Contains(names, agent) {
			return names
		}
		return append(names, agent)
	})
}

func (s *Server) removeActiveAgent(ctx context.Context, project, agent string) {
	s.editActiveAgents(ctx, project, func(names []string) []string {
		return slices.DeleteFunc(names, func(n string) bool { return n == agent })
	})
}

func (s *Server) editActiveAgents(ctx context.Context, project string, edit func([]string) []string) {
	current, err := s.memory().WorkingMemory(ctx, project)
	if err != nil {
		s.logf("memory: reading %s's working memory: %v", project, err)
		return
	}
	updated := edit(current.ActiveAgents)
	if _, err := s.memory().SetWorkingMemory(ctx, project, memory.WorkingMemoryPatch{ActiveAgents: &updated}); err != nil {
		s.logf("memory: updating %s's active agents: %v", project, err)
	}
}

// captureAgentFinished records the same diff stat, pull request and summary a
// finish notice already tells the lead, and links the agent's own report when
// it left one via the report tool, so consolidation later reads what the lead
// saw rather than re-deriving it. A worker that genuinely finishes is no
// longer active, so this is also where it leaves working memory's list.
func (s *Server) captureAgentFinished(ctx context.Context, a state.Agent, changes api.AgentChanges, pr *api.PullRequest, summary string) {
	payload := map[string]any{
		"title": a.Title, "branch": a.Branch,
		"files": changes.Files, "insertions": changes.Insertions, "deletions": changes.Deletions, "dirty": changes.Dirty,
		"summary": summary,
	}
	if pr != nil {
		payload["pr"] = map[string]any{"number": pr.Number, "url": pr.URL, "state": pr.State}
	}
	var report memory.Report
	if reports, err := s.memory().Reports(ctx, a.Project, a.Name); err == nil && len(reports) > 0 {
		report = reports[0]
		payload["reportId"] = report.ID
	}
	s.captureEvent(ctx, a.Project, a.Name, "agent_finished", payload, "")
	s.removeActiveAgent(ctx, a.Project, a.Name)
}

// The task graph's own history (D77).
// A task row is project *state*: it is rewritten as the work moves, so by
// itself it can't say when something was picked up or what it was blocked on
// last week. These three put that in events, through the same door as
// everything else, so consolidation reads the plan's history beside the rest
// of what happened.

// captureTaskCreated records a task the project now has.
func (s *Server) captureTaskCreated(ctx context.Context, t memory.Task) {
	s.captureEvent(ctx, t.Project, t.Agent, "task_created", map[string]any{
		"taskId": t.ID, "goal": t.Goal, "status": t.Status,
		"parentTaskId": t.ParentID, "dependsOn": t.DependsOn,
	}, "")
}

// captureTaskStatus records a task moving, and only when it really moved:
// a write that leaves the status as it was is not a transition, and the
// caller checks that before calling this.
func (s *Server) captureTaskStatus(ctx context.Context, t memory.Task, was string) {
	s.captureEvent(ctx, t.Project, t.Agent, "task_status_changed", map[string]any{
		"taskId": t.ID, "goal": t.Goal, "from": was, "to": t.Status,
	}, "")
	if t.Status == memory.TaskBlocked {
		s.captureTaskBlocked(ctx, t.Project, t, t.DependsOn)
	}
}

// captureTaskBlocked records that a task is waiting on something. It happens
// twice over: when a task's status becomes blocked, and when an edge is drawn
// to something unfinished. Both are the same fact — this can't move yet — and
// a reader of the history wants them under one type rather than having to
// know which of the two shapes meant it.
func (s *Server) captureTaskBlocked(ctx context.Context, project string, t memory.Task, on []string) {
	s.captureEvent(ctx, project, t.Agent, "task_blocked", map[string]any{
		"taskId": t.ID, "goal": t.Goal, "status": t.Status, "dependsOn": on,
	}, "")
}

// ptr is a pointer to a value, for the merge patches this package fills in.
func ptr[T any](v T) *T { return &v }

// agentOfCommit is the name of the project's agent whose own commit this is,
// or "" when none made it — a pull request merged from outside AgentBox is
// still worth an event, just not one attributed to an agent. A pull request's
// branch name says nothing: an agent's work is pushed under any name.
func agentOfCommit(heads []agentHead, sha string) string {
	for _, h := range heads {
		if h.owns(sha) {
			return h.agent
		}
	}
	return ""
}

// captureLeadTurn is the nearest equivalent of chat.Manager.Finished for the
// lead role: Finished is only called for a worker's turn (see chat.go), so
// this reads the same published stream every chat item already goes through,
// via the Publish hook every conversation shares, and reacts to the one shape
// a finished lead turn takes — its user item touched with Result now set —
// so consolidation later has a record of what the project's chat was asked
// and what it answered.
func (s *Server) captureLeadTurn(ev api.ChatEvent) {
	if ev.Item == nil || ev.Item.Kind != "user" || ev.Item.Result == nil {
		return
	}
	project, name, ok := strings.Cut(ev.Agent, "/")
	if !ok || name != state.LeadName {
		return
	}
	userMessage := truncateRunes(ev.Item.Text, 500)
	// Off this call's own stack: Publish fires while the conversation's lock
	// is held, and LastMessage would deadlock taking it again.
	go func() {
		ctx := s.background()
		lead, err := s.store.Agent(ctx, project, state.LeadName)
		if err != nil {
			return
		}
		reply := s.chat.LastMessage(lead)
		s.captureEvent(ctx, project, "", "lead_turn", map[string]any{
			"userMessage":    userMessage,
			"assistantReply": truncateRunes(reply, 800),
		}, "")
		// A turn has just ended, so the chat's session is idle and warm and
		// nobody is waiting on it: the moment consolidation runs (D76). It
		// is on this goroutine rather than its own because the two belong in
		// order — the event this turn produced is part of what is about to
		// be distilled — and because nothing here blocks the turn either way.
		s.consolidateAfterLeadTurn(ctx, project)
	}()
}

// truncateRunes cuts s to at most n runes, so a captured event never carries
// more of an agent's or a user's prose than what consolidation will read.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
