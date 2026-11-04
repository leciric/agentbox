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
	msg := api.ChatMessageRequest{Text: req.Text, Images: req.Images, Files: req.Files}
	if _, err := s.tellAgentMessage(ctx, a, msg); err != nil {
		return api.ChatItem{}, err
	}
	s.countFeature(api.FeatureAgentTurn)
	return s.chat.Told(lead, a.Name, req.Text, len(req.Images)+len(req.Files))
}
