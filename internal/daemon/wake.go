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
	return s.tellAgentMessage(ctx, a, api.ChatMessageRequest{Text: text, Images: images})
}

// tellAgentMessage is tellAgent for a message with files as well as pictures.
func (s *Server) tellAgentMessage(ctx context.Context, a state.Agent, msg api.ChatMessageRequest) (told, error) {
	s.wakeIdle(a.Ref())
	if a.IsLead() || a.Status != state.AgentReady {
		item, err := s.chat.SendMessage(a, msg)
		return told{item: item}, err
	}
	// Most messages go to a machine that runs: no lock for those.
	if inst, err := s.cfg.Incus.Instance(ctx, a.Instance); err == nil && inst.Status == "Running" {
		item, err := s.chat.SendMessage(a, msg)
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
		item, err := s.chat.SendMessage(a, msg)
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
		if err := s.startMachine(ctx, m, a, "a message: "+headWords(msg.Text, 60)); err != nil {
			return told{}, err
		}
		woke = "started"
	}
	if woke != "" {
		s.refreshAgents(ctx)
	}
	item, err := s.chat.SendMessage(a, msg)
	return told{item: item, woke: woke}, err
}

// startMachine starts a stopped agent's machine, with its in-agent API: what
// `agentbox start` does.
// why goes to the log: a machine that someone retired and that runs again
// should leave a line saying who started it.
func (s *Server) startMachine(ctx context.Context, m *agent.Manager, a state.Agent, why string) error {
	s.logf("starting %s's machine for %s", a.Ref(), why)
	s.markRetired(a.Ref(), false)
	s.startedIdle(a.Ref())
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

// markRetired remembers that a retire stopped or paused an agent, so that
// automatic messages (an answer, a memory notice) don't start its machine
// again. Anything that starts the machine clears it (startMachine).
func (s *Server) markRetired(ref string, retired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if retired {
		if s.retired == nil {
			s.retired = map[string]bool{}
		}
		s.retired[ref] = true
		return
	}
	delete(s.retired, ref)
}

// tellUnlessRetired is tellAgent for messages nobody asked for: an agent
// retired with how=stop stays stopped rather than being started to hear them.
func (s *Server) tellUnlessRetired(ctx context.Context, a state.Agent, text string) (told, error) {
	s.mu.Lock()
	retired := s.retired[a.Ref()]
	s.mu.Unlock()
	if retired {
		if inst, err := s.cfg.Incus.Instance(ctx, a.Instance); err == nil && inst.Status != "Running" {
			s.logf("%s was retired and its machine is %s: not starting it for %s", a.Ref(), inst.Status, headWords(text, 60))
			return told{}, nil
		}
	}
	return s.tellAgent(ctx, a, text)
}
