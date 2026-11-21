package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Agents' tasks that AgentBox stopped before sending. A create job keeps the
// agent's task in the store from the moment the agent's row is added
// (state.AgentTask) and sends it once the machine is made; the chat forgets
// it once a prompt carries it to the AI tool (package chat, tasks.go). On 20
// and 21 Nov 2026 the VM went down twice in between: agent-861
// and agent-864 came up made and idle, no task in their chats, and the
// message that woke them ("Start your task now") was all they ever heard.
//
// sendLeftTasks, as the daemon starts, puts each such task into its agent's
// chat as the turn AgentBox's stop cut short, which is what it is: then
// continueTurns, right after, does with it what it does with every one —
// carries it on in its turn, holds it for the user's Resume all after an
// unexpected stop, or leaves it for the next message with "Continue agents
// after restarts" off — and whichever turn runs first carries the task. An
// agent whose machine was still being made when AgentBox stopped — left
// "incomplete", as agent-862 was — keeps its task for when it is: starting
// it, or a message to it, finishes making it (finishCreate), and its task
// goes first.

// sendLeftTasks hands each made agent whose task never reached its chat that
// task, as a turn the restart cut short. It runs before continueTurns reads
// the turns.
func (s *Server) sendLeftTasks(ctx context.Context) {
	tasks, err := s.store.AgentTasks(ctx)
	if err != nil {
		s.logf("agents' tasks: %v", err)
		return
	}
	for _, t := range tasks {
		a, err := s.store.Agent(ctx, t.Project, t.Agent)
		switch {
		case errors.Is(err, state.ErrNotFound):
			// Gone while nothing was there to forget its task with it.
			_ = s.store.DeleteAgentTask(ctx, t.Project, t.Agent)
			continue
		case err != nil:
			s.logf("agents' tasks: %s: %v", t.Ref(), err)
			continue
		case a.Status != state.AgentReady:
			continue // its machine is still to be made: the task waits for it
		}
		s.restoreGoal(ctx, a, t)
		restored, err := s.chat.RestoreTask(a, t.Text)
		if err != nil {
			s.logf("agents' tasks: %s: %v", a.Ref(), err)
		}
		if !restored {
			// Its chat has it: the turn sending it is either recorded as
			// running, for continueTurns, or ended without the AI tool, and
			// the next prompt carries it.
			continue
		}
		s.logf("%s: AgentBox stopped before sending this agent its task; it goes with the agent's next turn", a.Ref())
		s.recordMade(ctx, a, t)
	}
}

// restoreGoal gives a the goal it was made with, as the create job does
// before sending the task, unless it has one: a job that got as far as
// sending the task had set it already.
func (s *Server) restoreGoal(ctx context.Context, a state.Agent, t state.AgentTask) {
	if t.Goal == nil {
		return
	}
	if _, has, err := s.store.AgentGoalOf(ctx, a.Project, a.Name); err == nil && !has {
		s.startGoal(ctx, a, t.Goal, "")
	}
}

// recordMade is what the create job does once an agent is made and its task
// sent, for an agent whose job didn't get to it: the project's memory and the
// rail hear of the agent, and the chat that asked for it waits on its task.
func (s *Server) recordMade(ctx context.Context, a state.Agent, t state.AgentTask) {
	s.captureEvent(ctx, a.Project, a.Name, "agent_created", map[string]any{
		"title": a.Title, "task": t.Text, "branch": a.Branch, "restored": true,
	}, "")
	s.addActiveAgent(ctx, a.Project, a.Name)
	s.record(ctx, createdEvent(a, t.Text, time.Now()))
	if t.ByLead {
		s.leadAsked(a)
	}
}

// beingMade marks the agent ref as one a job of this daemon is making,
// until the func it returns is called.
func (s *Server) beingMade(ref string) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.making == nil {
		s.making = map[string]bool{}
	}
	s.making[ref] = true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.making, ref)
	}
}

// unfinished reports whether a was left incomplete: marked creating, with
// nothing of this daemon's making it — its create job died with the daemon
// before.
func (s *Server) unfinished(ctx context.Context, a state.Agent) bool {
	if a.IsLead() || a.Status != state.AgentCreating {
		return false
	}
	s.mu.Lock()
	making := s.making[a.Ref()]
	s.mu.Unlock()
	if making {
		return false
	}
	// `agentbox vm migrate` makes machines anew with the agent marked
	// creating too (migration.go).
	busy, err := s.store.HasRunningJob(ctx, "recreate", a.Ref())
	return err == nil && !busy
}

// finishCreate starts the job that finishes making a, which AgentBox's stop
// left incomplete (agent.FinishCreate), and once it is made sends it its
// task, kept since its create began, and then what was written to it
// meanwhile. A create job, so the agent reads as being made while it runs.
func (s *Server) finishCreate(a state.Agent) (api.Job, error) {
	s.mu.Lock()
	if s.making[a.Ref()] {
		s.mu.Unlock()
		return api.Job{}, nil // a request a moment ago started it
	}
	s.mu.Unlock()
	if err := s.diskRefusal("making " + a.Ref() + "'s machine"); err != nil {
		return api.Job{}, err
	}
	done := s.beingMade(a.Ref())
	j, err := s.launchJob("create", a.Project, func(ctx context.Context, log io.Writer) (any, error) {
		defer done()
		a, err := s.store.Agent(ctx, a.Project, a.Name)
		if err != nil {
			return nil, err
		}
		t, kept, err := s.store.AgentTaskOf(ctx, a.Project, a.Name)
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(log, "AgentBox stopped while %s was being made: finishing it\n", a.Ref())
		made, err := s.manager(log).FinishCreate(ctx, a, t.Text)
		if err != nil {
			return nil, err
		}
		ctx = context.WithoutCancel(ctx)
		s.refreshAgents(ctx)
		if kept {
			s.restoreGoal(ctx, made, t)
			s.recordMade(ctx, made, t)
			if _, err := s.chat.Send(made, t.Text); err != nil {
				_, _ = fmt.Fprintf(log, "the agent was made, but its task couldn't be sent: %v\n", err)
			}
		}
		if err := s.chat.Release(made); err != nil {
			_, _ = fmt.Fprintf(log, "the agent was made, but what was written to it meanwhile couldn't be sent: %v\n", err)
		}
		return s.agentReady(ctx, made)
	})
	if err != nil {
		done()
	}
	return j, err
}
