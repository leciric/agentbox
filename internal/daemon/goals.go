package daemon

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// Agent goals (api.AgentGoal).
//
// A goal the AI tool keeps itself (its own /goal) only shows: package chat
// reads it from the adapter and goalFromTool keeps it, so the agent's row
// and header carry it.
//
// A goal create_agent gives, or one set later on a running agent (setGoal:
// the app, the lead's set_goal, `agentbox goal`), is the daemon's to drive
// (source "agentbox"). The agent is told it with its task, or as a message
// when it is set later. Each time it ends a turn, the daemon
// runs the goal's checks itself — an open pull request, green CI, a command
// that must exit 0 in the agent's machine — and, while one fails, sends the
// agent back with what is missing instead of reporting a finish, like the
// "carry on" nudge for a turn that ended waiting on work. It stops when every
// check passes (met: the ordinary finish notice follows), when the agent says
// it is blocked, or when the budget of continuations runs out (blocked: the
// one time a goal counts as needing the user). Uniform for every tool: for
// Claude Code AgentBox doesn't also set its /goal, whose check reads only the
// transcript and can't see CI.

// goalBlockedLine is how an agent working towards a goal says it can't go on
// without the user: a line of its last message starting "GOAL BLOCKED:".
var goalBlockedLine = regexp.MustCompile(`(?im)^[\s>*_` + "`" + `]*GOAL BLOCKED:?\s*(.*)$`)

// goalCheckTimeout bounds one check: a command, a GitHub read.
const goalCheckTimeout = 5 * time.Minute

// goalOf is an agent's goal for the API: nil when it has none.
func (s *Server) goalOf(ctx context.Context, project, name string) *api.AgentGoal {
	g, ok, err := s.store.AgentGoalOf(ctx, project, name)
	if err != nil || !ok {
		return nil
	}
	return &g.AgentGoal
}

// chatGoalOf is chat.Manager.GoalOf.
func (s *Server) chatGoalOf(a state.Agent) (api.AgentGoal, bool) {
	g, ok, err := s.store.AgentGoalOf(context.Background(), a.Project, a.Name)
	if err != nil || !ok {
		return api.AgentGoal{}, false
	}
	return g.AgentGoal, true
}

// goalFromTool is chat.Manager.GoalChanged: the agent's AI tool reported its
// own goal. A goal the daemon drives stays the agent's goal while it is
// active; the tool's is kept otherwise.
func (s *Server) goalFromTool(a state.Agent, g api.AgentGoal) {
	ctx := context.Background()
	if cur, ok, err := s.store.AgentGoalOf(ctx, a.Project, a.Name); err == nil && ok && cur.Source == api.GoalSourceAgentBox && cur.Status == api.GoalActive {
		return
	}
	if err := s.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
		s.logf("%s: keeping its goal: %v", a.Ref(), err)
	}
}

// checkGoal refuses a goal the daemon could never drive, and fills in its
// budget.
func checkGoal(spec *api.GoalSpec) (*api.GoalSpec, error) {
	if spec == nil {
		return nil, nil
	}
	out := *spec
	out.Objective = strings.TrimSpace(out.Objective)
	if out.Objective == "" {
		return nil, errors.New("a goal needs an objective: what done looks like")
	}
	switch {
	case out.Budget < 0:
		return nil, fmt.Errorf("a goal's budget is %d: it is how many times the agent is sent back, 1 to %d", out.Budget, api.MaxGoalBudget)
	case out.Budget == 0:
		out.Budget = api.DefaultGoalBudget
	case out.Budget > api.MaxGoalBudget:
		return nil, fmt.Errorf("a goal's budget is %d: at most %d", out.Budget, api.MaxGoalBudget)
	}
	out.Checks = nil
	for _, c := range spec.Checks {
		c.Kind, c.Command = strings.TrimSpace(c.Kind), strings.TrimSpace(c.Command)
		c.Met, c.Detail = nil, ""
		switch c.Kind {
		case api.GoalCheckPROpen, api.GoalCheckCIGreen:
			c.Command = ""
		case api.GoalCheckCommand:
			if c.Command == "" {
				return nil, errors.New("a command check needs its command: a shell line that exits 0 once the goal is met")
			}
		default:
			return nil, fmt.Errorf("a goal check is %q: it is pr_open, ci_green or command", c.Kind)
		}
		out.Checks = append(out.Checks, c)
	}
	return &out, nil
}

// startGoal keeps a new agent's goal and shows it in its chat, and returns
// what its task becomes: the task, with the goal and how it is checked.
func (s *Server) startGoal(ctx context.Context, a state.Agent, spec *api.GoalSpec, task string) string {
	if spec == nil {
		return task
	}
	g := api.AgentGoal{
		Objective: spec.Objective, Source: api.GoalSourceAgentBox, Status: api.GoalActive,
		Checks: spec.Checks, Budget: spec.Budget, UpdatedAt: time.Now(),
	}
	if err := s.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
		s.logf("%s: keeping its goal: %v", a.Ref(), err)
		return task
	}
	if _, err := s.chat.ShowGoal(a, g, "set"); err != nil {
		s.logf("%s: showing its goal: %v", a.Ref(), err)
	}
	return strings.TrimSpace(task + "\n\n" + goalBrief(g))
}

// goalBrief is what an agent is told of its goal with its task.
func goalBrief(g api.AgentGoal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[AgentBox: you have a goal: %s", g.Objective)
	if len(g.Checks) > 0 {
		b.WriteString("\nWhen you end a turn, AgentBox checks it itself:")
		for _, c := range g.Checks {
			b.WriteString("\n- " + describeCheck(c))
		}
		b.WriteString("\nWhile a check fails you are sent back with what is missing")
	} else {
		b.WriteString("\nYou are done when you end a turn with it met")
	}
	fmt.Fprintf(&b, ", at most %d times. Only if you can't go on without the user, end your message with a line `GOAL BLOCKED: <what you need>`.]", g.Budget)
	return b.String()
}

func describeCheck(c api.GoalCheck) string {
	switch c.Kind {
	case api.GoalCheckPROpen:
		return "your branch has an open pull request (pr_open)"
	case api.GoalCheckCIGreen:
		return "every CI check on your pull request passed (ci_green)"
	default:
		return fmt.Sprintf("`%s` exits 0 in your worktree", c.Command)
	}
}

// goalAfterTurn is an agent's turn ending as its goal sees it. It reports
// whether it handled the ending — the agent sent back, or blocked and the
// user told — so the caller reports no finish; false lets the ordinary
// finish go on, the goal met or no goal to drive.
func (s *Server) goalAfterTurn(ctx context.Context, a state.Agent, end chat.TurnEnd) bool {
	stored, ok, err := s.store.AgentGoalOf(ctx, a.Project, a.Name)
	if err != nil || !ok || stored.Source != api.GoalSourceAgentBox || stored.Status != api.GoalActive {
		return false
	}
	g := stored.AgentGoal
	if m := goalBlockedLine.FindStringSubmatch(end.LastMessage); m != nil {
		g.LastUnmet = "The agent says: " + cmp.Or(strings.TrimSpace(m[1]), "it is blocked")
		s.blockGoal(ctx, a, g)
		return true
	}
	// Checks have time of their own: a command may take minutes, longer than
	// the finish it decides is given.
	checks, cancel := context.WithTimeout(context.WithoutCancel(ctx), goalCheckTimeout+time.Minute)
	missing := s.runGoalChecks(checks, a, &g)
	cancel()
	g.UpdatedAt = time.Now()
	if len(missing) == 0 {
		g.Status, g.LastUnmet = api.GoalMet, ""
		s.saveGoal(ctx, a, g, api.GoalMet)
		return false
	}
	g.Checked++
	g.LastUnmet = strings.Join(missing, "; ")
	if g.Continuations >= g.Budget {
		g.LastUnmet = fmt.Sprintf("Sent back %d times and still: %s", g.Continuations, g.LastUnmet)
		s.blockGoal(ctx, a, g)
		return true
	}
	g.Continuations++
	s.saveGoal(ctx, a, g, "checked")
	s.setOutcome(a, "", "")
	// Hidden: the goal row just added says what is missing in the app's
	// words; the prose is for the model.
	if err := s.chat.Notice(a, goalNudge(g, missing), chat.NoticeOptions{Act: true, Hidden: true}); err != nil {
		s.logf("%s: sending it back to its goal: %v", a.Ref(), err)
		return false
	}
	s.logf("%s: goal not met (%s); sent back, %d of %d", a.Ref(), g.LastUnmet, g.Continuations, g.Budget)
	return true
}

// goalNudge is what an agent is sent back with.
func goalNudge(g api.AgentGoal, missing []string) string {
	return fmt.Sprintf("[AgentBox: your goal isn't met yet: %s\nStill missing:\n- %s\n"+
		"Carry on until it is (continuation %d of %d). If you are waiting on CI or other work, wait for it in this turn with one blocking command (`gh pr checks <n> --watch`). "+
		"Only if you can't go on without the user, end your message with a line `GOAL BLOCKED: <what you need>`.]",
		g.Objective, strings.Join(missing, "\n- "), g.Continuations, g.Budget)
}

// blockGoal stops driving a goal that can't be met without the user, and
// tells them: it is the agent asking.
func (s *Server) blockGoal(ctx context.Context, a state.Agent, g api.AgentGoal) {
	note := "Goal blocked: " + g.LastUnmet
	// The agent is asking before its goal reads blocked: whoever sees the
	// goal blocked (the app, a test) sees it asking too, not the outcome of
	// the turn before.
	s.markToldWaiting(a, false)
	s.setOutcome(a, api.AgentOutcomeAsking, note)
	g.Status, g.UpdatedAt = api.GoalBlocked, time.Now()
	s.saveGoal(ctx, a, g, api.GoalBlocked)
	s.record(ctx, api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentAsking, Outcome: api.AgentOutcomeAsking, Summary: note, At: time.Now(),
	})
	s.tellLeadAbout(ctx, a, fmt.Sprintf("[AgentBox: %s (%s) is blocked on its goal %q and is waiting: %s. "+
		"Tell it what it needs with tell_agent(%q), or ask the user if only they know.]",
		a.Name, titleOrNone(a.Title), g.Objective, g.LastUnmet, a.Name),
		chat.NoticeOptions{Act: s.leadIsWaiting(a)}, peerEvent{Status: state.RemoteAsking, Summary: note})
}

// saveGoal keeps g and adds its row to the agent's chat.
func (s *Server) saveGoal(ctx context.Context, a state.Agent, g api.AgentGoal, event string) {
	if err := s.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
		s.logf("%s: keeping its goal: %v", a.Ref(), err)
	}
	if _, err := s.chat.ShowGoal(a, g, event); err != nil {
		s.logf("%s: showing its goal: %v", a.Ref(), err)
	}
}

// runGoalChecks runs g's checks, recording how each went on it, and returns
// what is missing: nothing when every one passed.
func (s *Server) runGoalChecks(ctx context.Context, a state.Agent, g *api.AgentGoal) []string {
	var missing []string
	var pr *api.PullRequest
	prRead := false
	readPR := func() *api.PullRequest {
		if !prRead {
			prRead = true
			pr = s.prFor(ctx, a)
		}
		return pr
	}
	for i := range g.Checks {
		c := &g.Checks[i]
		met, detail := false, ""
		switch c.Kind {
		case api.GoalCheckPROpen:
			switch p := readPR(); {
			case p == nil:
				detail = "no pull request from your branch yet"
			case p.State != "open":
				detail = fmt.Sprintf("pull request #%d is %s", p.Number, p.State)
			default:
				met, detail = true, fmt.Sprintf("#%d is open", p.Number)
			}
		case api.GoalCheckCIGreen:
			met, detail = s.ciGreen(ctx, a, readPR())
		case api.GoalCheckCommand:
			met, detail = s.goalCommand(ctx, a, c.Command)
		}
		c.Met, c.Detail = &met, detail
		if !met {
			missing = append(missing, describeCheck(*c)+": "+detail)
		}
	}
	return missing
}

// ciGreen checks the agent's pull request's CI.
func (s *Server) ciGreen(ctx context.Context, a state.Agent, pr *api.PullRequest) (bool, string) {
	if pr == nil {
		return false, "no pull request from your branch yet"
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return false, "its project couldn't be read: " + err.Error()
	}
	client, repo, err := s.githubFor(p)
	if err != nil {
		return false, "GitHub couldn't be reached: " + err.Error()
	}
	read, cancel := context.WithTimeout(ctx, goalCheckTimeout)
	defer cancel()
	got, err := client.PullRequestByNumber(read, repo, pr.Number)
	if err != nil {
		return false, fmt.Sprintf("#%d couldn't be read from GitHub: %v", pr.Number, err)
	}
	switch got.Checks {
	case "passing":
		return true, fmt.Sprintf("#%d's checks passed", pr.Number)
	case "pending":
		return false, fmt.Sprintf("#%d's checks are still running", pr.Number)
	case "failing":
		return false, fmt.Sprintf("#%d's checks failed (`gh pr checks %d`)", pr.Number, pr.Number)
	default:
		return false, fmt.Sprintf("no checks have reported on #%d yet", pr.Number)
	}
}

// goalCommand runs a command check in the agent's worktree.
func (s *Server) goalCommand(ctx context.Context, a state.Agent, command string) (bool, string) {
	run, cancel := context.WithTimeout(ctx, goalCheckTimeout)
	defer cancel()
	var out bytes.Buffer
	err := s.manager(nil).Exec(run, a, command, nil, &out, &out)
	tail := strings.TrimSpace(out.String())
	if len(tail) > 400 {
		tail = "…" + tail[len(tail)-400:]
	}
	if err == nil {
		return true, "exited 0"
	}
	if tail != "" {
		return false, fmt.Sprintf("failed (%v): %s", err, tail)
	}
	return false, fmt.Sprintf("failed: %v", err)
}

// setGoalOn sets or changes an existing agent's goal, and sends it to work
// towards it: a working agent reads it mid-turn (its turn's end then checks
// the new goal), an idle or finished one starts a turn on it, its machine
// started first when it was stopped. A new goal starts afresh: its budget is
// all left. Over an active goal the AI tool keeps itself (its own /goal) it
// is AgentBox's that drives the agent from then on (goalFromTool).
func (s *Server) setGoalOn(ctx context.Context, a state.Agent, spec api.GoalSpec) (api.GoalResult, error) {
	checked, err := checkGoal(&spec)
	if err != nil {
		return api.GoalResult{}, err
	}
	changed := false
	if cur, ok, err := s.store.AgentGoalOf(ctx, a.Project, a.Name); err != nil {
		return api.GoalResult{}, err
	} else if ok && cur.Source == api.GoalSourceAgentBox && cur.Status == api.GoalActive {
		changed = true
	}
	g := api.AgentGoal{
		Objective: checked.Objective, Source: api.GoalSourceAgentBox, Status: api.GoalActive,
		Checks: checked.Checks, Budget: checked.Budget, UpdatedAt: time.Now(),
	}
	if err := s.store.SetAgentGoal(ctx, state.AgentGoal{Project: a.Project, Agent: a.Name, AgentGoal: g}); err != nil {
		return api.GoalResult{}, err
	}
	if _, err := s.chat.ShowGoal(a, g, "set"); err != nil {
		s.logf("%s: showing its goal: %v", a.Ref(), err)
	}
	// Whatever it said last (asking on a blocked goal, finished) is over: it
	// is back at work.
	s.setOutcome(a, "", "")
	text := goalBrief(g)
	if changed {
		text = "[AgentBox: your goal changed; the one before no longer counts.]\n" + text
	}
	t, err := s.tellAgent(ctx, a, text)
	if err != nil {
		return api.GoalResult{Goal: g}, fmt.Errorf("the goal is set, but %s couldn't be told it: %w", a.Name, err)
	}
	told := "turn"
	if t.item.Kind == "aside" {
		told = "aside"
	}
	s.logf("%s: goal set on a running agent (%s)", a.Ref(), told)
	return api.GoalResult{Goal: g, Told: told, Woke: t.woke}, nil
}

// clearGoalOn clears an agent's goal: AgentBox stops checking it, and the
// agent reads that the next time it is written to. An active goal the AI
// tool keeps itself is the tool's to clear, with /goal clear in its chat.
func (s *Server) clearGoalOn(ctx context.Context, a state.Agent) (api.GoalResult, error) {
	cur, ok, err := s.store.AgentGoalOf(ctx, a.Project, a.Name)
	if err != nil {
		return api.GoalResult{}, err
	}
	if !ok || cur.Status == api.GoalCleared {
		return api.GoalResult{}, fmt.Errorf("%s has no goal to clear", a.Name)
	}
	if cur.Source == api.GoalSourceTool && cur.Status == api.GoalActive {
		return api.GoalResult{}, fmt.Errorf("%s's goal is its AI tool's own /goal: send /goal clear in its chat", a.Name)
	}
	g := cur.AgentGoal
	wasActive := g.Status == api.GoalActive
	g.Status, g.UpdatedAt = api.GoalCleared, time.Now()
	s.saveGoal(ctx, a, g, api.GoalCleared)
	if wasActive && g.Source == api.GoalSourceAgentBox {
		if err := s.chat.Notice(a, "[AgentBox: your goal was cleared: AgentBox no longer checks it or sends you back to it.]", chat.NoticeOptions{Hidden: true}); err != nil {
			s.logf("%s: telling it its goal was cleared: %v", a.Ref(), err)
		}
	}
	return api.GoalResult{Goal: g}, nil
}

// putGoal is PUT /v1/agents/{project}/{agent}/goal (and the lead's
// /v1/project/agents/{agent}/goal, from which the agent's finish goes to it).
func (s *Server) putGoal(from agentFrom, byLead bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		var spec api.GoalSpec
		if err := readJSON(r, &spec); err != nil {
			return err
		}
		if byLead {
			s.leadAsked(a)
		}
		res, err := s.setGoalOn(r.Context(), a, spec)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, res)
	}
}

// deleteGoal is DELETE …/goal: the agent's goal cleared.
func (s *Server) deleteGoal(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		res, err := s.clearGoalOn(r.Context(), a)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, res)
	}
}
