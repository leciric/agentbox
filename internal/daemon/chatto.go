package daemon

import (
	"context"
	"errors"
	"fmt"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A message written in a project's chat to one of its agents (the composer's
// To: chip, `agentbox chat --to`): it goes to the agent's chat by the same way
// as the agent's own chat and tell_agent, and the project's chat keeps a
// notice of it, which the lead reads at its next turn without one being
// started for it (chat.Manager.Told). A lead that is kept in sync this way
// spends nothing until somebody writes to it.

// errOtherSession refuses a message to another session's agent until the
// user says to send it anyway (api.ErrorOtherSession).
type errOtherSession struct{ agent, topic string }

func (e errOtherSession) Error() string {
	return fmt.Sprintf("%s belongs to the %q session: send anyway?", e.agent, e.topic)
}

// sendToAgent sends a message written in lead's chat to the agent it names,
// and gives back the notice the chat keeps of it.
func (s *Server) sendToAgent(ctx context.Context, lead state.Agent, req api.ChatMessageRequest) (api.ChatItem, error) {
	if lead.Project == state.HomeProject {
		return api.ChatItem{}, errors.New("only a project's chat sends to its agents: open that project's chat")
	}
	a, err := s.store.Agent(ctx, lead.Project, req.To)
	if err != nil {
		return api.ChatItem{}, err
	}
	if a.IsLead() {
		return api.ChatItem{}, errNotAnAgent
	}
	if !req.AnySession {
		if topic, other := s.otherSession(ctx, lead.Session(), a); other {
			return api.ChatItem{}, errOtherSession{agent: a.Name, topic: topic}
		}
	}
	msg := api.ChatMessageRequest{Text: req.Text, Images: req.Images, Files: req.Files}
	if _, err := s.tellAgentMessage(ctx, a, msg); err != nil {
		return api.ChatItem{}, err
	}
	s.countFeature(api.FeatureAgentTurn)
	return s.chat.Told(lead, a.Name, req.Text, len(req.Images)+len(req.Files))
}

// otherSession reports whether a belongs to a session of its project's chat
// other than from, one that still exists, and that session's topic. An agent
// nobody owns, or whose session was deleted, is nobody's.
func (s *Server) otherSession(ctx context.Context, from string, a state.Agent) (string, bool) {
	owner := a.Session()
	if from == "" || owner == "" || owner == from {
		return "", false
	}
	ls, err := s.store.LeadSession(ctx, a.Project, owner)
	if errors.Is(err, state.ErrNotFound) {
		return "", false
	}
	if err != nil {
		return owner, true
	}
	return ls.Topic, true
}
