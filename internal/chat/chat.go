// Package chat runs the conversations of the app's Chat tab, one per agent. The
// agent's AI tool (Claude Code, Codex or OpenCode) runs inside the agent behind an ACP
// adapter, which the daemon speaks to over the adapter's stdin and stdout.
// What the tool reports becomes a list of items (messages, tool calls, plans,
// permission requests); every change is stored and published as a numbered event.
package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

var errStopped = errors.New("the chat was stopped")

const (
	// flushDelay batches streamed text: changes go out this long after the first one.
	flushDelay   = 50 * time.Millisecond
	saveInterval = time.Second
	// startTimeout covers starting the adapter and its session; installing the
	// adapter in an older agent has its own limit.
	startTimeout = 2 * time.Minute
	// cancelTimeout is how long a cancelled turn waits for the tool to stop
	// before its adapter is stopped instead.
	cancelTimeout = 20 * time.Second
	// steerGrace is how long a turn that has just been given a message waits
	// before it's given another, so messages sent within moments of each other
	// go in as one. The first message of a turn never waits: a correction worth
	// changing course for shouldn't arrive late. See drainOutbox.
	steerGrace = 3 * time.Second

	maxOutput   = 8 << 10  // the end of a tool call's output that's kept
	maxDiffText = 64 << 10 // each side of a diff
	maxThought  = 64 << 10
)

// ToolNames are the AI tools the chat can drive.
var ToolNames = map[string]string{"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode"}

// Launcher starts an agent's ACP adapter. status says what it's doing while it
// prepares, like installing the adapter.
type Launcher func(ctx context.Context, a state.Agent, status func(detail string)) (*Process, error)

// ModelPreparer writes the model a chat should start on into its AI tool's own
// configuration, before its adapter is launched. Claude Code resolves a model
// at launch against a wider catalogue than the menu it validates mid-session
// against, so this is what lets a session begin on a model that menu doesn't
// list (D46); mid-session switching still goes through set_config_option.
// model is the stored preference, "" for none, and compactWindow the context
// the chat compacts at, 0 for the model's whole window (D83, D91). Optional:
// with none set, a chat starts exactly as it did before, on whatever its tool
// defaults to.
type ModelPreparer func(ctx context.Context, a state.Agent, model string, compactWindow int64) error

// Manager holds every agent's conversation.
type Manager struct {
	Store   *state.Store
	Launch  Launcher
	Prepare ModelPreparer // may be nil
	Publish func(api.ChatEvent)
	// Finished, when set, is called after an agent's turn ends, with how it
	// ended. The daemon uses it to tell the project's chat, and to tell a
	// genuine finish (the model stopping on its own) from a turn merely cut
	// short by a limit or a cancellation.
	Finished func(a state.Agent, result api.ChatTurnResult)
	// Idle, when set, is called after a turn ends with nothing following it:
	// no notice or message that waited started another. The daemon compacts a
	// lead's full chat then (D73), while nobody is waiting on it. Off the
	// conversation's lock, in a goroutine of its own.
	Idle func(a state.Agent)
	// AuthFailed, when set, is called when a turn failed because the agent's
	// AI tool was refused by its provider: an expired or revoked login. The
	// daemon marks the account rejected, so a dead token is named where it is
	// stored rather than only inside the one agent that hit it.
	AuthFailed func(a state.Agent, detail string)
	// Limits, when set, is given every reading of the account's usage limits
	// a chat's AI tool relays (D85). Off the conversation's lock, in a
	// goroutine of its own.
	Limits  func(a state.Agent, reading acp.RateLimit)
	Logf    func(format string, args ...any) // may be nil
	Version string                           // AgentBox's, told to adapters
	// Now and After are the clock a chat waits on: what time it is, and how
	// work is scheduled for later. Both are nil outside tests, where the real
	// clock is used; a test injects one so a wait of hours takes none.
	Now   func() time.Time
	After func(d time.Duration, f func()) Timer
	// ImageDir is the directory that keeps the pictures sent in an agent's
	// chat (images.go). With none, a message can't carry any.
	ImageDir func(project, agent string) string

	mu    sync.Mutex
	convs map[string]*conversation
	// adapters are the goroutines that launch and then read each adapter.
	adapters sync.WaitGroup
}

// Timer is a wake-up that can be called off before it fires: what
// time.AfterFunc returns, and what a test's clock returns in its place.
type Timer interface{ Stop() bool }

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) after(d time.Duration, f func()) Timer {
	if m.After != nil {
		return m.After(d, f)
	}
	return time.AfterFunc(d, f)
}

// resumeAfterLimit reads the installation's "Resume after a usage limit"
// setting, which is on until somebody turns it off. It is read afresh every
// time rather than remembered, so turning it off also stops the waits already
// running. A store that can't be read leaves the setting at its default: the
// feature failing quietly off would be the harder thing to notice.
func (m *Manager) resumeAfterLimit() bool {
	on, err := m.Store.FlagOn(context.Background(), state.SettingResumeAfterLimit)
	if err != nil {
		m.logf("chat: reading %s: %v", state.SettingResumeAfterLimit, err)
		return true
	}
	return on
}

// conversation locks and returns an agent's conversation, loaded from the store.
// The caller unlocks it.
func (m *Manager) conversation(a state.Agent) (*conversation, error) {
	if _, ok := ToolNames[a.AI]; !ok {
		return nil, fmt.Errorf("%s runs no AI tool to chat with", a.Ref())
	}
	m.mu.Lock()
	if m.convs == nil {
		m.convs = map[string]*conversation{}
	}
	c := m.convs[a.Ref()]
	if c == nil {
		c = &conversation{
			m: m, agent: a,
			byID: map[string]*api.ChatItem{}, dirty: map[string]bool{}, unsaved: map[string]bool{},
			tools: map[string]*api.ChatItem{}, pending: map[string]func(any, error){},
			session: api.ChatSession{State: api.ChatOff, Tool: a.AI, Options: []api.ChatOption{}, Commands: []api.ChatCommand{}},
		}
		m.convs[a.Ref()] = c
	}
	m.mu.Unlock()
	c.mu.Lock()
	// Take the caller's record, not the one the conversation was made with. A
	// project's chat is first read before its lead exists, with no worktree yet,
	// and the first message is what creates it; an agent can also change its
	// Claude Code account between turns.
	c.agent = a
	if err := c.load(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	return c, nil
}

// RenameClaudeAccount follows a Claude Code account to its new name in every
// conversation held, the running sessions' own accounts included. The token
// is the same, so a session carries on untouched; this is only so that what
// it reports next — a usage-limit reading, a refused token — is filed under
// the name the account has now, not one that no longer exists.
func (m *Manager) RenameClaudeAccount(old, name string) {
	m.mu.Lock()
	convs := make([]*conversation, 0, len(m.convs))
	for _, c := range m.convs {
		convs = append(convs, c)
	}
	m.mu.Unlock()
	for _, c := range convs {
		c.mu.Lock()
		if c.agent.ClaudeAccount == old {
			c.agent.ClaudeAccount = name
		}
		if c.adapter != nil && c.adapter.account == old {
			c.adapter.account = name
		}
		c.mu.Unlock()
	}
}

func (m *Manager) existing(ref string) *conversation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.convs[ref]
}

// Thread returns an agent's conversation, with the number of the last event it includes.
func (m *Manager) Thread(a state.Agent) (api.ChatThread, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatThread{}, err
	}
	defer c.mu.Unlock()
	c.flush(false)
	items := make([]api.ChatItem, 0, len(c.items))
	for _, it := range c.items {
		items = append(items, clone(*it))
	}
	return api.ChatThread{Agent: a.Ref(), Seq: c.seq, Session: clone(c.session), Items: items}, nil
}

// LastMessage returns the text of the last thing an agent's AI tool said: the
// final assistant message of its most recent turn, which is usually how the
// tool summed up the work. The daemon reads it when the agent finishes, so the
// project's chat learns what happened without replaying the conversation.
//
// It answers with "" when that turn said nothing — the tool stopped after a
// tool call, or the conversation is empty.
func (m *Manager) LastMessage(a state.Agent) string {
	c, err := m.conversation(a)
	if err != nil {
		return ""
	}
	defer c.mu.Unlock()
	for i := len(c.items) - 1; i >= 0; i-- {
		it := c.items[i]
		if it.Kind == "user" {
			return "" // back at the start of the turn, with nothing said in it
		}
		// A subagent's words are its report to the agent, not the agent's
		// summary of the turn.
		if it.Kind == "assistant" && it.Parent == "" {
			if text := strings.TrimSpace(it.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

// Start starts the agent's AI tool behind its adapter, unless it runs. It
// returns at once; the session's state follows as events.
func (m *Manager) Start(a state.Agent) (api.ChatSession, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatSession{}, err
	}
	defer c.mu.Unlock()
	// Starting the chat again is you saying you have dealt with whatever
	// stopped it — a spent usage limit included, which is the one the session
	// keeps a record of. The record goes with the banner that offered this.
	c.clearLimit()
	c.startAdapter()
	c.flush(false)
	return clone(c.session), nil
}

// Send adds your message and starts a turn, starting the AI tool first if needed.
//
// A message sent while a turn is already running isn't refused. It joins that
// turn, so the model reads it in the middle of its work and decides for itself
// what it's worth: changing course now, or carrying on and handling it once
// what it's doing is done. That judgement is the model's, not a queue's — see
// aside for what happens when the tool can't take it.
//
// images are pictures sent with the message, which may then have no text.
func (m *Manager) Send(a state.Agent, text string, images ...api.ChatImageUpload) (api.ChatItem, error) {
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
	if c.compaction != nil {
		return clone(*c.hold(text, saved)), nil
	}
	if c.turn != nil {
		return clone(*c.aside(text, saved)), nil
	}
	return clone(*c.startTurn(text, saved)), nil
}

// Notice puts something in front of a conversation that the user didn't type:
// one of the project's agents finished, or asked a question.
//
// With act, it starts a turn, so the chat reacts on its own. Without, the
// notice waits in the conversation and the chat reads it the next time the user
// writes. A notice that arrives mid-turn is queued and delivered when the turn
// ends, so nothing is dropped and no turn is interrupted.
//
// Unlike a message somebody sent (see Send), a notice never joins the running
// turn. Notices are the automatic kind — an agent finished, an agent asked
// something — and a lead that reacts to one by messaging that agent can be told
// again by the turn its own message causes. Landing those mid-turn would tighten
// that circle into a loop; waiting for the turn to end leaves the lead a moment
// to settle, and several notices still become one.
func (m *Manager) Notice(a state.Agent, text string, opts NoticeOptions) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("the notice is empty")
	}
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	if c.turn != nil || c.rolling {
		// Busy: hold it until the running turn, or the rollover, ends. A
		// notice delivered mid-rollover would start a turn on the session
		// that is being replaced, and be lost with it.
		c.queued = append(c.queued, queuedNotice{text: text, opts: opts})
		return nil
	}
	c.deliver(text, opts)
	return nil
}

// NoticeOptions is how a notice is delivered.
type NoticeOptions struct {
	// Act starts a turn on the notice, so the conversation reacts to it now.
	// Without it the notice waits, and is read the next time somebody writes.
	Act bool
	// Hidden keeps the notice, and the turn it starts, out of what the app
	// shows: the caller is showing the same thing in a shape of its own, and
	// this text is only here because the model has to read something. See
	// api.ChatItem.Hidden.
	Hidden bool
}

// deliver adds a notice and, when asked, starts the turn that reacts to it.
// The conversation is locked.
func (c *conversation) deliver(text string, opts NoticeOptions) {
	it := c.add("notice", c.lastTurn())
	it.Text, it.Hidden = text, opts.Hidden
	if !opts.Act {
		c.flush(true)
		return
	}
	// The turn is the notice: the chat is answering what happened, not the user.
	// It carries the notice's text, so it is hidden for the same reason.
	user := c.add("user", "")
	user.Turn, user.Text, user.Hidden = user.ID, text, opts.Hidden
	c.beginTurn(user, text, nil)
}

// startTurn adds a message of yours and starts its turn. The conversation is
// locked and no turn is running.
func (c *conversation) startTurn(text string, images []api.ChatImage) *api.ChatItem {
	it := c.add("user", "")
	it.Turn, it.Text, it.Images = it.ID, text, images
	c.beginTurn(it, text, images)
	return it
}

// beginTurn starts the turn that it heads, with text and images as its prompt.
// The conversation is locked and no turn is running.
func (c *conversation) beginTurn(it *api.ChatItem, text string, images []api.ChatImage) {
	// Whatever the turn was waiting for, it is running now: a pending resume
	// has been overtaken, and the limit isn't what the session is doing.
	c.endLimit()
	t := &turn{id: it.ID, text: text, images: images}
	c.turn = t
	c.tools, c.plan, c.open, c.openMessage = map[string]*api.ChatItem{}, nil, nil, ""
	started := it.CreatedAt
	c.session.TurnStartedAt = &started
	if c.windowRestart && !c.rolling {
		// The context window changed since the adapter started, and it reads
		// that only at launch: a new adapter resumes the same session on it.
		c.stopAdapter()
		c.windowRestart = false
	}
	ad := c.startAdapter()
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	go c.prompt(ad, t)
}

// aside takes a message that arrived while a turn was running. It goes into the
// conversation straight away, inside the turn it interrupted, and joins the
// outbox for drain to hand over. The conversation is locked and a turn runs.
func (c *conversation) aside(text string, images []api.ChatImage) *api.ChatItem {
	it := c.add("aside", c.turn.id)
	it.Text, it.Images, it.Delivery = text, images, api.ChatAsideWaiting
	c.outbox = append(c.outbox, &outgoing{item: it.ID, text: text, images: images, turn: c.turn})
	c.flush(true)
	c.drain()
	return it
}

// midTurn frames messages for a model that is in the middle of something, so the
// judgement the feature promises is one the model can actually make: it has to
// know the work isn't finished, and that acting now is its call rather than an
// instruction.
func midTurn(texts []string) string {
	what := "This message"
	if len(texts) > 1 {
		what = fmt.Sprintf("These %d messages", len(texts))
	}
	return "[AgentBox: " + what + " arrived while you were working, so the task you were" +
		" already given isn't finished. Decide what they're worth: if they change what you" +
		" should be doing, change course now; if they don't, carry on with what you were" +
		" doing and handle them once that's done. You don't need to acknowledge this note.]\n\n" +
		strings.Join(texts, "\n\n")
}

// drain starts handing the outbox over, unless that's already happening. One
// goroutine does it at a time and always takes the oldest message first, so
// nothing a sender wrote later reaches the tool earlier. The conversation is
// locked.
func (c *conversation) drain() {
	if c.draining || len(c.outbox) == 0 {
		return
	}
	c.draining = true
	go c.drainOutbox()
}

// drainOutbox gives each waiting message to the AI tool: into the running turn
// while one runs and the adapter takes them, otherwise as a turn of its own once
// that turn ends. It holds the conversation except while it waits on the
// adapter, which it must not do under the lock — the adapter's own replies come
// in on a goroutine that needs it.
func (c *conversation) drainOutbox() {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { c.draining = false }()
	for len(c.outbox) > 0 {
		out := c.outbox[0]
		t, ad := c.turn, c.adapter
		switch {
		case t == nil:
			// Nothing is running: everything waiting becomes one turn of its own.
			c.sendOutbox()
			return
		case ad != nil && !ad.ready && ad.err == nil:
			// The tool is still starting. Wait for it rather than give up on a
			// turn that hasn't really begun.
			started := ad.started
			c.mu.Unlock()
			<-started
			c.mu.Lock()
			continue
		case t != out.turn, t.cancelled, t.deferred, ad == nil, !ad.ready, !ad.steering:
			// This one can't join the turn that's running, so none of them may:
			// they wait together and go in after it. finishTurn drains again.
			if t == out.turn {
				t.deferred = true
			}
			c.deferOutbox()
			return
		}
		// Each message pre-empts the generation, and an adapter given several in
		// quick succession takes minutes to settle the turn afterwards. So a turn
		// that was just given one waits a moment before it's given another, and
		// everything that gathers in the meantime goes in as a single message.
		// The first never waits: an urgent correction arriving late is the thing
		// this feature exists to avoid.
		if wait := steerGrace - time.Since(t.steeredAt); !t.steeredAt.IsZero() && wait > 0 {
			c.mu.Unlock()
			time.Sleep(wait)
			c.mu.Lock()
			continue
		}
		batch := make([]*outgoing, 0, len(c.outbox))
		texts := make([]string, 0, len(c.outbox))
		var images []api.ChatImage
		for _, o := range c.outbox {
			if o.turn != t {
				break
			}
			batch = append(batch, o)
			texts = append(texts, o.text)
			images = append(images, o.images...)
		}
		sessionID, dir := ad.sessionID, c.m.imageDir(c.agent)
		if len(images) > 0 && !ad.images {
			// Only possible for pictures taken while the adapter was still
			// starting, before it said it can't read them.
			texts = append(texts, fmt.Sprintf("[AgentBox: %d image(s) were sent too, which this tool can't read.]", len(images)))
			images = nil
		}
		c.mu.Unlock()
		var res acp.SteerResponse
		err := ad.conn.Call(context.Background(), acp.MethodSessionSteer, acp.SteerRequest{
			SessionID: sessionID,
			Prompt:    c.promptBlocks(dir, midTurn(texts), images),
			// Should the session have gone idle underneath us, the message comes
			// back rather than becoming a turn of the adapter's own: a turn
			// AgentBox never asked for is one it doesn't follow or end.
			Meta: &acp.SteerMeta{Steering: acp.SteerOptions{IdleBehavior: acp.SteerPromptRequired}},
		}, &res)
		c.mu.Lock()
		if len(c.outbox) == 0 || c.outbox[0] != out {
			return // cleared, or given up on, while the adapter had it
		}
		if err != nil || res.Outcome == acp.SteerPromptRequired {
			if err != nil {
				c.m.logf("chat %s: steering: %v", c.agent.Ref(), err)
			}
			if t == c.turn {
				t.deferred = true
			}
			c.deferOutbox()
			return
		}
		// Injected, or (from an adapter that didn't know to hand it back) a turn
		// of its own. Either way the tool has them.
		c.outbox = c.outbox[len(batch):]
		t.steeredAt = time.Now()
		for _, o := range batch {
			c.setDelivery(o.item, api.ChatAsideSent)
		}
		c.flush(true)
	}
}

// sendOutbox sends everything waiting as one turn of its own. The messages are
// already in the conversation, from when they were taken, so the oldest becomes
// the turn's own message where it stands and the rest join it: the turn is one
// prompt, and no text is repeated. The conversation is locked and no turn runs.
func (c *conversation) sendOutbox() {
	out := c.outbox
	c.outbox = nil
	texts := make([]string, 0, len(out))
	var images []api.ChatImage
	var head *api.ChatItem
	for _, o := range out {
		it := c.byID[o.item]
		if it == nil {
			continue
		}
		if o.text != "" {
			texts = append(texts, o.text)
		}
		images = append(images, o.images...)
		it.Delivery = ""
		if head == nil {
			head, it.Kind, it.Turn = it, "user", it.ID
		} else {
			it.Turn = head.ID
		}
		c.touch(it)
	}
	if head == nil {
		c.flush(true)
		return
	}
	c.beginTurn(head, strings.Join(texts, "\n\n"), images)
}

// deferOutbox says of everything waiting that it goes in after the running turn.
// The conversation is locked.
func (c *conversation) deferOutbox() {
	for _, o := range c.outbox {
		c.setDelivery(o.item, api.ChatAsideDeferred)
	}
	c.flush(true)
}

// loseOutbox gives up on everything waiting, for a session that is ending: the
// messages were taken and can't be delivered now. It says so where the sender
// will see it, because a message that was accepted and then quietly dropped is
// worse than one that was refused. The conversation is locked.
func (c *conversation) loseOutbox(why string) {
	out := c.outbox
	c.outbox = nil
	if len(out) == 0 {
		return
	}
	for _, o := range out {
		c.setDelivery(o.item, api.ChatAsideLost)
	}
	what := "The message above never reached"
	if len(out) > 1 {
		what = fmt.Sprintf("The %d messages above never reached", len(out))
	}
	c.add("error", c.lastTurn()).Text = fmt.Sprintf("%s %s: %s", what, ToolNames[c.agent.AI], why)
	c.flush(true)
}

func (c *conversation) setDelivery(item, delivery string) {
	if it := c.byID[item]; it != nil && it.Kind == "aside" && it.Delivery != delivery {
		it.Delivery = delivery
		c.touch(it)
	}
}

// Cancel stops the running turn, if there is one.
func (m *Manager) Cancel(a state.Agent) (api.ChatSession, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatSession{}, err
	}
	defer c.mu.Unlock()
	// Stopping a chat that is waiting out a usage limit is how you say you
	// don't want it carried on, and there is no running turn to cancel then.
	// The limit itself stays on the session: it is still why the turn stopped,
	// and the chat still can't do anything until it resets.
	c.cancelResume()
	c.resumeTry = 0
	t := c.turn
	if t == nil || t.cancelled {
		c.flush(true)
		return clone(c.session), nil
	}
	t.cancelled = true
	c.cancelPending()
	ad := c.adapter
	if ad != nil && ad.ready {
		// The turn ends when the tool answers the prompt, as cancelled.
		if err := ad.conn.Notify(acp.MethodSessionCancel, acp.CancelNotification{SessionID: ad.sessionID}); err != nil {
			c.finishTurn(t, nil, err)
		} else {
			time.AfterFunc(cancelTimeout, func() {
				c.mu.Lock()
				defer c.mu.Unlock()
				if c.turn == t && c.adapter == ad {
					c.finishTurn(t, nil, fmt.Errorf("%s didn't stop within %s, so its session was stopped", ToolNames[c.agent.AI], cancelTimeout))
					c.stopAdapter()
					c.flush(true)
				}
			})
		}
	} else {
		c.finishTurn(t, &acp.PromptResponse{StopReason: "cancelled"}, nil)
	}
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	return clone(c.session), nil
}

// Answer answers a permission request with one of its options; an empty option
// cancels it.
func (m *Manager) Answer(a state.Agent, itemID, optionID string) (api.ChatItem, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatItem{}, err
	}
	defer c.mu.Unlock()
	it := c.byID[itemID]
	if it == nil || it.Permission == nil {
		return api.ChatItem{}, fmt.Errorf("no permission request %q: %w", itemID, state.ErrNotFound)
	}
	reply := c.pending[itemID]
	if reply == nil {
		return api.ChatItem{}, errors.New("that request isn't waiting for an answer anymore")
	}
	if optionID != "" && !slices.ContainsFunc(it.Permission.Options, func(o api.ChatPermissionOption) bool { return o.ID == optionID }) {
		return api.ChatItem{}, fmt.Errorf("the request has no option %q", optionID)
	}
	out := acp.RequestPermissionResponse{Outcome: acp.PermissionOutcome{Outcome: "cancelled"}}
	it.Permission.Outcome = "cancelled"
	if optionID != "" {
		out.Outcome = acp.PermissionOutcome{Outcome: "selected", OptionID: optionID}
		it.Permission.Outcome = optionID
	}
	delete(c.pending, itemID)
	reply(out, nil)
	c.touch(it)
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	return clone(*it), nil
}

// SetOption changes a setting of the session, like the model, and keeps it for
// the agent's later sessions.
func (m *Manager) SetOption(ctx context.Context, a state.Agent, id, value string) (api.ChatSession, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatSession{}, err
	}
	if id == state.ChatOptionContextWindow && a.AI == "claude" {
		return m.setContextWindow(c, value)
	}
	if id == "model" && a.AI == "claude" {
		// "opus[1m]" names Opus: the window is chosen on its own (D91).
		w, _ := c.windows()
		value = w.NormalizeClaudeModel(value)
	}
	i := slices.IndexFunc(c.session.Options, func(o api.ChatOption) bool { return o.ID == id })
	if i < 0 {
		// No live session yet — a new agent whose chat hasn't started, or a
		// project chat that first message creates. The model is still worth
		// choosing now: it's the setting you want to pick before typing, and
		// it's the one AgentBox remembers a menu for. It's stored and applied
		// when the session starts, like every other chosen setting.
		option, ok := c.modelFromRememberedMenu(id)
		if !ok {
			c.mu.Unlock()
			return api.ChatSession{}, fmt.Errorf("%s has no setting %q (its settings are known once it has started)", ToolNames[a.AI], id)
		}
		if !validValue(option, value) && !c.modelNamedOutsideMenu(id, value) {
			c.mu.Unlock()
			return api.ChatSession{}, fmt.Errorf("%q isn't a choice for %s", value, option.Name)
		}
		c.stored.Options[id] = value
		if err := m.Store.SaveChat(context.Background(), a.Project, a.Name, c.stored); err != nil {
			c.mu.Unlock()
			return api.ChatSession{}, err
		}
		// Show the choice back straight away, so the composer reflects what
		// the next session will start on rather than looking unchanged.
		option.Value = value
		c.session.Options = append(c.session.Options, option)
		c.session.ContextUsed, c.session.ContextSize = 0, 0
		c.refreshWindowOption()
		c.markSession()
		c.flush(false)
		defer c.mu.Unlock()
		return clone(c.session), nil
	}
	option := c.session.Options[i]
	// A named model the live menu doesn't list can still be chosen: the tool
	// resolves one at launch against a wider catalogue than set_config_option
	// validates against (D46). It is stored now and starts the next session.
	// Every other setting, and every model the menu does list, is unchanged.
	valid := validValue(option, value)
	named := !valid && c.modelNamedOutsideMenu(id, value)
	if !valid && !named {
		c.mu.Unlock()
		return api.ChatSession{}, fmt.Errorf("%q isn't a choice for %s", value, option.Name)
	}
	c.stored.Options[id] = value
	if err := m.Store.SaveChat(context.Background(), a.Project, a.Name, c.stored); err != nil {
		c.mu.Unlock()
		return api.ChatSession{}, err
	}
	ad := c.adapter
	if named && ad != nil && ad.ready {
		// set_config_option would refuse it, and an error here reads as "your
		// choice didn't stick" when it did. Say when it takes effect instead.
		c.session.Options[i].Value = value
		c.session.ContextUsed, c.session.ContextSize = 0, 0
		c.refreshWindowOption()
		c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
			"%s can't switch to %q while this session is running — it isn't on the menu this account was offered. It's saved, and this chat starts on it next time (clear the chat to start one now).",
			ToolNames[a.AI], value)
		c.markSession()
		c.flush(false)
		defer c.mu.Unlock()
		return clone(c.session), nil
	}
	if ad == nil || !ad.ready {
		// Applied when the session starts.
		if id == "model" && value != option.Value {
			c.session.ContextUsed, c.session.ContextSize = 0, 0
		}
		c.session.Options[i].Value = value
		c.refreshWindowOption()
		c.markSession()
		c.flush(false)
		defer c.mu.Unlock()
		return clone(c.session), nil
	}
	req := setRequest(ad.sessionID, option, value)
	c.mu.Unlock()

	var out acp.SetConfigOptionResponse
	err = ad.conn.Call(ctx, acp.MethodSetConfigOption, req, &out)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return clone(c.session), err
	}
	if c.adapter == ad && out.ConfigOptions != nil {
		c.setOptions(toOptions(out.ConfigOptions))
		// Another model can have another window, and so another place to
		// compact: that only reaches the tool on a restart (window.go).
		c.restartIfWindowMoved()
		c.markSession()
	}
	c.flush(false)
	return clone(c.session), nil
}

// modelNamedOutsideMenu reports a model chosen by naming it rather than by
// picking it off the menu the account was offered.
//
// This is the escape hatch, and it exists because of a chicken and egg: a
// model outside the curated menu — Fable — is advertised only once a session
// has already started on it, so it can never be picked from a menu the first
// time. Claude Code has the same problem and solves it the same way, and its
// own picker says so: "For other/previous model names, specify with --model."
// Naming one here is that, stored: it goes into the tool's settings before the
// next session starts (D46), and from then on the adapter really does advertise
// it, so it becomes an ordinary entry that rememberChoices keeps.
//
// AgentBox still invents no model list of its own beyond the tiny pinned one
// (D69). The menu is the adapter's, plus that; this is the user naming a
// third, different model, which is a different thing from AgentBox guessing
// what an account can run.
//
// "default" is excluded because it is the menu's own sentinel rather than a
// model id, and the one value that must never be written as one. Caller holds
// c.mu.
func (c *conversation) modelNamedOutsideMenu(id, value string) bool {
	return id == "model" && c.agent.AI == "claude" && value != "" && value != "default"
}

// modelMenuSetting is where a tool's remembered model menu is stored, and
// whether it has one at all. Codex keeps its own menu and AgentBox remembers
// none for it.
func modelMenuSetting(ai string) (string, bool) {
	switch ai {
	case "claude":
		return state.SettingClaudeModelChoices, true
	case "opencode":
		return state.SettingOpenCodeModelChoices, true
	}
	return "", false
}

// modelFromRememberedMenu builds the "model" option from the menu the agent's
// own tool last advertised, for an agent whose session hasn't started yet,
// plus AgentBox's own small pinned list for Claude (state.MergePinnedClaudeModels).
// It's only ever a menu that really arrived (rememberChoices, or `opencode
// models` for OpenCode), with the pinned models the one addition; nothing
// else here invents a model. Caller holds c.mu.
func (c *conversation) modelFromRememberedMenu(id string) (api.ChatOption, bool) {
	key, ok := modelMenuSetting(c.agent.AI)
	if id != "model" || !ok {
		return api.ChatOption{}, false
	}
	raw, err := c.m.Store.Setting(context.Background(), key)
	if err != nil || raw == "" {
		return api.ChatOption{}, false
	}
	var choices []api.ChatOptionChoice
	if err := json.Unmarshal([]byte(raw), &choices); err != nil || len(choices) == 0 {
		return api.ChatOption{}, false
	}
	if c.agent.AI == "claude" {
		// AgentBox's own pinned models go on top of the remembered menu, so
		// the composer offers them once any chat on this installation has
		// started — same menu everywhere, not a separate list before a
		// session starts. OpenCode keeps its own menu, untouched.
		choices = state.MergePinnedClaudeModels(choices)
	}
	// A lead's chat, and an agent made before any default was chosen, store no
	// preference at all — not even AgentBox's own "opus[1m]" alias. Showing
	// that as an empty value would leave the composer's button blank; "default"
	// is what an untouched session actually reports (see the live probe in
	// D45), so that's the honest stand-in until a session has really started.
	value := c.stored.Options["model"]
	if value == "" && slices.ContainsFunc(choices, func(ch api.ChatOptionChoice) bool { return ch.Value == "default" }) {
		value = "default"
	}
	return api.ChatOption{
		ID: "model", Name: "Model", Description: "AI model to use",
		Category: "model", Type: "select", Value: value, Choices: choices,
	}, true
}

// Clear removes the conversation and ends its session: the next message starts
// a new one. Chosen settings stay.
func (m *Manager) Clear(a state.Agent) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	c.stopAdapter()
	if err := m.Store.ClearChat(context.Background(), a.Project, a.Name); err != nil {
		return err
	}
	m.removeImages(a.Project, a.Name)
	c.items, c.byID = nil, map[string]*api.ChatItem{}
	c.dirty, c.unsaved, c.appends = map[string]bool{}, map[string]bool{}, nil
	c.turn, c.tools, c.plan, c.open, c.openMessage = nil, map[string]*api.ChatItem{}, nil, nil, ""
	c.queued, c.outbox, c.compaction = nil, nil, nil
	c.clearLimit()
	c.stored.SessionID = ""
	c.session.TurnStartedAt, c.session.ContextUsed, c.session.ContextSize = nil, 0, 0
	c.session.Commands = []api.ChatCommand{}
	c.emit(api.ChatEvent{Cleared: true})
	c.markSession()
	c.flush(true)
	return nil
}

// Stop ends an agent's chat session, if it has one, saying why to a running turn.
func (m *Manager) Stop(ref, reason string) {
	c := m.existing(ref)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loseOutbox(reason + ".")
	c.clearLimit()
	if t := c.turn; t != nil {
		t.cancelled = true
		c.finishTurn(t, nil, errors.New(reason))
	}
	c.stopAdapter()
	c.flush(true)
}

// Forget stops an agent's session and drops its conversation from memory, for
// an agent being destroyed. Its stored items go with the agent.
func (m *Manager) Forget(ref string) {
	c := m.existing(ref)
	if c == nil {
		return
	}
	m.mu.Lock()
	delete(m.convs, ref)
	m.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gone = true
	c.outbox = nil // the agent is going, and its conversation with it
	c.cancelResume()
	if t := c.turn; t != nil {
		t.cancelled = true
		c.finishTurn(t, nil, errors.New("the agent was destroyed"))
	}
	c.stopAdapter()
	c.flush(false)
}

// State is the state of an agent's chat session, or "" if it hasn't chatted
// since the daemon started.
// AgentFor is the agent record a conversation would start its AI tool with.
func (m *Manager) AgentFor(ref string) state.Agent {
	c := m.existing(ref)
	if c == nil {
		return state.Agent{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agent
}

func (m *Manager) State(ref string) string {
	c := m.existing(ref)
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session.State
}

// Close ends every session and stores what hasn't been, for the daemon's shutdown.
func (m *Manager) Close() {
	m.mu.Lock()
	convs := slices.Collect(maps.Values(m.convs))
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range convs {
		c.mu.Lock()
		c.loseOutbox("AgentBox stopped.")
		c.cancelResume()
		if t := c.turn; t != nil {
			t.cancelled = true
			c.finishTurn(t, nil, errors.New("AgentBox stopped"))
		}
		if ad := c.adapter; ad != nil && ad.proc != nil {
			wg.Go(ad.proc.Stop)
		}
		c.stopAdapter()
		c.flush(true)
		c.mu.Unlock()
	}
	wg.Wait()
}

// Wait waits for every adapter's goroutine to end. Close doesn't: an adapter
// still launching when it is called has no process yet to stop, and finishes
// its launch — which can write to the agent's worktree, its tools, its HOME —
// before it finds it was stopped and goes. A test waits for that before its
// directories are removed.
func (m *Manager) Wait() { m.adapters.Wait() }

// conversation is one agent's chat. Everything in it is guarded by mu.
type conversation struct {
	m     *Manager
	agent state.Agent

	mu      sync.Mutex
	loaded  bool
	seq     int64
	items   []*api.ChatItem
	byID    map[string]*api.ChatItem
	session api.ChatSession
	stored  state.Chat
	adapter *adapter // nil while no adapter runs
	turn    *turn    // the running turn
	gone    bool     // the agent was destroyed: nothing more is stored
	// queued holds notices that arrived mid-turn, or during a rollover;
	// finishTurn and rollover deliver them.
	queued []queuedNotice
	// rolling says a hidden prompt is running on the session: it is being
	// consolidated into the project's memory and replaced (rollover.go), or
	// asked to distil the project's events into memories and kept
	// (hidden.go). Notices wait for it, as they wait for a running turn.
	rolling bool
	// compaction is the running compaction's card (rollover.go). While it is
	// set, messages the user sends are held for the fresh session.
	compaction *api.ChatItem
	// windowRestart says the context window changed since the adapter
	// started, so the next turn restarts it (window.go).
	windowRestart bool
	// capture, while a hidden prompt runs, collects what the session says
	// instead of the conversation collecting it.
	capture *strings.Builder
	// outbox holds messages somebody sent mid-turn, oldest first, until the AI
	// tool has them. drainOutbox empties it; draining says that it's running.
	outbox   []*outgoing
	draining bool
	// resumeTimer is the pending "carry on once the usage limit resets"
	// wake-up, resumeGen names it so a timer that fires late does nothing, and
	// resumeTry counts the waits this stretch of limit has already had. See
	// planResume in limit.go: none of it outlives the daemon.
	resumeTimer Timer
	resumeGen   int64
	resumeTry   int

	// The running turn's open text item, tool calls, plan and unanswered permission requests.
	open        *api.ChatItem
	openMessage string
	tools       map[string]*api.ChatItem
	plan        *api.ChatItem
	pending     map[string]func(result any, err error)

	// Changes flush hasn't published or stored yet.
	appends      []api.ChatAppend
	dirty        map[string]bool // items to publish whole
	sessionDirty bool
	unsaved      map[string]bool
	lastSave     time.Time
	timer        *time.Timer
}

// queuedNotice is something that arrived while a turn was running.
type queuedNotice struct {
	text string
	opts NoticeOptions
}

// outgoing is a message sent mid-turn, waiting for the AI tool. turn is the one
// it was sent during, which is the one it may still join.
type outgoing struct {
	item   string // its "aside" item in the conversation
	text   string
	images []api.ChatImage
	turn   *turn
}

type turn struct {
	id        string // the user message's
	text      string
	images    []api.ChatImage
	cancelled bool
	// deferred means a message sent during this turn couldn't join it, so no
	// later one may either: they'd reach the tool out of the order they were sent.
	deferred  bool
	steeredAt time.Time // when it was last given a message, for steerGrace
}

type adapter struct {
	proc      *Process
	conn      *acp.Conn
	sessionID string
	started   chan struct{} // closed once the session is ready, or failed to start
	ready     bool
	steering  bool   // it takes a message into a running turn, per acp.MethodSessionSteer
	images    bool   // it takes images in a prompt, per promptCapabilities.image
	err       error  // why it didn't start
	replaying bool   // a loaded session replays history that's already here
	spend     spend  // what it has cost, for the token ledger (tokens.go)
	window    int64  // the compact window it was started with, 0 for the whole (window.go)
	sizeOf    string // the model and window last remembered from its usage_update
	// account is the Claude Code account it was started with. A session keeps
	// its token until it restarts, while c.agent follows the store (a
	// project's chat moves with its project's account), so what the session
	// reports about its account is filed under this one (sessionAgent).
	account string
	// subagents are the subagent sessions it announced, by session id
	// (subagents.go).
	subagents map[string]*subagent
}

// sessionAgent is the agent as its running session knows it: with the Claude
// Code account the session was started with, which is the token it is really
// using, rather than one the agent has been moved to since.
func (c *conversation) sessionAgent(ad *adapter) state.Agent {
	a := c.agent
	if ad != nil {
		a.ClaudeAccount = ad.account
	}
	return a
}

func (c *conversation) load() error {
	if c.loaded {
		return nil
	}
	ctx := context.Background()
	rows, err := c.m.Store.ChatItems(ctx, c.agent.Project, c.agent.Name)
	if err != nil {
		return err
	}
	stored, err := c.m.Store.Chat(ctx, c.agent.Project, c.agent.Name)
	if err != nil {
		return err
	}
	for _, row := range rows {
		it := &api.ChatItem{}
		if json.Unmarshal(row.Data, it) != nil || it.ID == "" {
			continue
		}
		c.items = append(c.items, it)
		c.byID[it.ID] = it
	}
	c.stored = stored
	c.loaded = true
	if c.agent.AI == "claude" && c.stored.Options["model"] != "" {
		// A model stored as "opus[1m]" before the window was a choice of its
		// own reads as "opus" now, compacting where it always did (D91).
		w, _ := c.windows()
		c.stored.Options["model"] = w.NormalizeClaudeModel(c.stored.Options["model"])
	}
	// Before any session has run, the composer still needs the one setting
	// worth choosing ahead of the first message: the model. Seed it from the
	// menu a Claude Code adapter last advertised, so choosing it here is the
	// same menu, in the same place, as choosing it mid-conversation — not a
	// separate control that only sometimes exists.
	// The context window goes beside it, from the stored model, with or
	// without a remembered menu.
	if c.session.State == api.ChatOff {
		c.rememberedImageSupport()
		if option, ok := c.modelFromRememberedMenu("model"); ok {
			c.session.Options = []api.ChatOption{option}
		}
		c.refreshWindowOption()
	}
	c.settle()
	c.flush(true)
	return nil
}

// settle ends what a previous daemon left running: its session is gone.
func (c *conversation) settle() {
	var unfinished *api.ChatItem
	lost := false
	for _, it := range c.items {
		changed := false
		switch {
		case it.Kind == "user" && it.Result == nil:
			unfinished = it
			it.Result = &api.ChatTurnResult{State: "failed", EndedAt: it.UpdatedAt}
			changed = true
		case it.Streaming:
			it.Streaming = false
			changed = true
		case it.Tool != nil && (it.Tool.Status == "pending" || it.Tool.Status == "in_progress"):
			it.Tool.Status = "stopped"
			changed = true
		case it.Permission != nil && it.Permission.Outcome == "":
			it.Permission.Outcome = "cancelled"
			changed = true
		case it.Subagent != nil && it.Subagent.State == subagentRunning:
			it.Subagent.State = subagentStopped
			changed = true
		case it.Compaction != nil && it.Compaction.State == api.ChatCompactionRunning:
			it.Compaction.State = api.ChatCompactionFailed
			it.Compaction.Error = "AgentBox stopped while it ran"
			it.Text = "The session couldn't be replaced; AgentBox stopped first."
			changed = true
		case it.Kind == "aside" && it.Delivery != api.ChatAsideSent && it.Delivery != api.ChatAsideLost:
			// The outbox only ever lived in memory, so a message still waiting
			// when the daemon stopped has nowhere to go now. Say so rather than
			// leave it looking delivered.
			it.Delivery = api.ChatAsideLost
			lost = true
			changed = true
		}
		if changed {
			c.unsaved[it.ID] = true
		}
	}
	if unfinished != nil {
		c.add("error", unfinished.ID).Text = "AgentBox stopped while this turn was running."
	}
	if lost {
		c.add("error", c.lastTurn()).Text = "AgentBox stopped before the messages above reached " + ToolNames[c.agent.AI] + "."
	}
}

func (c *conversation) startAdapter() *adapter {
	if c.adapter != nil {
		return c.adapter
	}
	ad := &adapter{started: make(chan struct{})}
	c.adapter = ad
	c.session.Error = ""
	c.session.Detail = "Starting " + ToolNames[c.agent.AI]
	c.session.State = c.stateNow()
	c.markSession()
	c.m.adapters.Go(func() { c.run(ad) })
	return ad
}

// stopAdapter ends the adapter, if one runs. Its turn, if any, ends when the
// prompt fails.
func (c *conversation) stopAdapter() {
	ad := c.adapter
	if ad == nil {
		return
	}
	c.adapter = nil
	c.cancelPending()
	c.stopSubagents(ad)
	if ad.proc != nil {
		go ad.proc.Stop()
	}
	c.session.Detail, c.session.Error = "", ""
	c.session.State = c.stateNow()
	c.markSession()
}

func (c *conversation) stateNow() string {
	switch {
	case c.adapter == nil && c.session.Error != "":
		return api.ChatError
	case c.adapter == nil:
		return api.ChatOff
	case !c.adapter.ready:
		return api.ChatStarting
	case c.turn != nil && len(c.pending) > 0:
		return api.ChatWaiting
	case c.turn != nil:
		return api.ChatRunning
	default:
		return api.ChatReady
	}
}

// run starts an adapter and its session, then waits for the adapter to exit.
func (c *conversation) run(ad *adapter) {
	err := c.connect(ad)
	c.mu.Lock()
	if err == nil && c.adapter != ad {
		err = errStopped
	}
	if err != nil {
		ad.err = err
		close(ad.started)
		if c.adapter == ad {
			c.adapter = nil
			c.session.Detail, c.session.Error = "", err.Error()
			c.session.State = c.stateNow()
			c.markSession()
			c.m.logf("chat %s: %v", c.agent.Ref(), err)
		}
		c.flush(true)
		c.mu.Unlock()
		if ad.proc != nil {
			ad.proc.Stop()
			ad.proc.Stdout.Close()
		}
		return
	}
	ad.ready = true
	close(ad.started)
	c.session.Detail = ""
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	c.mu.Unlock()

	<-ad.conn.Done()
	// Its output has ended; make sure the process has too.
	ad.proc.Stop()
	waitErr := ad.proc.Wait()
	ad.proc.Stdout.Close()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adapter != ad {
		return // stopped on purpose
	}
	c.adapter = nil
	c.cancelPending()
	msg := ToolNames[c.agent.AI] + " exited"
	if waitErr != nil {
		msg += " (" + waitErr.Error() + ")"
	}
	if line := lastLine(ad.proc.Stderr()); line != "" {
		msg += ": " + line
	}
	c.session.Detail, c.session.Error = "", msg
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	c.m.logf("chat %s: %s", c.agent.Ref(), msg)
}

// connect launches the adapter, then resumes the agent's session or starts a
// new one, with the settings you chose.
func (c *conversation) connect(ad *adapter) error {
	// Snapshot the agent under the lock: Start replaces c.agent on every call,
	// and this runs on the adapter's own goroutine.
	c.mu.Lock()
	a := c.agent
	model, window := c.launchSettings()
	ad.window = window
	ad.account = a.ClaudeAccount
	c.windowRestart = false
	c.mu.Unlock()
	tool := ToolNames[a.AI]
	status := func(detail string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.adapter == ad {
			c.session.Detail = detail
			c.markSession()
		}
	}
	// The model goes into the tool's own configuration first: it is read once,
	// when the adapter starts, so after that only set_config_option can move it
	// — and that refuses models this account's menu doesn't list (D46).
	if c.m.Prepare != nil {
		if err := c.m.Prepare(context.Background(), a, model, window); err != nil {
			c.m.logf("chat %s: preparing the model %q: %v", a.Ref(), model, err)
			// Said out loud, not just logged. A model that didn't reach the
			// tool means the session starts on something else, and an agent
			// answering on a model nobody chose is the failure this repo has
			// already been burned by. The session still starts: for a model
			// the menu does offer, set_config_option below applies it anyway.
			c.mu.Lock()
			c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
				"AgentBox couldn't tell %s to start on %q (%v). If it isn't a model this account's menu lists, this session is running on something else — the model below says which.",
				tool, model, err)
			c.mu.Unlock()
		}
	}
	proc, err := c.m.Launch(context.Background(), a, status)
	if err != nil {
		return err
	}
	c.mu.Lock()
	ad.proc = proc
	if c.adapter != ad {
		c.mu.Unlock()
		return errStopped
	}
	ad.conn = acp.NewConn(proc.Stdout, proc.Stdin, handler{c: c, ad: ad})
	previous := c.stored.SessionID
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	explain := func(err error) error {
		var rpcErr *acp.Error
		switch {
		case errors.As(err, &rpcErr) && rpcErr.Code == acp.CodeAuthRequired:
			return fmt.Errorf("%s has no login in this agent: %w (add one with agentbox auth %s)", tool, err, a.AI)
		case errors.Is(err, acp.ErrClosed):
			if line := lastLine(proc.Stderr()); line != "" {
				return fmt.Errorf("%w: %s%s", err, line, hint(proc.Stderr()))
			}
		}
		return err
	}

	var init acp.InitializeResponse
	err = ad.conn.Call(ctx, acp.MethodInitialize, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
		// Subagents as sessions of their own, each shown as a card (D86).
		// An adapter that doesn't know the capability ignores it.
		ClientCapabilities: acp.ClientCapabilities{Subagents: &struct{}{}, Meta: acp.SubagentSessionsMeta()},
		ClientInfo:         &acp.Implementation{Name: "agentbox", Title: "AgentBox", Version: c.m.Version},
	}, &init)
	if err != nil {
		return explain(err)
	}

	mcp := []acp.McpServer{} // the agent's own configuration gives the tools their MCP servers
	var resp acp.SessionResponse
	var resumeErr error
	if previous != "" {
		req := acp.ResumeSessionRequest{SessionID: previous, Cwd: a.Worktree, McpServers: mcp}
		switch caps := init.AgentCapabilities; {
		case caps.SessionCapabilities.Resume != nil:
			resumeErr = ad.conn.Call(ctx, acp.MethodSessionResume, req, &resp)
		case caps.LoadSession:
			c.setReplaying(ad, true)
			resumeErr = ad.conn.Call(ctx, acp.MethodSessionLoad, req, &resp)
			c.setReplaying(ad, false)
		default:
			resumeErr = errors.New("its adapter can't resume sessions")
		}
		resp.SessionID = previous
	}
	if previous == "" || resumeErr != nil {
		resp = acp.SessionResponse{}
		if err := ad.conn.Call(ctx, acp.MethodSessionNew, acp.NewSessionRequest{Cwd: a.Worktree, McpServers: mcp}, &resp); err != nil {
			return explain(err)
		}
		if resp.SessionID == "" {
			return errors.New("the adapter started no session")
		}
	}

	c.mu.Lock()
	if c.adapter != ad {
		c.mu.Unlock()
		return errStopped
	}
	ad.sessionID = resp.SessionID
	ad.steering = init.Meta != nil && init.Meta.Steering != nil && init.Meta.Steering.Supported
	ad.images = init.AgentCapabilities.PromptCapabilities.Image
	c.setImageSupport(ad.images)
	if resumeErr != nil && len(c.items) > 0 {
		c.add("notice", c.lastTurn()).Text = fmt.Sprintf("This is a new %s session, which doesn't remember the conversation above: the earlier session couldn't be resumed (%v).", tool, explain(resumeErr))
	}
	if c.stored.SessionID != resp.SessionID {
		c.stored.SessionID = resp.SessionID
		if err := c.m.Store.SaveChat(context.Background(), a.Project, a.Name, c.stored); err != nil {
			c.m.logf("chat %s: saving the session: %v", a.Ref(), err)
		}
	}
	if info := init.AgentInfo; info != nil {
		c.session.Adapter = strings.TrimSpace(cmp(info.Title, info.Name) + " " + info.Version)
	}
	c.setOptions(toOptions(resp.ConfigOptions))
	ids := make([]string, 0, len(c.session.Options))
	for _, o := range c.session.Options {
		ids = append(ids, o.ID)
	}
	c.markSession()
	c.mu.Unlock()
	c.rememberChoices()

	// Apply the settings you chose, one at a time: changing the model can change
	// the choices of another setting, like the effort.
	for _, id := range ids {
		c.mu.Lock()
		option, value, ok := c.wanted(id)
		c.mu.Unlock()
		if !ok {
			continue
		}
		var out acp.SetConfigOptionResponse
		if err := ad.conn.Call(ctx, acp.MethodSetConfigOption, setRequest(resp.SessionID, option, value), &out); err != nil {
			c.m.logf("chat %s: setting %s to %s: %v", a.Ref(), id, value, err)
			if id == "model" {
				// Say so, loudly. Every other setting may fall back to the
				// tool's own default in silence, but a model that didn't
				// apply means the agent is quietly answering on something
				// else — agents ran weeks of turns on Sonnet this way, with
				// nothing on screen to show for it.
				c.mu.Lock()
				c.add("notice", c.lastTurn()).Text = fmt.Sprintf(
					"%s wouldn't set the model to %q, so this agent is running on %s instead. That model may not be one this account offers any more \u2014 pick another below.",
					ToolNames[c.agent.AI], value, c.runningModelName())
				c.mu.Unlock()
			}
			continue
		}
		c.mu.Lock()
		if c.adapter == ad && out.ConfigOptions != nil {
			c.setOptions(toOptions(out.ConfigOptions))
			c.markSession()
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *conversation) setReplaying(ad *adapter, replaying bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ad.replaying = replaying
}

// wanted returns the value a setting should get: the one you chose, or full
// access for an autonomous agent's permission mode. ok is false when it
// already has it, or (other than for "model", see below) the choice isn't
// offered anymore.
func (c *conversation) wanted(id string) (api.ChatOption, string, bool) {
	i := slices.IndexFunc(c.session.Options, func(o api.ChatOption) bool { return o.ID == id })
	if i < 0 {
		return api.ChatOption{}, "", false
	}
	option := c.session.Options[i]
	if id == state.ChatOptionContextWindow {
		return option, "", false // AgentBox's own, applied at launch (window.go)
	}
	value, ok := c.stored.Options[id]
	if !ok && c.agent.Autonomous && option.Category == "mode" {
		for _, choice := range option.Choices {
			if choice.Kind == "full_access" {
				value, ok = choice.Value, true
				break
			}
		}
	}
	if !ok || value == option.Value {
		return option, value, false
	}
	if option.ID == "model" {
		// The model is worth trying even when it isn't literally among this
		// account's choices: the adapter resolves a preference itself (see
		// resolveModelPreference in claude-agent-acp) — "opus[1m]" becomes
		// plain "opus" on an account with no 1M-context entry, the same way
		// it turns a human-typed alias like "opus" into a full model ID.
		// validValue still gates an explicit pick made through SetOption:
		// that's someone choosing from what's on screen, not a stored
		// preference reaching past what this account happens to offer.
		//
		// A model absent from option.Choices (Fable, at the time of writing)
		// isn't necessarily unusable by this account at all: confirmed live,
		// this account's raw /v1/messages inference and the bare `claude`
		// CLI's explicit --model override both run claude-fable-5-1 fine —
		// but Claude Code's own mid-session picker doesn't offer it (the
		// interactive /model list is the same four entries set_config_option
		// validates against and refuses anything outside). set_config_option
		// itself has no equivalent of --model's one-off override — but a
		// separate, launch-time resolver (settings.json's "model" key) does
		// reach Fable, and PrepareChatModel now writes that file before the
		// adapter starts, which is how such a model gets onto this list in the
		// first place. See D45 and D46.
		// So "some surface of this account accepts this model" is
		// never proof it belongs in *this* list; only what set_config_option
		// itself validates against does.
		return option, value, true
	}
	return option, value, validValue(option, value)
}

// prompt sends a turn's message once the session is ready, and ends the turn
// when the tool answers.
func (c *conversation) prompt(ad *adapter, t *turn) {
	<-ad.started
	c.mu.Lock()
	if c.turn != t {
		c.mu.Unlock()
		return
	}
	if ad.err != nil {
		err := ad.err
		if !errors.Is(err, errStopped) {
			err = fmt.Errorf("%s didn't start: %w", ToolNames[c.agent.AI], err)
		}
		c.finishTurn(t, nil, err)
		c.mu.Unlock()
		return
	}
	if t.cancelled {
		// Stopped just as the session became ready: don't send the prompt at all.
		c.finishTurn(t, &acp.PromptResponse{StopReason: "cancelled"}, nil)
		c.mu.Unlock()
		return
	}
	c.session.State = c.stateNow()
	c.markSession()
	sessionID, dir, text, images := ad.sessionID, c.m.imageDir(c.agent), t.text, t.images
	if len(images) > 0 && !ad.images {
		// Attached before this tool's adapter had ever said whether it reads
		// images, and it turned out not to: the text still goes, and the
		// pictures are said to be missing where you see it.
		c.add("notice", t.id).Text = fmt.Sprintf("%s's adapter doesn't take images, so the %d sent with this message didn't reach it.", ToolNames[c.agent.AI], len(images))
		c.flush(true)
		images = nil
		if text == "" {
			text = "[AgentBox: the user sent only images, which this tool can't read.]"
		}
	}
	c.mu.Unlock()

	var res acp.PromptResponse
	err := ad.conn.Call(context.Background(), acp.MethodSessionPrompt, acp.PromptRequest{
		SessionID: sessionID,
		Prompt:    c.promptBlocks(dir, text, images),
	}, &res)
	c.mu.Lock()
	defer c.mu.Unlock()
	// A turn that failed still spent what it spent before it did: its
	// response is empty, so what is booked is the cost alone.
	c.book(ad, state.TokensTurn, t.id, &res)
	if err != nil {
		c.finishTurn(t, nil, err)
	} else {
		c.finishTurn(t, &res, nil)
	}
}

// hint adds what to do about a failure whose own message doesn't say. A tool
// manager's errors are the common one: they name themselves and nothing else.
func hint(stderr string) string {
	if strings.Contains(stderr, "mise ERROR") || strings.Contains(stderr, "not a valid shim") {
		return ". Its AI tool is installed with mise and couldn't find itself." +
			" Check that mise works for you (mise which claude-agent-acp), or put claude-agent-acp on your PATH another way"
	}
	return ""
}

// authSigns are what a refused login looks like by the time it reaches here.
// The adapter passes the provider's own wording through, so these are the
// sentences Anthropic and Claude Code write, matched case-insensitively.
//
// They are deliberately narrow. Saying a token is dead when it isn't would
// send someone to log in again for nothing, so nothing vague ("unauthorized",
// a bare 401) is on the list, and neither is a billing refusal: a spent credit
// balance is not a bad token.
var authSigns = []string{
	"oauth token is invalid",
	"oauth access token is invalid",
	"oauth token expired",
	"oauth token has expired",
	"authentication_error",
	"invalid api key",
	"please run /login",
	"401 unauthorized",
}

// AuthFailure reports whether a failed turn failed on the agent's login.
func AuthFailure(text string) bool {
	text = strings.ToLower(text)
	for _, sign := range authSigns {
		if strings.Contains(text, sign) {
			return true
		}
	}
	return false
}

// firstLine keeps an error readable where only a sentence fits.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

var stopNotes = map[string]string{
	"max_tokens":        "The response stopped at the model's output limit.",
	"max_turn_requests": "The turn stopped at its limit of model requests.",
	"refusal":           "The model declined to continue.",
}

// finishTurn ends a turn with the tool's answer, or with an error.
func (c *conversation) finishTurn(t *turn, res *acp.PromptResponse, err error) {
	if c.turn != t {
		return
	}
	c.closeOpen()
	for _, it := range c.tools {
		if it.Tool.Status == "pending" || it.Tool.Status == "in_progress" {
			it.Tool.Status = "stopped"
			c.touch(it)
		}
	}
	c.cancelPending()
	result := &api.ChatTurnResult{EndedAt: time.Now()}
	limited := false
	switch {
	case t.cancelled:
		result.State = "cancelled"
		if res != nil {
			result.StopReason = res.StopReason
		}
		if err != nil && !errors.Is(err, acp.ErrClosed) {
			c.add("notice", t.id).Text = "Stopped: " + err.Error() + "."
		}
	case err != nil:
		result.State = "failed"
		c.add("error", t.id).Text = err.Error()
		if c.m.AuthFailed != nil && AuthFailure(err.Error()) {
			// Off the lock this holds, and off this turn's path: nothing here
			// waits for it.
			go c.m.AuthFailed(c.sessionAgent(c.adapter), firstLine(err.Error()))
		}
		// A spent usage limit is the one failure the chat can get over on its
		// own, by waiting for it to reset (limit.go).
		limited = c.limited(t, err.Error())
	case res.StopReason == "cancelled":
		result.State, result.StopReason = "cancelled", res.StopReason
	default:
		result.State, result.StopReason = "completed", res.StopReason
		if note := stopNotes[res.StopReason]; note != "" {
			c.add("notice", t.id).Text = note
		}
	}
	if !limited {
		// Any other ending clears the limit and the count of waits with it, so
		// the next one starts its backoff from the beginning.
		c.clearLimit()
	}
	if user := c.byID[t.id]; user != nil {
		user.Result = result
		c.touch(user)
	}
	c.turn, c.tools, c.plan = nil, map[string]*api.ChatItem{}, nil
	c.session.TurnStartedAt = nil
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)

	// Anything that arrived while the turn ran. Several become one notice, so
	// three agents finishing at once wake the chat once, not three times.
	c.deliverQueued()
	// Messages somebody sent while the turn ran that the tool never took: now
	// that nothing runs, they go in as a turn of their own.
	c.drain()
	// Whoever ended the turn may want to know, so the daemon can tell the
	// project's chat that this agent finished.
	if c.m.Finished != nil && !c.agent.IsLead() {
		go c.m.Finished(c.agent, *result)
	}
	// Nothing that waited started another turn, so the chat is idle: the
	// moment the daemon can replace a full session without anyone waiting.
	if c.m.Idle != nil && c.turn == nil && !c.gone {
		go c.m.Idle(c.agent)
	}
}

func (c *conversation) cancelPending() {
	for id, reply := range c.pending {
		reply(acp.RequestPermissionResponse{Outcome: acp.PermissionOutcome{Outcome: "cancelled"}}, nil)
		if it := c.byID[id]; it != nil && it.Permission != nil && it.Permission.Outcome == "" {
			it.Permission.Outcome = "cancelled"
			c.touch(it)
		}
	}
	c.pending = map[string]func(any, error){}
}

func (c *conversation) lastTurn() string {
	if c.turn != nil {
		return c.turn.id
	}
	for i := len(c.items) - 1; i >= 0; i-- {
		if c.items[i].Kind == "user" {
			return c.items[i].ID
		}
	}
	return ""
}

// handler takes what an adapter sends. Updates from an adapter that was
// replaced, or for another session (a subagent's), are dropped.
type handler struct {
	c  *conversation
	ad *adapter
}

func (h handler) Notify(method string, params json.RawMessage) {
	if method != acp.MethodSessionUpdate {
		return
	}
	var n acp.SessionNotification
	if json.Unmarshal(params, &n) != nil {
		return
	}
	c, u := h.c, n.Update
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adapter != h.ad {
		return
	}
	if h.ad.sessionID != "" && n.SessionID != h.ad.sessionID {
		// Another session is one of this adapter's subagents, or nothing to
		// do with this chat. A subagent is recorded whether or not a turn is
		// running: one started in the background goes on after its turn.
		if sa := h.ad.subagents[n.SessionID]; sa != nil && !h.ad.replaying {
			c.subagentUpdate(h.ad, sa, u)
		}
		return
	}
	switch u.SessionUpdate {
	case "subagent_spawned":
		if !h.ad.replaying {
			c.spawnSubagent(h.ad, nil, u)
		}
		return
	case "subagent_state_update":
		if !h.ad.replaying {
			c.endSubagent(h.ad, u)
		}
		return
	case "available_commands_update":
		commands := make([]api.ChatCommand, 0, len(u.AvailableCommands))
		for _, cmd := range u.AvailableCommands {
			command := api.ChatCommand{Name: cmd.Name, Description: cmd.Description}
			if cmd.Input != nil {
				command.Hint = cmd.Input.Hint
			}
			commands = append(commands, command)
		}
		c.session.Commands = commands
		c.markSession()
		return
	case "config_option_update":
		c.setOptions(toOptions(u.ConfigOptions))
		c.markSession()
		return
	case "current_mode_update":
		for i, o := range c.session.Options {
			if o.Category == "mode" {
				c.session.Options[i].Value = u.CurrentModeID
			}
		}
		c.markSession()
		return
	case "usage_update":
		c.session.ContextUsed, c.session.ContextSize = u.Used, c.contextSize(h.ad, u.Size)
		if model := optionValueOf(c.session.Options, "model"); c.agent.AI == "claude" && u.Size > 0 && model != "" && h.ad.sizeOf != model+"="+strconv.FormatInt(u.Size, 10) {
			// The account's own answer to how long this model's window is,
			// which is what the context window offers next time (D91).
			h.ad.sizeOf = model + "=" + strconv.FormatInt(u.Size, 10)
			ref, size := c.agent.Ref(), u.Size
			go func() {
				if err := c.m.Store.RememberClaudeModelWindow(context.Background(), model, size); err != nil {
					c.m.logf("chat %s: remembering %s's window: %v", ref, model, err)
				}
			}()
		}
		h.ad.spend.observe(u.Cost)
		// A cost with no turn running and no hidden prompt asking is a result
		// the session made by itself — a background task finishing woke it —
		// and it goes in the ledger now: there is no turn for it to wait for.
		// Everything else is booked when its prompt answers.
		if u.Cost != nil && c.turn == nil && !c.rolling && !h.ad.replaying {
			c.book(h.ad, state.TokensBackground, newID(), nil)
		}
		if rl := u.Meta.RateLimit; rl != nil && c.m.Limits != nil && !h.ad.replaying {
			go c.m.Limits(c.sessionAgent(h.ad), *rl)
		}
		c.markSession()
		return
	}
	// The hidden consolidation prompt (rollover.go) runs outside any turn, so
	// what it says would be dropped by the return below. It is collected here
	// instead, which is what keeps it out of the conversation: nothing the
	// user sees gains an item for it.
	if c.capture != nil && c.turn == nil && !h.ad.replaying && u.SessionUpdate == "agent_message_chunk" {
		c.capture.WriteString(u.Text())
		return
	}
	if h.ad.replaying || c.turn == nil {
		return
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		c.appendText("assistant", u.MessageID, u.Text())
	case "agent_thought_chunk":
		c.appendText("thought", u.MessageID, u.Text())
	case "tool_call", "tool_call_update":
		c.updateTool(u)
	case "plan":
		c.updatePlan(u.Entries)
	}
}

func (h handler) Request(method string, params json.RawMessage, reply func(any, error)) {
	cancelled := acp.RequestPermissionResponse{Outcome: acp.PermissionOutcome{Outcome: "cancelled"}}
	if method != acp.MethodRequestPermission {
		// AgentBox offers no files or terminals: the tools use their own, in the agent.
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method + " isn't supported"})
		return
	}
	var req acp.RequestPermissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		reply(nil, &acp.Error{Code: -32602, Message: err.Error()})
		return
	}
	c := h.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adapter != h.ad || c.turn == nil || c.turn.cancelled {
		reply(cancelled, nil)
		return
	}
	title := ""
	if req.ToolCall.Title != nil {
		title = *req.ToolCall.Title
	}
	if req.ToolCall.ToolCallID != "" {
		// A subagent asks under its own session, and the tool call it asks
		// about is one of its own; the question itself goes in the
		// conversation, where somebody will see it.
		if sa := h.ad.subagents[req.SessionID]; sa != nil && req.SessionID != h.ad.sessionID {
			sa.updateTool(c, req.ToolCall)
			if title == "" {
				title = sa.tools[req.ToolCall.ToolCallID].Tool.Title
			}
		} else {
			c.updateTool(req.ToolCall)
			if title == "" {
				title = c.tools[req.ToolCall.ToolCallID].Tool.Title
			}
		}
	}
	c.closeOpen()
	it := c.add("permission", c.turn.id)
	it.Permission = &api.ChatPermission{CallID: req.ToolCall.ToolCallID, Title: title, Options: []api.ChatPermissionOption{}}
	for _, o := range req.Options {
		it.Permission.Options = append(it.Permission.Options, api.ChatPermissionOption{ID: o.OptionID, Name: o.Name, Kind: o.Kind})
	}
	c.pending[it.ID] = reply
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
}

// appendText adds streamed text to the open message or thought, or opens a new
// one when the kind or the message changes.
func (c *conversation) appendText(kind, messageID, text string) {
	if text == "" {
		return
	}
	it := c.open
	if it != nil && (it.Kind != kind || (messageID != "" && c.openMessage != "" && messageID != c.openMessage)) {
		c.closeOpen()
		it = nil
	}
	if it == nil {
		if strings.TrimSpace(text) == "" {
			return // don't open a message with blank space
		}
		it = c.add(kind, c.turn.id)
		it.Text, it.Streaming = text, true
		c.open, c.openMessage = it, messageID
		return
	}
	if kind == "thought" && len(it.Text) >= maxThought {
		return
	}
	it.Text += text
	it.UpdatedAt = time.Now()
	c.unsaved[it.ID] = true
	if !c.dirty[it.ID] {
		c.appends = append(c.appends, api.ChatAppend{ID: it.ID, Text: text})
	}
	c.schedule(flushDelay)
}

func (c *conversation) closeOpen() {
	if c.open == nil {
		return
	}
	c.open.Streaming = false
	c.touch(c.open)
	c.open, c.openMessage = nil, ""
}

func (c *conversation) updateTool(u acp.SessionUpdate) {
	if u.ToolCallID == "" {
		return
	}
	it := c.tools[u.ToolCallID]
	if it == nil {
		c.closeOpen()
		it = c.add("tool", c.turn.id)
		it.Tool = &api.ChatTool{CallID: u.ToolCallID, Kind: "other", Status: "pending"}
		c.tools[u.ToolCallID] = it
	}
	applyTool(it.Tool, u)
	c.touch(it)
}

// applyTool copies what a tool_call or tool_call_update says into a tool call.
func applyTool(t *api.ChatTool, u acp.SessionUpdate) {
	if u.Title != nil && *u.Title != "" {
		t.Title = *u.Title
	}
	if u.Kind != "" {
		t.Kind = u.Kind
	}
	if u.Status != "" {
		t.Status = u.Status
	}
	if name := cmp(u.Meta.ClaudeCode.ToolName, u.Name); name != "" {
		t.Name = name
	}
	if len(u.Locations) > 0 {
		var paths []string
		for _, l := range u.Locations {
			if l.Path != "" && !slices.Contains(paths, l.Path) {
				paths = append(paths, l.Path)
			}
		}
		t.Paths = paths
	}
	if command := commandOf(u.RawInput); command != "" {
		t.Command = command
	}
	if content, ok := u.ToolContent(); ok {
		var texts []string
		var diffs []api.ChatDiff
		for _, block := range content {
			switch block.Type {
			case "diff":
				diffs = append(diffs, toDiff(block))
			case "content":
				if s := acp.BlockText(block.Content); strings.TrimSpace(s) != "" {
					texts = append(texts, unfence(s))
				}
			}
		}
		if len(diffs) > 0 {
			t.Diffs = diffs
		}
		if len(texts) > 0 {
			t.Output = tailText(strings.Join(texts, "\n"), maxOutput)
		}
	}
	if out := rawText(u.RawOutput); out != "" && (t.Output == "" || t.Status == "completed" || t.Status == "failed") {
		t.Output = tailText(out, maxOutput)
	}
}

func (c *conversation) updatePlan(entries []acp.PlanEntry) {
	if c.plan == nil {
		c.plan = c.add("plan", c.turn.id)
	}
	plan := make([]api.ChatPlanEntry, 0, len(entries))
	for _, e := range entries {
		plan = append(plan, api.ChatPlanEntry{Content: e.Content, Status: e.Status})
	}
	c.plan.Plan = plan
	c.touch(c.plan)
}

// add appends a new item to the conversation.
func (c *conversation) add(kind, turn string) *api.ChatItem {
	now := time.Now()
	it := &api.ChatItem{ID: newID(), Turn: turn, Kind: kind, CreatedAt: now, UpdatedAt: now}
	c.items = append(c.items, it)
	c.byID[it.ID] = it
	c.touch(it)
	return it
}

// touch marks an item to be published whole and stored.
func (c *conversation) touch(it *api.ChatItem) {
	it.UpdatedAt = time.Now()
	c.dirty[it.ID] = true
	c.unsaved[it.ID] = true
	c.schedule(flushDelay)
}

func (c *conversation) markSession() {
	c.sessionDirty = true
	c.schedule(flushDelay)
}

func (c *conversation) schedule(d time.Duration) {
	if c.timer != nil {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(d, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.timer == timer {
			c.timer = nil
			c.flush(false)
		}
	})
	c.timer = timer
}

func (c *conversation) emit(ev api.ChatEvent) {
	c.seq++
	ev.Agent, ev.Seq = c.agent.Ref(), c.seq
	if c.m.Publish == nil {
		return // nobody is listening; the change is still stored
	}
	c.m.Publish(ev)
}

// flush publishes what changed, in the order it happened: text added to items
// first, then items published whole, in conversation order, then the session.
// It stores changed items at most once a second, unless save is set.
func (c *conversation) flush(save bool) {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	var merged []api.ChatAppend
	for _, ap := range c.appends {
		switch n := len(merged); {
		case c.dirty[ap.ID]: // published whole below
		case n > 0 && merged[n-1].ID == ap.ID:
			merged[n-1].Text += ap.Text
		default:
			merged = append(merged, ap)
		}
	}
	c.appends = nil
	for i := range merged {
		c.emit(api.ChatEvent{Append: &merged[i]})
	}
	if len(c.dirty) > 0 {
		for _, it := range c.items {
			if c.dirty[it.ID] {
				item := clone(*it)
				c.emit(api.ChatEvent{Item: &item})
			}
		}
		c.dirty = map[string]bool{}
	}
	if c.sessionDirty {
		session := clone(c.session)
		c.emit(api.ChatEvent{Session: &session})
		c.sessionDirty = false
	}
	switch {
	case len(c.unsaved) == 0:
	case save || time.Since(c.lastSave) >= saveInterval:
		c.save()
	default:
		c.schedule(saveInterval)
	}
}

func (c *conversation) save() {
	if c.gone {
		c.unsaved = map[string]bool{}
		return
	}
	rows := make([]state.ChatItem, 0, len(c.unsaved))
	for i, it := range c.items {
		if !c.unsaved[it.ID] {
			continue
		}
		data, err := json.Marshal(it)
		if err != nil {
			continue
		}
		rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(i), Data: data})
	}
	c.lastSave = time.Now()
	if err := c.m.Store.SaveChatItems(context.Background(), c.agent.Project, c.agent.Name, rows); err != nil {
		c.m.logf("chat %s: storing the conversation: %v", c.agent.Ref(), err)
		c.schedule(saveInterval)
		return
	}
	c.unsaved = map[string]bool{}
}

func toOptions(options []acp.ConfigOption) []api.ChatOption {
	out := make([]api.ChatOption, 0, len(options))
	for _, o := range options {
		option := api.ChatOption{ID: o.ID, Name: o.Name, Description: o.Description, Category: o.Category, Type: o.Type, Value: o.Value, Choices: []api.ChatOptionChoice{}}
		for _, ch := range o.Choices {
			option.Choices = append(option.Choices, api.ChatOptionChoice{Value: ch.Value, Name: ch.Name, Description: ch.Description, Group: ch.Group, Kind: ch.Kind})
		}
		out = append(out, option)
	}
	return out
}

// setOptions replaces the session's options. usage_update reports the context
// window for whichever model is actually running, so once the model changes,
// the last reading belongs to a model that's no longer current: clearing it
// here means the composer shows nothing until a turn on the new model reports
// a fresh one, rather than the old model's size passed off as the new one's.
func (c *conversation) setOptions(opts []api.ChatOption) {
	if c.agent.AI == "claude" {
		// AgentBox's own pinned models go on top of the live menu the adapter
		// just advertised, same as before a session starts (see
		// modelFromRememberedMenu) — so the composer offers them whether or
		// not a session is running. rememberChoices strips them back out
		// before storing, so the remembered menu stays the adapter's alone.
		for i := range opts {
			if opts[i].Category == "model" && opts[i].Type == "select" {
				opts[i].Choices = state.MergePinnedClaudeModels(opts[i].Choices)
			}
		}
	}
	if old, new := optionValueOf(c.session.Options, "model"), optionValueOf(opts, "model"); old != "" && new != "" && old != new {
		c.session.ContextUsed, c.session.ContextSize = 0, 0
	}
	c.session.Options = opts
	c.refreshWindowOption()
}

// runningModelName names the model the session is actually on, for a message
// to you. The adapter's display name when it has one, else the raw value.
// Caller holds c.mu.
func (c *conversation) runningModelName() string {
	i := slices.IndexFunc(c.session.Options, func(o api.ChatOption) bool { return o.ID == "model" })
	if i < 0 {
		return "its default model"
	}
	option := c.session.Options[i]
	for _, ch := range option.Choices {
		if ch.Value == option.Value {
			return ch.Name
		}
	}
	if option.Value == "" {
		return "its default model"
	}
	return option.Value
}

// rememberChoices stores the menus a Claude Code adapter advertised — the
// models, and the effort levels — so a page with no chat running can offer the
// real thing. AgentBox never composes these lists itself: what an account may
// use comes over ACP and differs per account, and inventing an entry only
// produces a choice that resolves to nothing. Best effort — failing to
// remember a menu must not stop a session starting.
//
// The two menus are not quite alike. The models are the account's, and stay
// put. The effort levels are the ones "available for this model", and a model
// can advertise none at all (Haiku 4.5 sends no effort option), so what is
// remembered is the levels Claude Code has been seen to name — enough to catch
// a level it has never heard of, not a promise about any one model. A session
// that sends no menu leaves the last one alone rather than forgetting it.
func (c *conversation) rememberChoices() {
	// c.agent is replaced under c.mu on every Start, so it's read here too.
	c.mu.Lock()
	ai, ref := c.agent.AI, c.agent.Ref()
	// Each tool's menus are remembered under its own keys: an OpenCode model
	// is a provider/model id and means nothing to Claude Code, so the two
	// lists must never land in the same setting.
	categories := map[string]string{}
	switch ai {
	case "claude":
		categories[state.SettingClaudeModelChoices] = "model"
		categories[state.SettingClaudeEffortChoices] = "thought_level"
	case "opencode":
		categories[state.SettingOpenCodeModelChoices] = "model"
	}
	menus := map[string][]api.ChatOptionChoice{}
	for key, category := range categories {
		i := slices.IndexFunc(c.session.Options, func(o api.ChatOption) bool { return o.Category == category && o.Type == "select" })
		if i >= 0 {
			menus[key] = slices.Clone(c.session.Options[i].Choices)
		}
	}
	c.mu.Unlock()
	for key, choices := range menus {
		if key == state.SettingClaudeModelChoices {
			// setOptions folds AgentBox's own pinned models into the live
			// menu; they don't belong in what's remembered as the adapter's,
			// so they're taken back out here.
			choices = slices.DeleteFunc(choices, state.IsPinnedClaudeChoice)
		}
		if len(choices) == 0 {
			continue
		}
		raw, err := json.Marshal(choices)
		if err != nil {
			continue
		}
		if err := c.m.Store.SetSetting(context.Background(), key, string(raw)); err != nil {
			c.m.logf("chat %s: remembering the %s menu: %v", ref, key, err)
		}
	}
}

func optionValueOf(options []api.ChatOption, id string) string {
	i := slices.IndexFunc(options, func(o api.ChatOption) bool { return o.ID == id })
	if i < 0 {
		return ""
	}
	return options[i].Value
}

// validValue reports whether value is a genuine choice of o — one the tool
// itself offers, not one of AgentBox's own pinned entries folded into the
// menu for display (setOptions, modelFromRememberedMenu). A pinned model is
// never "valid" here even while it's on the menu: picking one always goes
// through modelNamedOutsideMenu instead, the same launch-time channel as a
// model typed by hand (D46), because the running adapter was never told
// about it and a live set_config_option call for it would only fail.
func validValue(o api.ChatOption, value string) bool {
	if o.Type == "boolean" {
		return value == "true" || value == "false"
	}
	return slices.ContainsFunc(o.Choices, func(ch api.ChatOptionChoice) bool {
		return ch.Value == value && !state.IsPinnedClaudeChoice(ch)
	})
}

func setRequest(sessionID string, o api.ChatOption, value string) acp.SetConfigOptionRequest {
	req := acp.SetConfigOptionRequest{SessionID: sessionID, ConfigID: o.ID, Value: value}
	if o.Type == "boolean" {
		req.Value, req.Type = value == "true", "boolean"
	}
	return req
}

func toDiff(block acp.ToolCallContent) api.ChatDiff {
	d := api.ChatDiff{Path: block.Path, NewText: block.NewText, Created: block.OldText == nil}
	if block.OldText != nil {
		d.OldText = *block.OldText
	}
	if len(d.OldText) > maxDiffText || len(d.NewText) > maxDiffText {
		d.OldText, d.NewText, d.Truncated = headText(d.OldText, maxDiffText), headText(d.NewText, maxDiffText), true
	}
	return d
}

// commandOf finds the command line in a tool call's input: a string, or an
// argument list whose shell wrapper (bash -lc '…') is left out.
func commandOf(raw json.RawMessage) string {
	var in struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(in.Command, &s) == nil && s != "" {
		return s
	}
	var argv []string
	if json.Unmarshal(in.Command, &argv) == nil && len(argv) > 0 {
		if len(argv) == 3 && strings.HasSuffix(argv[0], "sh") && (argv[1] == "-c" || argv[1] == "-lc") {
			return argv[2]
		}
		return strings.Join(argv, " ")
	}
	return in.Cmd
}

// rawText is a tool call's raw output as text.
func rawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		for _, key := range []string{"formatted_output", "aggregated_output", "output", "stdout"} {
			var v string
			if json.Unmarshal(fields[key], &v) == nil && v != "" {
				var stderr string
				if key == "stdout" && json.Unmarshal(fields["stderr"], &stderr) == nil && stderr != "" {
					v += "\n" + stderr
				}
				return v
			}
		}
	}
	return string(raw)
}

// unfence takes the text out of a Markdown code fence that wraps all of it.
func unfence(s string) string {
	t := strings.TrimSpace(s)
	nl := strings.IndexByte(t, '\n')
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") || nl < 0 || nl >= len(t)-3 {
		return s
	}
	return strings.TrimSuffix(strings.TrimSuffix(t[nl+1:], "```"), "\n")
}

func tailText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := len(s) - max
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "…" + s[i:]
}

func headText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

func cmp(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// clone copies a value through JSON, so what's published or returned doesn't
// share memory with the conversation, which keeps changing.
func clone[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		panic("chat: " + err.Error())
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		panic("chat: " + err.Error())
	}
	return out
}
