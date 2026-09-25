package chat

import (
	"context"
	"errors"
	"strings"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Compaction carries a conversation on in a fresh session (D73).
//
// A model's context is finite and a project's chat is not: it is one
// conversation the user never ends, and left alone it fills the window and
// either stops or is compacted by the tool itself, behind AgentBox's back and
// out of AgentBox's memory. Rolling the session over is the other half of the
// answer to that. What the conversation was about is consolidated into the
// project's memory first, a fresh session is started, and the recap goes into
// the brief that session reads (internal/agent/recap.go).
//
// Nothing here decides *when* that happens or what is written: the daemon does
// both (internal/daemon/rollover.go). This file is the mechanism — the hidden
// prompt that costs the old session its last words, and the roll itself.
//
// The conversation the user sees is untouched. chat_items stay exactly as they
// are, which is the whole point of not going through Clear: the app shows one
// unbroken chat, with a single notice where the session changed.

// RolloverNotice is the one visible mark a compaction leaves. It goes in
// through the same no-turn notice path a finish notice uses with
// finish_notices=off (D53): the history says what happened, and nothing is
// spent saying it.
const RolloverNotice = "Conversation continued in a fresh session; earlier context was consolidated into project memory."

// ErrBusy says a chat can't be rolled over right now: a turn is running, or a
// rollover already is. It is not a failure — the caller tries again the next
// time a turn is about to start.
var ErrBusy = errors.New("the chat is busy")

// errNoSession says there is nothing to roll over: the chat has no adapter, so
// its next turn starts a session anyway.
var errNoSession = errors.New("the chat has no running session")

// Context reports how full a chat's context is, as the AI tool last said in a
// usage_update.
//
// ready is false unless there is something to compact *now*: a conversation
// this daemon has loaded, with a session behind it, and no turn running. A
// chat whose adapter has stopped is deliberately not ready — the numbers it
// last reported describe a session nobody is talking to, and the turn that
// wakes it will resume that session and report again.
//
// used and size are 0 until an update arrives, and 0 again after a new session,
// a resume, a clear or a model switch. That is "unknown", never "empty": a
// caller that treats it as room to spare would compact nothing and a caller
// that treats it as full would compact everything, so the only safe reading is
// to wait for the tool to say.
func (m *Manager) Context(ref string) (used, size int64, ready bool) {
	c := m.existing(ref)
	if c == nil {
		return 0, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session.ContextUsed, c.session.ContextSize, c.adapter != nil && c.turn == nil
}

// Compact consolidates a chat's session and rolls it over.
//
// ask is the hidden prompt: it runs on the session that is about to go, and
// what it answers never enters the conversation (see capture in handler.Notify
// — the chat items the user sees gain nothing but the notice below). settle is
// then given that answer, off the conversation's lock, to write into the
// project's memory and to rewrite the brief the next session will read.
//
// The rollover happens whatever settle makes of the answer, and whatever the
// hidden prompt did: a session that couldn't summarise itself is exactly the
// session that most needs replacing, and a chat left in a context it has
// filled is stuck. settle is told what went wrong and decides what to write
// instead; its own error is returned, after the roll, for the caller to log.
//
// Notices that arrive while this runs wait for it, as they wait for a running
// turn, and are delivered into the fresh session once it is in place.
func (m *Manager) Compact(ctx context.Context, a state.Agent, ask string, settle func(answer string, askErr error) error) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	switch {
	case c.turn != nil, c.rolling:
		c.mu.Unlock()
		return ErrBusy
	case c.adapter == nil:
		c.mu.Unlock()
		return errNoSession
	}
	ad := c.adapter
	c.rolling, c.capture = true, &strings.Builder{}
	c.mu.Unlock()

	answer, askErr := c.consolidate(ctx, ad, ask, state.TokensCompaction)
	if askErr != nil {
		m.logf("chat %s: consolidating the conversation: %v", a.Ref(), askErr)
	}
	// Both errors, or neither: the roll and what was made of the answer fail
	// independently, and a caller that saw only one of them would log the
	// wrong thing.
	settleErr := settle(answer, askErr)
	return errors.Join(settleErr, m.Rollover(a))
}

// consolidate runs the hidden prompt on the session and returns what it said.
// It mirrors prompt, without a turn: no chat item heads it, nothing is
// published, and the text comes back through c.capture rather than through
// appendText. What it spent goes in the token ledger as kind.
func (c *conversation) consolidate(ctx context.Context, ad *adapter, ask, kind string) (string, error) {
	select {
	case <-ad.started:
	case <-ctx.Done():
		// An adapter that is still starting, with a caller that has waited as
		// long as it means to. The roll still happens: the session this would
		// have summarised is one nobody is talking to.
		return "", ctx.Err()
	}
	c.mu.Lock()
	switch {
	case ad.err != nil:
		err := ad.err
		c.mu.Unlock()
		return "", err
	case c.adapter != ad:
		c.mu.Unlock()
		return "", errStopped
	}
	sessionID := ad.sessionID
	c.mu.Unlock()

	var res acp.PromptResponse
	err := ad.conn.Call(ctx, acp.MethodSessionPrompt, acp.PromptRequest{
		SessionID: sessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: ask}},
	}, &res)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.book(ad, kind, newID(), &res)
	answer := ""
	if c.capture != nil {
		answer = c.capture.String()
	}
	if err != nil {
		return answer, err
	}
	if strings.TrimSpace(answer) == "" {
		return "", errors.New("the session said nothing")
	}
	return answer, nil
}

// Rollover ends the chat's session and leaves its conversation where it is.
//
// It is the second half of Clear: the adapter stops and the stored session id
// goes, so the next turn's connect calls session/new. What Clear also does and
// this deliberately doesn't is throw the conversation away — chat_items are
// untouched, so the user's chat carries on down the page with a notice where
// the session changed.
//
// The fresh session knows nothing of what was said. What it is given instead
// is its brief, which the caller has rewritten with a recap by the time this
// runs.
func (m *Manager) Rollover(a state.Agent) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	return c.rollover()
}

// rollover rolls the session, or says why it didn't. Either way the chat stops
// holding back what arrived while it ran. The conversation is locked.
func (c *conversation) rollover() error {
	// A turn that started while the consolidation ran — the user wrote — owns
	// the session now. Stopping its adapter would kill that turn mid-sentence,
	// so the rollover is abandoned and the next check makes it again; the
	// consolidation it already did is in the project's memory either way.
	if c.turn != nil {
		c.settleRolling()
		return ErrBusy
	}
	c.stopAdapter()
	c.capture = nil
	c.stored.SessionID = ""
	err := c.m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored)
	c.tools, c.plan, c.open, c.openMessage = map[string]*api.ChatItem{}, nil, nil, ""
	// Nothing of the old session's state describes the new one: its context is
	// unknown until it says, and its commands are its own.
	c.session.TurnStartedAt, c.session.ContextUsed, c.session.ContextSize = nil, 0, 0
	c.session.Commands = []api.ChatCommand{}
	c.session.State = c.stateNow()
	c.add("notice", c.lastTurn()).Text = RolloverNotice
	c.markSession()
	c.flush(true)
	c.settleRolling()
	return err
}

// settleRolling ends the window notices wait through, and delivers what
// waited. A notice held back here lands in the fresh session, which is where
// it is worth reading.
func (c *conversation) settleRolling() {
	c.rolling = false
	c.deliverQueued()
}

// deliverQueued hands over what arrived while the chat was busy, several as
// one: three agents finishing at once wake the chat once, not three times.
// The conversation is locked and nothing runs.
func (c *conversation) deliverQueued() {
	queued := c.queued
	if len(queued) == 0 {
		return
	}
	c.queued = nil
	texts := make([]string, 0, len(queued))
	// One of them wanting a turn is enough to start one. Hiding needs all of
	// them: the merged item carries every text, so one notice meant to be read
	// keeps the whole of it visible.
	merged := NoticeOptions{Hidden: true}
	for _, n := range queued {
		texts = append(texts, n.text)
		merged.Act = merged.Act || n.opts.Act
		merged.Hidden = merged.Hidden && n.opts.Hidden
	}
	c.deliver(strings.Join(texts, "\n\n---\n\n"), merged)
}
