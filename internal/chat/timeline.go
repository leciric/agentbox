package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// An agent's timeline: the turns of its conversation, each ending in a
// checkpoint of its worktree (internal/agent/checkpoint.go), that the user can
// roll the agent back to or fork a new agent from.
//
// Either way the AI tool's session can't follow: none of the three can be
// told to forget its last turns, and a fork's tool has never seen the
// conversation at all. So the session restarts, and the first prompt of the
// fresh one carries a context transfer: the conversation up to the turn,
// written out by Handoff. It goes ahead of the user's next message rather than
// into the brief, because it belongs to this conversation, not to the agent,
// and the notice that marks the rollback shows it, so what the model was told
// is never hidden from whoever reads the chat.

// handoffBudget is the most a context transfer spends, in bytes: about 10k
// tokens, the cost of a long brief.
const handoffBudget = 40 << 10

// ErrTurnRunning refuses a rollback while the agent is mid-turn: what the turn
// is doing would land on the files being put back.
var ErrTurnRunning = errors.New("a turn is running: stop it first")

// Turn reports which turn of the conversation the user message id heads,
// counted from 1, and the start of that message's text. ok is false when the
// conversation has no such turn any more: it was rolled back or cleared.
func (m *Manager) Turn(a state.Agent, id string) (number int, prompt string, ok bool) {
	c, err := m.conversation(a)
	if err != nil {
		return 0, "", false
	}
	defer c.mu.Unlock()
	for _, it := range c.items {
		if it.Kind != "user" {
			continue
		}
		number++
		if it.ID == id {
			return number, it.Text, true
		}
	}
	return 0, "", false
}

// Busy reports whether a turn of the agent's runs, or its session is being
// replaced: no time to put its files back.
func (m *Manager) Busy(a state.Agent) bool {
	c := m.existing(a.Ref())
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn != nil || c.rolling
}

// Checkpointed tells the app that a turn's checkpoint was taken.
func (m *Manager) Checkpointed(a state.Agent, turn string) {
	c := m.existing(a.Ref())
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID[turn] != nil {
		c.emit(api.ChatEvent{Checkpoint: turn})
	}
}

// UpTo returns copies of the conversation's items up to the end of the turn
// headed by the user message turn; all of them for "".
func (m *Manager) UpTo(a state.Agent, turn string) ([]api.ChatItem, error) {
	c, err := m.conversation(a)
	if err != nil {
		return nil, err
	}
	defer c.mu.Unlock()
	end, err := c.turnEnd(turn)
	if err != nil {
		return nil, err
	}
	out := make([]api.ChatItem, 0, end)
	for _, it := range c.items[:end] {
		out = append(out, clone(*it))
	}
	return out, nil
}

// turnEnd is the index just past the last item of turn: where the next turn's
// user message is, or the end. The conversation is locked.
func (c *conversation) turnEnd(turn string) (int, error) {
	if turn == "" {
		return len(c.items), nil
	}
	start := -1
	for i, it := range c.items {
		if it.ID == turn && it.Kind == "user" {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, fmt.Errorf("%s's conversation has no such turn any more", c.agent.Ref())
	}
	for i := start + 1; i < len(c.items); i++ {
		if c.items[i].Kind == "user" {
			return i, nil
		}
	}
	return len(c.items), nil
}

// Rewind cuts the conversation back to the end of a turn, and restarts its
// session: the items after the turn go, the session is forgotten, and the
// next turn's prompt starts with a context transfer of what is left, intro
// first. notice is what the conversation says happened, on an item that also
// shows the transfer. The worktree is the caller's to put back.
func (m *Manager) Rewind(a state.Agent, turn, intro, notice string) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	if c.turn != nil || c.rolling {
		return fmt.Errorf("%s: %w", a.Ref(), ErrTurnRunning)
	}
	end, err := c.turnEnd(turn)
	if err != nil {
		return err
	}
	c.stopAdapter()
	for _, it := range c.items[end:] {
		delete(c.byID, it.ID)
		delete(c.dirty, it.ID)
		delete(c.unsaved, it.ID)
	}
	c.items = c.items[:end:end]
	kept := c.appends[:0]
	for _, ap := range c.appends {
		if c.byID[ap.ID] != nil {
			kept = append(kept, ap)
		}
	}
	c.appends = kept
	if err := m.Store.TruncateChatItems(context.Background(), a.Project, a.Name, int64(end)); err != nil {
		return err
	}
	c.outbox, c.compaction = nil, nil
	c.tools, c.plan, c.open, c.openMessage = map[string]*api.ChatItem{}, nil, nil, ""
	c.clearLimit()
	handoff := Handoff(c.items, intro)
	c.stored.SessionID, c.stored.Handoff = "", handoff
	if err := m.Store.SaveChat(context.Background(), a.Project, a.Name, c.stored); err != nil {
		return err
	}
	c.session.TurnStartedAt, c.session.ContextUsed, c.session.ContextSize = nil, 0, 0
	c.session.Commands = []api.ChatCommand{}
	c.session.State = c.stateNow()
	c.emit(api.ChatEvent{After: c.items[end-1].ID})
	it := c.add("notice", turn)
	it.Text, it.Handoff = notice, handoff
	c.markSession()
	c.flush(true)
	return nil
}

// Seed starts a new agent's conversation as a copy of items from src's, the
// pictures sent with them included, and has its first session told handoff.
// notice marks where the copy ends. The agent must not have chatted yet.
func (m *Manager) Seed(a, src state.Agent, items []api.ChatItem, handoff, notice string) error {
	c, err := m.conversation(a)
	if err != nil {
		return err
	}
	defer c.mu.Unlock()
	if len(c.items) > 0 {
		return fmt.Errorf("%s has a conversation already", a.Ref())
	}
	turn := ""
	for i := range items {
		it := clone(items[i])
		if len(it.Images) > 0 {
			m.copyImages(src, a, it.Images)
		}
		c.items = append(c.items, &it)
		c.byID[it.ID] = &it
		c.unsaved[it.ID] = true
		if it.Kind == "user" {
			turn = it.ID
		}
	}
	c.stored.SessionID, c.stored.Handoff = "", handoff
	if err := m.Store.SaveChat(context.Background(), a.Project, a.Name, c.stored); err != nil {
		return err
	}
	it := c.add("notice", turn)
	it.Text, it.Handoff = notice, handoff
	c.flush(true)
	return nil
}

func (m *Manager) copyImages(from, to state.Agent, images []api.ChatImage) {
	src, dst := m.imageDir(from), m.imageDir(to)
	if src == "" || dst == "" {
		return
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return
	}
	for _, img := range images {
		if !imageID.MatchString(img.ID) {
			continue
		}
		if err := copyFile(filepath.Join(src, img.ID), filepath.Join(dst, img.ID)); err != nil {
			m.logf("chat %s: copying a picture from %s: %v", to.Ref(), from.Ref(), err)
		}
	}
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Handoff writes a conversation out for a session that never saw it: intro,
// then each turn's message, what was done, and the answer it ended on. Past
// handoffBudget the first turn — usually the task — stays, and the turns after
// it give way to the latest.
func Handoff(items []*api.ChatItem, intro string) string {
	type turn struct {
		user    *api.ChatItem
		asides  []string
		work    []*api.ChatItem
		answer  string
		subwork int
	}
	var turns []*turn
	for _, it := range items {
		if it.Kind == "user" {
			turns = append(turns, &turn{user: it})
			continue
		}
		if len(turns) == 0 {
			continue
		}
		t := turns[len(turns)-1]
		switch {
		case it.Parent != "":
			t.subwork++
		case it.Kind == "aside":
			t.asides = append(t.asides, it.Text)
		case it.Kind == "tool":
			t.work = append(t.work, it)
		case it.Kind == "assistant" && strings.TrimSpace(it.Text) != "":
			t.answer = it.Text
		}
	}
	blocks := make([]string, len(turns))
	for i, t := range turns {
		var b strings.Builder
		if t.user.Woken {
			// Nobody wrote it: the session started it, when background work ended.
			fmt.Fprintf(&b, "### Turn %d\n\nNo message: background work you had left running ended (%s), and you carried on by yourself.\n", i+1, headText(strings.ReplaceAll(cmp(strings.TrimSpace(t.user.Text), "a background task"), "\n", ", "), 1000))
		} else {
			fmt.Fprintf(&b, "### Turn %d\n\nUser: %s\n", i+1, headText(strings.TrimSpace(t.user.Text), 4000))
		}
		for _, aside := range t.asides {
			fmt.Fprintf(&b, "\nUser, while you worked: %s\n", headText(strings.TrimSpace(aside), 2000))
		}
		if did := Activity(t.work); did != "" {
			fmt.Fprintf(&b, "\nYou %s.\n", lowerFirst(did))
		}
		if t.answer != "" {
			fmt.Fprintf(&b, "\nYou answered: %s\n", headText(strings.TrimSpace(t.answer), 4000))
		}
		if t.user.Result != nil && t.user.Result.State != "completed" {
			fmt.Fprintf(&b, "\n(This turn %s.)\n", t.user.Result.State)
		}
		blocks[i] = b.String()
	}
	header := "[AgentBox: context transfer] " + intro + "\n\n" +
		"This is the conversation so far, written out because this session didn't take part in it. " +
		"Carry on from its end: the message after this one is the user's next.\n\n"
	size := len(header)
	for _, b := range blocks {
		size += len(b) + 1
	}
	if size > handoffBudget && len(blocks) > 2 {
		// The first turn, then as many of the latest as fit.
		room := handoffBudget - len(header) - len(blocks[0]) - 100
		from := len(blocks)
		for from > 1 && room-len(blocks[from-1]) > 0 {
			from--
			room -= len(blocks[from]) + 1
		}
		if from > 1 {
			left := fmt.Sprintf("(Turns 2 to %d are left out for length.)\n", from)
			if from == 2 {
				left = "(Turn 2 is left out for length.)\n"
			}
			blocks = append([]string{blocks[0], left}, blocks[from:]...)
		}
	}
	return header + strings.Join(blocks, "\n")
}

// Activity says what a run of tool calls did, the way the app's collapsed
// log does: "Ran 4 commands, edited 3 files and read 6 files".
func Activity(items []*api.ChatItem) string {
	var commands, reads, searches, fetches, others int
	edited := map[string]bool{}
	for _, it := range items {
		if it.Tool == nil {
			continue
		}
		switch it.Tool.Kind {
		case "execute":
			commands++
		case "read":
			reads++
		case "edit", "delete", "move":
			paths := it.Tool.Paths
			if len(paths) == 0 {
				paths = []string{it.Tool.Title}
			}
			for _, p := range paths {
				edited[p] = true
			}
		case "search":
			searches++
		case "fetch":
			fetches++
		default:
			others++
		}
	}
	var parts []string
	add := func(ok bool, text string) {
		if ok {
			parts = append(parts, text)
		}
	}
	add(commands > 0, "ran "+count(commands, "command"))
	add(len(edited) > 0, "edited "+count(len(edited), "file"))
	add(reads > 0, "read "+count(reads, "file"))
	add(searches > 0, "searched "+times(searches))
	add(fetches > 0, "fetched "+count(fetches, "page"))
	add(others > 0, "used "+count(others, "tool"))
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return upperFirst(parts[0])
	}
	return upperFirst(strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1])
}

func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return strconv.Itoa(n) + " times"
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
