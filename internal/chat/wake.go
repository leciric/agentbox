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
	text = strings.TrimSpace(text)
	if text == "" && len(images) == 0 {
		return api.ChatItem{}, errors.New("the message is empty")
	}
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatItem{}, err
	}
	defer c.mu.Unlock()
	saved, err := c.saveImages(images)
	if err != nil {
		return api.ChatItem{}, err
	}
	switch {
	case c.compaction != nil:
		return clone(*c.hold(text, saved)), nil
	case c.turn != nil:
		return clone(*c.aside(text, saved)), nil
	}
	it := c.add("aside", c.lastTurn())
	it.Text, it.Images, it.Delivery = text, saved, api.ChatAsideWaking
	c.outbox = append(c.outbox, &outgoing{item: it.ID, text: text, images: saved})
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
	if c.turn != nil || c.compaction != nil || len(c.outbox) == 0 {
		return nil // what runs hands the outbox over when it ends
	}
	c.sendOutbox()
	return nil
}
