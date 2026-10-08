package chat

import (
	"context"
	"errors"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// errNoTurn refuses a request for approval made outside a turn: the card
// would wait in a chat nobody is in.
var errNoTurn = errors.New("the chat has no turn running to ask the user in")

// Approve asks the user, in a's chat, whether AgentBox may do something one
// of its tools asked for, and waits for the answer: true when they approved.
// It is the card the AI tool's own permission requests make (Request), and
// the app answers it on the same route; the turn waits on it the same way,
// and a cancelled turn or ctx refuses it.
func (m *Manager) Approve(ctx context.Context, a state.Agent, req api.ChatPermission) (bool, error) {
	c, err := m.conversation(a)
	if err != nil {
		return false, err
	}
	if c.turn == nil {
		c.mu.Unlock()
		return false, errNoTurn
	}
	c.progressed()
	c.closeOpen()
	it := c.add("permission", c.turn.id)
	req.Outcome = ""
	req.Options = []api.ChatPermissionOption{
		{ID: api.RefuseOption, Name: "Refuse", Kind: "reject_once"},
		{ID: api.ApproveOption, Name: "Approve", Kind: "allow_once"},
	}
	it.Permission = &req
	answer := make(chan string, 1)
	c.pending[it.ID] = func(res any, _ error) {
		out, _ := res.(acp.RequestPermissionResponse)
		answer <- out.Outcome.OptionID
	}
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	c.mu.Unlock()

	select {
	case option := <-answer:
		return option == api.ApproveOption, nil
	case <-ctx.Done():
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, waiting := c.pending[it.ID]; !waiting {
			// Answered as it gave up: the answer stands.
			return <-answer == api.ApproveOption, nil
		}
		delete(c.pending, it.ID)
		it.Permission.Outcome = "cancelled"
		c.touch(it)
		c.session.State = c.stateNow()
		c.markSession()
		c.flush(true)
		return false, ctx.Err()
	}
}
