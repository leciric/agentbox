package daemon

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Telling an agent whose machine isn't running. Its chat runs its AI tool
// inside that machine, so the machine has to run first: the chat preparing
// the model, then launching the tool, in a stopped machine only fails. Every
// path that sends an agent a message — the lead's tell_agent, the app's send
// box, the pull request watch, an answer to a question the agent can no
// longer be waiting on — goes through tellAgent, which starts the machine
// the way `agentbox start` does, or resumes it, before the chat sees the
// message.
//
// Starting a stopped machine takes memory, so it goes through admission
// (admission.go) like a create does. When the VM hasn't the room, the
// message is held in the chat (chat.Hold), the agent waits among the queued
// ones, and the queue loop starts it, and hands the chat what was held, once
// it fits (admitWaking). The sender is told it waits, and why.

// told is what became of a message sent to an agent.
type told struct {
	item api.ChatItem
	// woke is what its machine needed first: "" when it was running,
	// "started" or "resumed".
	woke string
	// waiting is why its machine waits to start, when the VM hasn't the
	// memory for it now: the message waits with it, and is delivered when it
	// starts.
	waiting string
}

// wakingAgent is a stopped agent with messages held for it until its
// machine can start.
type wakingAgent struct {
	agent state.Agent
	since time.Time
	// starting is set once admitted: a job is starting its machine.
	starting bool
}

// tellAgent sends a message to an agent's chat, starting or resuming its
// machine first when it isn't running, or holding the message until there's
// memory to start it. A lead has no machine of its own, and an agent that
// isn't ready (queued, being made) has none yet: the chat has those as ever.
func (s *Server) tellAgent(ctx context.Context, a state.Agent, text string, images ...api.ChatImageUpload) (told, error) {
	if a.IsLead() || a.Status != state.AgentReady {
		item, err := s.chat.Send(a, text, images...)
		return told{item: item}, err
	}
	// Most messages go to a machine that runs: no lock for those.
	if !s.isWaking(a.Ref()) {
		if inst, err := s.cfg.Incus.Instance(ctx, a.Instance); err == nil && inst.Status == "Running" {
			item, err := s.chat.Send(a, text, images...)
			return told{item: item}, err
		}
	}
	// One wake at a time, so two messages to the same stopped agent don't
	// both start its machine, and one held for it can't miss the start.
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.isWaking(a.Ref()) {
		item, err := s.chat.Hold(a, text, images...)
		return told{item: item, waiting: s.wakeReason(a.Ref())}, err
	}
	m := s.manager(s.cfg.Log)
	inst, err := s.cfg.Incus.Instance(ctx, a.Instance)
	if err != nil {
		// Incus can't say: the chat takes the message as it always has, and
		// says what went wrong if its machine can't be reached.
		s.logf("%s: looking at its machine before telling it something: %v", a.Ref(), err)
		item, err := s.chat.Send(a, text, images...)
		return told{item: item}, err
	}
	var woke string
	switch inst.Status {
	case "Running":
	case "Frozen":
		// A paused machine keeps its memory: nothing to admit.
		if err := s.diskPausedRefusal(a.Ref()); err != nil {
			return told{}, err
		}
		if err := m.Resume(ctx, a); err != nil {
			return told{}, err
		}
		woke = "resumed"
	default:
		if err := s.diskPausedRefusal(a.Ref()); err != nil {
			return told{}, err
		}
		done, wait, err := s.admitWake(ctx, a)
		if err != nil {
			return told{}, err
		}
		if wait != "" {
			item, err := s.chat.Hold(a, text, images...)
			if err != nil {
				return told{}, err
			}
			s.mu.Lock()
			s.waking[a.Ref()] = &wakingAgent{agent: a, since: time.Now()}
			s.waitReasons[a.Ref()] = wait
			s.mu.Unlock()
			s.logf("%s waits for memory to start its machine, with a message held for it: %s", a.Ref(), wait)
			s.kickQueue()
			return told{item: item, waiting: wait}, nil
		}
		err = s.startMachine(ctx, m, a)
		done()
		if err != nil {
			return told{}, err
		}
		woke = "started"
	}
	if woke != "" {
		s.refreshAgents(ctx)
	}
	item, err := s.chat.Send(a, text, images...)
	return told{item: item, woke: woke}, err
}

// startMachine starts a stopped agent's machine, with its in-agent API: what
// `agentbox start` does.
func (s *Server) startMachine(ctx context.Context, m *agent.Manager, a state.Agent) error {
	if err := s.serveAgentAPI(a.Instance); err != nil {
		return err
	}
	_, err := m.Start(ctx, a)
	return err
}

// admitWake decides whether a stopped agent's machine may start now, the
// way admitCreate does for a new one: when it may, its baseline is held
// until done is called, once the machine has started; when it may not, it
// says why.
func (s *Server) admitWake(ctx context.Context, a state.Agent) (done func(), wait string, err error) {
	shape, err := newShapes(s, ctx).of(a.Project)
	if err != nil {
		return nil, "", err
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	ref := a.Ref()
	plan, err := s.planAdmission(ctx, &agent.Waiter{Ref: ref, Project: a.Project, Need: shape.Baseline, Since: time.Now(), AnySlot: true})
	if err != nil {
		return nil, "", err
	}
	if !plan.verdicts[ref].Start {
		return nil, wakeMessage(plan.message(ref)), nil
	}
	return s.reserve(a.Project, shape.Baseline), "", nil
}

// reserve holds memory for a machine about to start, until the returned func
// is called.
func (s *Server) reserve(project string, reserved int64) func() {
	s.mu.Lock()
	s.pendingSeq++
	id := s.pendingSeq
	s.pendingCreates[id] = pendingCreate{project: project, reserved: reserved, wake: true}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.pendingCreates, id)
		s.mu.Unlock()
		s.kickQueue()
	}
}

// wakeMessage turns admission's line for a queued agent into one for a
// stopped one: it is its machine that waits, not a create.
func wakeMessage(msg string) string {
	const prefix = "queued: "
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		msg = msg[len(prefix):]
	}
	return "its machine waits for memory to start: " + msg
}

func (s *Server) isWaking(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waking[ref] != nil
}

func (s *Server) wakeReason(ref string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitReasons[ref]
}

// wakingWaiters are the stopped agents waiting to start, for admission, in
// the order they began to wait.
func (s *Server) wakingWaiters(res *shapes) ([]agent.Waiter, error) {
	s.mu.Lock()
	var waking []*wakingAgent
	for _, w := range s.waking {
		if !w.starting {
			waking = append(waking, w)
		}
	}
	s.mu.Unlock()
	sort.Slice(waking, func(i, j int) bool { return waking[i].since.Before(waking[j].since) })
	out := make([]agent.Waiter, 0, len(waking))
	for _, w := range waking {
		shape, err := res.of(w.agent.Project)
		if err != nil {
			return nil, err
		}
		out = append(out, agent.Waiter{Ref: w.agent.Ref(), Project: w.agent.Project, Need: shape.Baseline, Since: w.since, AnySlot: true})
	}
	return out, nil
}

// admitWaking starts the machines of the stopped agents the plan says now
// fit, each in a job of its own that hands its chat what was held for it.
// The queue is locked.
func (s *Server) admitWaking(plan admission) int {
	started := 0
	for _, w := range plan.waiting {
		s.mu.Lock()
		wa := s.waking[w.Ref]
		ok := wa != nil && !wa.starting && plan.verdicts[w.Ref].Start
		if ok {
			wa.starting = true
			delete(s.waitReasons, w.Ref)
		}
		s.mu.Unlock()
		if !ok {
			continue
		}
		done := s.reserve(wa.agent.Project, w.Need)
		a := wa.agent
		if _, err := s.jobs.start("start", a.Ref(), func(ctx context.Context, log io.Writer) (any, error) {
			defer done()
			return nil, s.wakeHeld(ctx, s.manager(log), a)
		}); err != nil {
			done()
			s.dropWaking(a, fmt.Sprintf("its machine couldn't be started: %v", err))
			continue
		}
		started++
	}
	return started
}

// wakeHeld starts a waiting agent's machine and hands its chat what was held
// for it; when the machine won't start, the chat says the messages were lost.
func (s *Server) wakeHeld(ctx context.Context, m *agent.Manager, a state.Agent) error {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	s.logf("%s has the memory to start now: starting its machine for the message held for it", a.Ref())
	err := s.startMachine(ctx, m, a)
	if err == nil {
		err = s.chat.Release(a)
	}
	if err != nil {
		s.dropWaking(a, fmt.Sprintf("its machine couldn't be started: %v", err))
		return err
	}
	s.mu.Lock()
	delete(s.waking, a.Ref())
	s.mu.Unlock()
	s.refreshAgents(ctx)
	return nil
}

// releaseWaking hands the chat what was held for an agent whose machine
// somebody else started: `agentbox start`, or resume.
func (s *Server) releaseWaking(a state.Agent) {
	s.mu.Lock()
	wa := s.waking[a.Ref()]
	if wa == nil || wa.starting {
		s.mu.Unlock()
		return // nothing held, or the job that starts it hands it over
	}
	delete(s.waking, a.Ref())
	delete(s.waitReasons, a.Ref())
	s.mu.Unlock()
	if err := s.chat.Release(a); err != nil {
		s.logf("%s: handing over the messages held for it: %v", a.Ref(), err)
	}
}

// dropWaking gives up on an agent's held messages: its machine won't start,
// or it was stopped or destroyed while it waited. The chat says they were
// never delivered.
func (s *Server) dropWaking(a state.Agent, why string) {
	s.mu.Lock()
	_, ok := s.waking[a.Ref()]
	delete(s.waking, a.Ref())
	delete(s.waitReasons, a.Ref())
	s.mu.Unlock()
	if ok {
		s.chat.Stop(a.Ref(), why)
	}
}

// anyWaking reports whether a stopped agent waits to start, dropping those
// that no longer can: destroyed, or started by somebody else meanwhile.
func (s *Server) anyWaking(ctx context.Context) bool {
	s.mu.Lock()
	var waking []state.Agent
	for _, w := range s.waking {
		if !w.starting {
			waking = append(waking, w.agent)
		}
	}
	s.mu.Unlock()
	left := false
	for _, a := range waking {
		if _, err := s.store.Agent(ctx, a.Project, a.Name); err != nil {
			s.dropWaking(a, "the agent is gone")
			continue
		}
		if inst, err := s.cfg.Incus.Instance(ctx, a.Instance); err == nil && inst.Status == "Running" {
			s.releaseWaking(a)
			continue
		}
		left = true
	}
	return left
}

// prTellAgent is tellAgent for the pull request watch. An agent that isn't
// ready can't be told: the watch tells the lead instead.
func (s *Server) prTellAgent(ctx context.Context, a state.Agent, text string) (told, error) {
	if a.Status != state.AgentReady {
		return told{}, fmt.Errorf("%s is %s", a.Ref(), a.Status)
	}
	// Nor can one whose machine Incus can't find: tellAgent would leave the
	// message in its chat, where nobody would see it fail.
	if _, err := s.cfg.Incus.Instance(ctx, a.Instance); err != nil {
		return told{}, err
	}
	return s.tellAgent(ctx, a, text)
}
