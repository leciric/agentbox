package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// A shared project whose owner can't be reached still takes what is written
// in it (internal/daemon/shareoutbox.go): a message in its chat, a prompt to
// one of its agents, an answer to an agent's question. This AgentBox keeps
// each, shows it as waiting, and sends them in order once the owner is back.
// What the owner refuses then (the project unshared, the agent gone) stays
// here, with why, until it is cancelled.

// EventShareOutbox is published with the whole outbox, a []ShareQueued, when
// anything in it changes.
const EventShareOutbox = "share-outbox"

// ChatAsideAway is the delivery of the chat item a message to a shared
// project answers with when its owner can't be reached: it was kept, as the
// ShareQueued its id names ("outbox-<id>"), and isn't in the chat yet.
const ChatAsideAway = "away"

// ShareQueuedPrefix begins the id of a chat item that stands for a
// ShareQueued: ChatAsideAway's.
const ShareQueuedPrefix = "outbox-"

// ShareQueued is something written in a shared project while its owner
// couldn't be reached, waiting here to be sent.
type ShareQueued struct {
	ID int64 `json:"id"`
	// Remote is the owner's id here (ShareRemote.ID), and Owner its name.
	Remote string `json:"remote"`
	Owner  string `json:"owner"`
	// Kind is ShareDidMessage, ShareDidPrompt or ShareDidAnswer.
	Kind string `json:"kind"`
	// Project is the project as it is known here ("<remote>~<project>").
	Project string `json:"project"`
	// Chat is the chat it shows in, as its ref here: a session of the
	// project's chat ("r1~web/lead.s2") or an agent's ("r1~web/agent-01").
	// "" for an answer, whose Question names what it answers.
	Chat     string `json:"chat,omitempty"`
	Question string `json:"question,omitempty"`
	// To is the agent a message in the project's chat goes to instead of its
	// lead (ChatMessageRequest.To).
	To string `json:"to,omitempty"`
	// Text is what it says, and Attachments how many pictures and files go
	// with it.
	Text        string    `json:"text"`
	Attachments int       `json:"attachments,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// Error is why the owner refused it once it was back: it won't be sent,
	// and stays to be read until it is cancelled.
	Error string `json:"error,omitempty"`
}

// ShareOutbox lists what waits to be sent to the AgentBoxes that share
// projects with this one, oldest first.
func (c *Client) ShareOutbox(ctx context.Context) ([]ShareQueued, error) {
	var out []ShareQueued
	return out, c.do(ctx, http.MethodGet, "/v1/share/outbox", nil, &out)
}

// CancelShareQueued takes back something that waits to be sent, or forgets
// one its owner refused.
func (c *Client) CancelShareQueued(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, "/v1/share/outbox/"+strconv.FormatInt(id, 10), nil, nil)
}
