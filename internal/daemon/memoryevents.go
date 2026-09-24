package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// memoryevents.go is the daemon's one door into a project's memory
// ([D72](../../docs/implementation/decisions.md#d72)): every chokepoint that
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
	// An agent that finished has a task that finished with it, one way or
	// another. What its report said is what the task now says (D77).
	s.closeAgentTask(ctx, a, report)
}

// The task graph's own history ([D77](../../docs/implementation/decisions.md#d77)).
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

// captureAgentTask is what makes an agent's creation a row in the plan rather
// than only a line in the history. An agent is made *for* something, and
// until now the only record of what was the task text inside an agent_created
// event, which nothing could mark as done.
//
// It links rather than creates when the plan already has an open task for
// this agent — a project's chat that wrote the work down before handing it
// over should get one task, not two — and otherwise writes one down from the
// task the agent was given, falling back to its title for an agent created
// with no task at all.
func (s *Server) captureAgentTask(ctx context.Context, a state.Agent, task string) {
	open, err := s.memory().Tasks(ctx, a.Project, memory.TaskFilter{Agent: a.Name, OpenOnly: true})
	if err != nil {
		s.logf("memory: reading %s's tasks in %s: %v", a.Name, a.Project, err)
		return
	}
	if len(open) > 0 {
		s.startTask(ctx, pickTask(open, memory.TaskOpen))
		return
	}
	goal := strings.TrimSpace(task)
	if goal == "" {
		goal = strings.TrimSpace(a.Title)
	}
	if goal == "" {
		// Nothing to call it. A task whose goal is the agent's name says
		// less than the agent_created event already does.
		return
	}
	out, err := s.memory().AddTask(ctx, memory.Task{
		Project: a.Project, Agent: a.Name, Status: memory.TaskActive,
		Goal: taskGoalLine(goal), Detail: goal,
	})
	if err != nil {
		s.logf("memory: recording %s's task in %s: %v", a.Name, a.Project, err)
		return
	}
	s.captureTaskCreated(ctx, out)
}

// startTask marks a task the agent already had as being worked on now. A task
// that was blocked stays blocked: an agent starting doesn't unblock it, and
// saying it does would hide the edge that is really in the way.
func (s *Server) startTask(ctx context.Context, t memory.Task) {
	if t.Status == memory.TaskActive || t.Status == memory.TaskBlocked {
		return
	}
	out, err := s.memory().UpdateTask(ctx, t.Project, t.ID, memory.TaskPatch{Status: ptr(memory.TaskActive)})
	if err != nil {
		s.logf("memory: starting task %s in %s: %v", t.ID, t.Project, err)
		return
	}
	s.captureTaskStatus(ctx, out, t.Status)
}

// closeAgentTask is the other half: a worker that genuinely finished says how
// it went in its report, and that is what the task's status becomes.
//
//	done     → the task is done
//	failed   → the task is abandoned: it was tried, and it didn't work
//	partial  → the task stays open, with what is left attached
//	blocked  → the task stays blocked, with what stopped it attached
//
// The two that leave it open are the point of the mapping. A worker filling
// its context and stopping halfway is the ordinary case, and closing its task
// because its turn ended would lose the only record that the work isn't done.
// An agent that left no report at all is taken at the word of its finish: the
// turn completed, so the task did.
func (s *Server) closeAgentTask(ctx context.Context, a state.Agent, report memory.Report) {
	open, err := s.memory().Tasks(ctx, a.Project, memory.TaskFilter{Agent: a.Name, OpenOnly: true})
	if err != nil || len(open) == 0 {
		if err != nil {
			s.logf("memory: reading %s's tasks in %s: %v", a.Name, a.Project, err)
		}
		return
	}
	t := pickTask(open, memory.TaskActive)
	patch := memory.TaskPatch{Status: ptr(taskStatusOf(report.Status))}
	if detail := taskDetail(t.Detail, a.Name, report); detail != t.Detail {
		patch.Detail = ptr(detail)
	}
	out, err := s.memory().UpdateTask(ctx, a.Project, t.ID, patch)
	if err != nil {
		s.logf("memory: closing task %s in %s: %v", t.ID, a.Project, err)
		return
	}
	if out.Status != t.Status {
		s.captureTaskStatus(ctx, out, t.Status)
	}
}

// pickTask is which of an agent's open tasks a lifecycle event is about.
// Tasks are ordered with what is in the way first, which is the right order to
// read a plan in and the wrong one to pick from here: an agent that finished
// finished the task it was *on*, and an agent starting picks up the one
// nothing is holding up. So the preferred status wins, and the plan's own
// order breaks the tie.
func pickTask(tasks []memory.Task, prefer string) memory.Task {
	for _, t := range tasks {
		if t.Status == prefer {
			return t
		}
	}
	return tasks[0]
}

// taskStatusOf maps how an agent said its work ended onto what its task now
// is. An empty status is an agent that filed no report and finished its turn.
func taskStatusOf(reportStatus string) string {
	switch reportStatus {
	case memory.StatusPartial:
		return memory.TaskOpen
	case memory.StatusBlocked:
		return memory.TaskBlocked
	case memory.StatusFailed:
		return memory.TaskAbandoned
	default:
		return memory.TaskDone
	}
}

// taskDetail attaches what a report says is still wrong to the task it was
// about, so a task left open says why without anybody having to find the
// report. It is bounded like any other field here: what doesn't fit is left
// in the report, which is where it already is in full.
func taskDetail(detail, agent string, report memory.Report) string {
	if len(report.RemainingIssues) == 0 {
		return detail
	}
	var b strings.Builder
	b.WriteString(detail)
	if detail != "" {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Still wrong when %s finished (%s):", agent, report.Status)
	for _, issue := range report.RemainingIssues {
		fmt.Fprintf(&b, "\n- %s", issue)
	}
	out := b.String()
	if len(out) > memory.MaxContentLen {
		return detail
	}
	return out
}

// taskGoalLine is a task's goal out of the text an agent was handed: a plan
// is read by its lines, and a task handed over as three paragraphs still has
// to be one of them. The whole of it stays in the task's detail.
func taskGoalLine(text string) string {
	line := text
	if before, _, ok := strings.Cut(line, "\n"); ok {
		line = before
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = text
	}
	if len(line) > MaxTaskGoalLine {
		line = strings.TrimSpace(line[:MaxTaskGoalLine]) + "…"
	}
	return line
}

// MaxTaskGoalLine is how much of a handed-over task becomes its goal. Short
// enough that a plan of twenty tasks is still a plan.
const MaxTaskGoalLine = 160

// ptr is a pointer to a value, for the merge patches this package fills in.
func ptr[T any](v T) *T { return &v }

// agentOfBranch is the name of the project's agent whose branch this is, or
// "" when nothing made it — a branch merged from outside AgentBox is still
// worth an event, just not one attributed to an agent.
func (s *Server) agentOfBranch(ctx context.Context, project, branch string) string {
	if branch == "" {
		return ""
	}
	agents, err := s.store.Agents(ctx, project)
	if err != nil {
		return ""
	}
	for _, a := range agents {
		if a.Branch == branch {
			return a.Name
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
