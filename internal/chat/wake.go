package chat

import (
	"errors"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Hold takes a message for an agent whose machine is stopped and has to wait
// for the VM's memory before it can start. Sent now, it would start the AI
// tool in a machine that isn't running, and fail. It goes into the
// conversation at once, as an aside marked waking, and waits in the outbox
// until Release, which the daemon calls once the machine runs. Stop gives up
// on it, saying so.
//
// A conversation that is busy after all — a turn running, a compaction —
// takes the message the way Send does.
func (m *Manager) Hold(a state.Agent, text string, images ...api.ChatImageUpload) (api.ChatItem, error) {
	return m.HoldMessage(a, api.ChatMessageRequest{Text: text, Images: images})
}

// HoldMessage is Hold for a message with files as well as pictures.
func (m *Manager) HoldMessage(a state.Agent, msg api.ChatMessageRequest) (api.ChatItem, error) {
	text := strings.TrimSpace(msg.Text)
	if text == "" && len(msg.Images) == 0 && len(msg.Files) == 0 {
		return api.ChatItem{}, errors.New("the message is empty")
	}
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatItem{}, err
	}
	defer c.mu.Unlock()
	sent, err := c.saveAttached(msg)
	if err != nil {
		return api.ChatItem{}, err
	}
	switch {
	case c.compaction != nil:
		return clone(*c.hold(text, sent)), nil
	case c.turn != nil:
		return clone(*c.aside(text, sent)), nil
	}
	it := c.add("aside", c.lastTurn())
	it.Text, it.Images, it.Files, it.Delivery = text, sent.images, sent.files, api.ChatAsideWaking
	c.outbox = append(c.outbox, &outgoing{item: it.ID, text: text, sent: sent})
	c.flush(true)
	return clone(*it), nil
}

// Release hands what Hold kept to the AI tool, as one turn, now that the
// agent's machine runs. It does nothing when nothing waits.
func (m *Manager) Release(a state.Agent) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	if c.turn == nil && c.compaction == nil && c.releaseResume() {
		return nil // the turn it starts hands the outbox over when it ends
	}
	if c.turn != nil || c.compaction != nil || len(c.outbox) == 0 {
		return nil // what runs hands the outbox over when it ends
	}
	c.sendOutbox()
	return nil
}
