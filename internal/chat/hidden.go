package chat

import (
	"context"
	"strings"

	"agentbox/internal/state"
)

// A hidden prompt without a rollover (D76).
//
// Compact (rollover.go) asks the session a question nobody sees and then
// throws the session away. Consolidating a project's raw events into memories
// wants the first half of that and not the second: there is no other model
// session lying around, the lead's is already warm and already knows the
// project, and nothing about summarising a stretch of history says the
// conversation has to end.
//
// So this is Compact's machinery with the roll taken off. Everything else is
// the same, deliberately: the prompt goes through the ACP connection rather
// than through a turn, the answer is intercepted by c.capture before the
// conversation collects it, and the chat is marked busy for the duration so
// notices wait rather than landing in the middle of it.

// Ask runs a hidden prompt on a chat's existing session and answers with what
// it said. It leaves no chat item, publishes no event, starts no turn and
// keeps the session.
//
// It answers ErrBusy when a turn is running or the chat is already being
// asked something, and says so rather than queueing: whatever wanted this can
// ask again, and a prompt held behind a turn would run against a session that
// has moved on since. A chat with no adapter has nothing to ask.
//
// The same race Compact accepts is accepted here, and for the same reason: a
// turn that starts while the prompt is in flight — the user wrote — owns the
// session from then on, and what comes back is partial. The caller is
// expected to make nothing of an answer it can't parse, which for a
// consolidation means the watermark doesn't move and the window is read again
// next time.
func (m *Manager) Ask(ctx context.Context, a state.Agent, ask string) (string, error) {
	c, err := m.conversation(a)
	if err != nil {
		return "", err
	}
	switch {
	case c.turn != nil, c.rolling:
		c.mu.Unlock()
		return "", ErrBusy
	case c.adapter == nil:
		c.mu.Unlock()
		return "", errNoSession
	}
	ad := c.adapter
	c.rolling, c.capture = true, &strings.Builder{}
	c.mu.Unlock()

	answer, askErr := c.consolidate(ctx, ad, ask, state.TokensConsolidation)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.capture = nil
	// Whatever happened, the chat stops holding things back, and what waited
	// is delivered into the session it was always going to land in.
	c.settleRolling()
	return answer, askErr
}
