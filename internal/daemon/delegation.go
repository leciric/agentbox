package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Delegation: an agent hands part of its task to a sub-agent of its own, on
// any of the AI tools, and waits for what it comes back with. A sub-agent is
// a real AgentBox agent — its own machine, a branch from its parent's, made
// through memory admission like any other, shown under its parent in the app
// — whose Parent names the agent that started it. The delegation is the
// parent's record of it (state.Delegation): running until the sub-agent
// genuinely finishes a turn, is cancelled, or can't be made, and kept with
// its result after the sub-agent is retired, which happens by itself once it
// is done unless the parent asked to keep it.
//
// The parent reaches it over its own socket (agentapi.go), through the
// memory MCP server's delegate_task, task_status, wait_for_task and
// cancel_task (internal/cli/memory.go).

const (
	// maxDelegationDepth is how deep sub-agents go: an agent's sub-agent may
	// start sub-agents of its own, and theirs may not. Each level is a machine
	// and a session, and a tree any deeper is one nobody can follow.
	maxDelegationDepth = 2
	// maxChildren is how many sub-agents one agent may have running at once.
	maxChildren = 3
	// maxDelegationWait is the longest one wait_for_task call waits. An MCP
	// call that never answers is one the AI tool may give up on itself.
	maxDelegationWait = 30 * time.Minute
	// delegationPoll is how often a wait looks again for what no signal
	// brings: the job making the sub-agent failed, or it was destroyed.
	delegationPoll = 5 * time.Second
	// delegationResultMax bounds what a parent is told a sub-agent said, in
	// a notice; task_status gives the whole of it.
	delegationResultMax = 4000
)

// delegatedTask is what a sub-agent is sent: its task, and what being one
// means for how it finishes.
func delegatedTask(parent, task string) string {
	return task + "\n\n---\n\n" + fmt.Sprintf("[AgentBox: you are a sub-agent of %s, which delegated this task to you and is waiting for "+
		"what you come back with. Commit your work on your own branch; don't push it or open a pull request unless the task "+
		"says to: %s merges it. Call the memory server's report tool when you are done, and end with a short summary of what "+
		"you did and what is left: your last message is what %s is given.]", parent, parent, parent)
}

// delegate answers POST /v1/self/delegations: the agent behind the socket
// starts a sub-agent on a task.
func (s *Server) delegate(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		parent, err := s.store.AgentByInstance(ctx, instance)
		if err != nil {
			return err
		}
		var req api.DelegateRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		d, err := s.startDelegation(ctx, parent, req)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusAccepted, s.delegationInfo(ctx, d))
	}
}

// startDelegation makes parent a sub-agent for req, held to the depth and
// the number of children an agent may have.
func (s *Server) startDelegation(ctx context.Context, parent state.Agent, req api.DelegateRequest) (state.Delegation, error) {
	title, task := strings.TrimSpace(req.Title), strings.TrimSpace(req.Task)
	if title == "" || task == "" {
		return state.Delegation{}, errors.New("a sub-agent needs a title and a task")
	}
	ai := req.AI
	if ai == "" {
		ai = "claude"
	}
	switch ai {
	case "claude", "codex", "opencode":
	default:
		return state.Delegation{}, fmt.Errorf("ai is %q: a sub-agent runs claude, codex or opencode", ai)
	}
	depth, err := s.delegationDepth(ctx, parent)
	if err != nil {
		return state.Delegation{}, err
	}
	if depth >= maxDelegationDepth {
		return state.Delegation{}, fmt.Errorf("%s is a sub-agent %d level(s) down, and sub-agents go at most %d deep: do this part yourself",
			parent.Name, depth, maxDelegationDepth)
	}
	running, err := s.store.Delegations(ctx, parent.Project, parent.Name, true)
	if err != nil {
		return state.Delegation{}, err
	}
	if len(running) >= maxChildren {
		return state.Delegation{}, fmt.Errorf("%s already has %d sub-agents running, the most an agent may: wait for one, or cancel one",
			parent.Name, len(running))
	}
	// The name first, so the delegation names its sub-agent from the start,
	// whether it is made now or waits in the queue.
	name, err := s.manager(nil).NextName(ctx, parent.Project)
	if err != nil {
		return state.Delegation{}, err
	}
	d := state.Delegation{
		ID: state.NewDelegationID(), Project: parent.Project, Parent: parent.Name, Child: name,
		Title: title, Task: task, AI: ai, Keep: req.Keep, Status: state.DelegationRunning, CreatedAt: time.Now(),
	}
	if err := s.store.AddDelegation(ctx, d); err != nil {
		return state.Delegation{}, err
	}
	create := api.CreateAgentRequest{
		Project: parent.Project, Name: name, Title: title, Task: delegatedTask(parent.Name, task),
		AI: ai, Model: req.Model, Effort: req.Effort, Size: req.Size, Branch: req.Branch,
		From:   parent.Branch,
		Parent: parent.Name,
		// Its parent is the one waiting on it: the project's chat is told it
		// finished, without a turn of its own spent on it.
		FinishNotice:  state.FinishNoticesOff,
		GitHubAccount: parent.GitHubAccount,
	}
	if ai == "claude" && parent.AI == "claude" {
		create.ClaudeAccount = parent.ClaudeAccount
	}
	job, err := s.createAgentJob(ctx, create, false)
	if err != nil {
		_, _ = s.store.FinishDelegation(context.WithoutCancel(ctx), d.ID, state.DelegationFailed, err.Error(), time.Now())
		return state.Delegation{}, err
	}
	d.Job = job.ID
	if err := s.store.SetDelegationJob(ctx, d.ID, job.ID); err != nil {
		s.logf("delegation %s: recording its job: %v", d.ID, err)
	}
	s.captureEvent(ctx, parent.Project, parent.Name, "agent_delegated", map[string]any{"agent": name, "title": title, "ai": ai}, "")
	return d, nil
}

// delegationDepth is how many agents up an agent's parents go: 0 for one the
// user or the project's chat made.
func (s *Server) delegationDepth(ctx context.Context, a state.Agent) (int, error) {
	depth := 0
	seen := map[string]bool{a.Name: true}
	for a.Parent != "" {
		depth++
		if seen[a.Parent] || depth > maxDelegationDepth {
			break // a loop, or deeper than allowed already: either way, no further
		}
		seen[a.Parent] = true
		parent, err := s.store.Agent(ctx, a.Project, a.Parent)
		if errors.Is(err, state.ErrNotFound) {
			break // retired: it still counts as a level
		}
		if err != nil {
			return 0, err
		}
		a = parent
	}
	return depth, nil
}

// listDelegations answers GET /v1/self/delegations.
func (s *Server) listDelegations(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		parent, err := s.store.AgentByInstance(ctx, instance)
		if err != nil {
			return err
		}
		found, err := s.store.Delegations(ctx, parent.Project, parent.Name, false)
		if err != nil {
			return err
		}
		out := make([]api.Delegation, 0, len(found))
		for _, d := range found {
			out = append(out, s.delegationInfo(ctx, s.settleDelegation(ctx, d)))
		}
		return writeJSON(w, http.StatusOK, out)
	}
}

// ownDelegation is the delegation a request names, refused unless the agent
// behind the socket made it.
func (s *Server) ownDelegation(r *http.Request, instance string) (state.Agent, state.Delegation, error) {
	parent, err := s.store.AgentByInstance(r.Context(), instance)
	if err != nil {
		return state.Agent{}, state.Delegation{}, err
	}
	d, err := s.store.Delegation(r.Context(), parent.Project, r.PathValue("id"))
	if err != nil {
		return state.Agent{}, state.Delegation{}, err
	}
	if d.Parent != parent.Name {
		return state.Agent{}, state.Delegation{}, fmt.Errorf("delegation %s: %w", d.ID, state.ErrNotFound)
	}
	return parent, d, nil
}

// getDelegation answers GET /v1/self/delegations/{id}, waiting up to ?wait=
// seconds for it to end.
func (s *Server) getDelegation(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		_, d, err := s.ownDelegation(r, instance)
		if err != nil {
			return err
		}
		var wait time.Duration
		if v := r.URL.Query().Get("wait"); v != "" {
			secs, err := strconv.Atoi(v)
			if err != nil || secs < 0 {
				return fmt.Errorf("wait is %q: a number of seconds", v)
			}
			wait = min(time.Duration(secs)*time.Second, maxDelegationWait)
		}
		d = s.awaitDelegation(r.Context(), d, wait)
		return writeJSON(w, http.StatusOK, s.delegationInfo(r.Context(), d))
	}
}

// cancelDelegation answers POST /v1/self/delegations/{id}/cancel: the
// sub-agent's turn is stopped and it is retired, or stopped when it was to be
// kept or has uncommitted work.
func (s *Server) cancelDelegation(instance string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		parent, d, err := s.ownDelegation(r, instance)
		if err != nil {
			return err
		}
		if d.Status != state.DelegationRunning {
			return writeJSON(w, http.StatusOK, s.delegationInfo(ctx, d))
		}
		ended, err := s.store.FinishDelegation(ctx, d.ID, state.DelegationCancelled, "cancelled by "+parent.Name, time.Now())
		if err != nil {
			return err
		}
		if ended {
			s.signalDelegation(d.ID)
			if child, err := s.store.Agent(ctx, d.Project, d.Child); err == nil {
				s.retireSubAgent(ctx, child, d.Keep)
			}
		}
		d, err = s.store.Delegation(ctx, d.Project, d.ID)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, s.delegationInfo(ctx, d))
	}
}

// awaitDelegation waits up to wait for a running delegation to end, and
// answers with it as it is then.
func (s *Server) awaitDelegation(ctx context.Context, d state.Delegation, wait time.Duration) state.Delegation {
	d = s.settleDelegation(ctx, d)
	if wait <= 0 || d.Status != state.DelegationRunning {
		return d
	}
	s.mu.Lock()
	s.delegationWaiters[d.ID]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.delegationWaiters[d.ID]--; s.delegationWaiters[d.ID] <= 0 {
			delete(s.delegationWaiters, d.ID)
		}
		s.mu.Unlock()
	}()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		signal := s.delegationSignal(d.ID)
		select {
		case <-ctx.Done():
			return d
		case <-deadline.C:
			return d
		case <-signal:
		case <-time.After(delegationPoll):
		}
		cur, err := s.store.Delegation(context.WithoutCancel(ctx), d.Project, d.ID)
		if err != nil {
			return d
		}
		if d = s.settleDelegation(ctx, cur); d.Status != state.DelegationRunning {
			return d
		}
	}
}

// delegationSignal is closed when the delegation next ends.
func (s *Server) delegationSignal(id string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.delegationDone[id]
	if !ok {
		ch = make(chan struct{})
		s.delegationDone[id] = ch
	}
	return ch
}

// signalDelegation wakes everybody waiting on a delegation.
func (s *Server) signalDelegation(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.delegationDone[id]; ok {
		close(ch)
		delete(s.delegationDone, id)
	}
}

// delegationWaited reports whether a wait_for_task is waiting on it now.
func (s *Server) delegationWaited(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delegationWaiters[id] > 0
}

// settleDelegation ends a running delegation whose sub-agent can never
// finish it: the job making it failed, or it is gone.
func (s *Server) settleDelegation(ctx context.Context, d state.Delegation) state.Delegation {
	if d.Status != state.DelegationRunning {
		return d
	}
	_, err := s.store.Agent(ctx, d.Project, d.Child)
	if err == nil || !errors.Is(err, state.ErrNotFound) {
		return d
	}
	why := "the sub-agent is gone: it was destroyed before it finished"
	if d.Job != "" {
		if j, _, err := s.jobs.lookup(ctx, d.Job); err == nil {
			switch j.Status {
			case api.JobRunning:
				return d // still being made
			case api.JobFailed, api.JobCancelled:
				why = "its machine couldn't be made: " + j.Error
			}
		}
	} else if time.Since(d.CreatedAt) < time.Minute {
		return d
	}
	at := time.Now()
	if ended, err := s.store.FinishDelegation(context.WithoutCancel(ctx), d.ID, state.DelegationFailed, why, at); err == nil && ended {
		d.Status, d.Result, d.FinishedAt = state.DelegationFailed, why, at
		s.signalDelegation(d.ID)
	}
	return d
}

// delegationFinished is a sub-agent genuinely finishing a turn: its
// delegation is done, with its last message as the result. Whoever waits is
// woken; a parent that isn't waiting is told in a message. The sub-agent is
// retired unless it was to be kept.
func (s *Server) delegationFinished(ctx context.Context, a state.Agent, last string) {
	d, ok, err := s.store.DelegationOf(ctx, a.Project, a.Name)
	if err != nil || !ok {
		return
	}
	ended, err := s.store.FinishDelegation(ctx, d.ID, state.DelegationDone, last, time.Now())
	if err != nil || !ended {
		return
	}
	s.signalDelegation(d.ID)
	if !s.delegationWaited(d.ID) {
		if parent, err := s.store.Agent(ctx, a.Project, d.Parent); err == nil {
			notice := fmt.Sprintf("[AgentBox: your sub-agent %s (%q) finished, on branch %s. task_status with id %s gives its result and report. It said:]\n\n%s",
				a.Name, d.Title, a.Branch, d.ID, clipText(last, delegationResultMax))
			if _, err := s.tellAgent(ctx, parent, notice); err != nil {
				s.logf("%s: telling it its sub-agent %s finished: %v", parent.Ref(), a.Name, err)
			}
		}
	}
	s.retireSubAgent(ctx, a, d.Keep)
}

// retireSubAgent frees a sub-agent whose task ended, in the background: its
// turn has only just ended, and the finish is still being told. One that was
// to be kept is left running when it finished, and stopped when cancelled.
func (s *Server) retireSubAgent(ctx context.Context, a state.Agent, keep bool) {
	s.retireRun(ctx, a, keep, "its parent's task")
}

// retireRun frees an agent made for one run — a sub-agent's task or a
// scheduled run — once it has ended. It is destroyed when its work is all
// committed, which keeps its branch and its media, and stopped otherwise, so
// nothing uncommitted is lost. keep leaves it as it is, but stops its turn.
func (s *Server) retireRun(ctx context.Context, a state.Agent, keep bool, what string) {
	if keep {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		cur, err := s.store.Agent(ctx, a.Project, a.Name)
		if err != nil || cur.Status == state.AgentQueued {
			if err == nil {
				// Never made: out of the queue, and nothing else to free.
				if err := s.store.RemoveAgent(ctx, cur.Project, cur.Name); err != nil {
					s.logf("%s: taking it out of the queue once %s ended: %v", cur.Ref(), what, err)
				}
				s.refreshAgents(ctx)
			}
			return
		}
		how := api.RetireDestroy
		if changesOf(cur).Dirty {
			how = api.RetireStop
		}
		s.chat.Stop(cur.Ref(), what+" ended")
		if err := s.retireOne(ctx, s.manager(s.cfg.Log), cur, how); err != nil {
			s.logf("%s: retiring it once %s ended: %v", cur.Ref(), what, err)
		}
		s.refreshAgents(ctx)
	}()
}

// delegationInfo is a delegation as its parent reads it.
func (s *Server) delegationInfo(ctx context.Context, d state.Delegation) api.Delegation {
	out := api.Delegation{
		ID: d.ID, Agent: d.Child, Parent: d.Parent, Title: d.Title, Task: d.Task, AI: d.AI, Keep: d.Keep,
		Status: d.Status, Result: d.Result, CreatedAt: d.CreatedAt, AgentState: "gone",
	}
	if !d.FinishedAt.IsZero() {
		out.FinishedAt = &d.FinishedAt
	}
	if child, err := s.store.Agent(ctx, d.Project, d.Child); err == nil {
		out.Branch = child.Branch
		if info, err := s.describe(ctx, child); err == nil {
			out.AgentState = info.State
		}
	} else if d.Status == state.DelegationRunning {
		out.AgentState = "creating"
	}
	if reports, err := s.memory().Reports(ctx, d.Project, d.Child); err == nil {
		for _, r := range reports {
			if !r.CreatedAt.Before(d.CreatedAt) {
				rep := apiReport(r)
				out.Report = &rep
				break // newest first
			}
		}
	}
	return out
}

// clipText cuts text to at most n bytes, at a rune, saying it was cut.
func clipText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n[…cut]"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
