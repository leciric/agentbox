package daemon

import (
	"context"
	"fmt"

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
// message. Nothing waits for memory to start one: the VM's pressure decides
// only when heavy commands run (pressure.go).

// told is what became of a message sent to an agent.
type told struct {
	item api.ChatItem
	// woke is what its machine needed first: "" when it was running,
	// "started" or "resumed".
	woke string
}

// tellAgent sends a message to an agent's chat, starting or resuming its
// machine first when it isn't running. A lead has no machine of its own, and an agent that
// isn't ready (queued, being made) has none yet: the chat has those as ever.
func (s *Server) tellAgent(ctx context.Context, a state.Agent, text string, images ...api.ChatImageUpload) (told, error) {
	if a.IsLead() || a.Status != state.AgentReady {
		item, err := s.chat.Send(a, text, images...)
		return told{item: item}, err
	}
	// Most messages go to a machine that runs: no lock for those.
	if inst, err := s.cfg.Incus.Instance(ctx, a.Instance); err == nil && inst.Status == "Running" {
		item, err := s.chat.Send(a, text, images...)
		return told{item: item}, err
	}
	// One wake at a time, so two messages to the same stopped agent don't
	// both start its machine.
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
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
		if err := s.startMachine(ctx, m, a); err != nil {
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
