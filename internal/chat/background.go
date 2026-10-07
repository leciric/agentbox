package chat

import (
	"errors"
	"slices"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What a session does between turns.
//
// Claude Code can leave work running after its turn ends: a command run in
// the background, a Monitor. When one finishes, the CLI wakes the session by
// itself, and the model carries on in a turn nobody prompted — reading the CI
// run it was watching, reporting it. claude-agent-acp streams that turn like
// any other, outside any session/prompt, and ends it with the usage_update
// that carries its cost. Such a turn is woken: it heads its own user item
// (api.ChatItem.Woken), its text and tool calls are kept, and it ends through
// finishTurn, so the lead hears of it as of any other.
//
// The work itself is reported by the adapter as async tasks, to a client that
// declares the asyncTasks capability (acp.SubagentSessionsMeta): spawned when
// a command is backgrounded or a monitor starts, a state update when it ends.
// While any runs the session isn't done, whatever its turn says
// (api.ChatSession.Background).

// wokenQuiet is how long a woken turn may go without a word from the adapter,
// with none of its tool calls running, before it is taken to have ended. The
// adapter ends one with a usage_update; this is for one that never came.
const wokenQuiet = 10 * time.Minute

// bgTask is one of an adapter's background tasks.
type bgTask struct {
	description string
	done        bool
}

// taskUpdate takes an async_task_* update. The conversation is locked.
func (c *conversation) taskUpdate(ad *adapter, u acp.SessionUpdate) {
	if u.AsyncTaskID == "" || ad.replaying {
		// Replay restores tasks whose process is gone with the old adapter.
		return
	}
	if ad.tasks == nil {
		ad.tasks = map[string]*bgTask{}
	}
	t := ad.tasks[u.AsyncTaskID]
	if t == nil {
		t = &bgTask{}
		ad.tasks[u.AsyncTaskID] = t
	}
	if d := firstLine(cmp(u.Description, u.Name)); d != "" {
		t.description = d
	}
	switch u.State {
	case "completed", "failed", "stopped":
		if !t.done {
			t.done = true
			// What woke the session, for the woken turn that follows.
			ad.woke = append(ad.woke, t.taskName())
		}
	case "running", "paused":
		t.done = false
	}
	c.syncBackground(ad)
}

func (t *bgTask) taskName() string {
	if t.description == "" {
		return "a background task"
	}
	return t.description
}

// background is what the adapter has running, by description.
func (ad *adapter) background() []string {
	var names []string
	for _, t := range ad.tasks {
		if !t.done {
			names = append(names, t.taskName())
		}
	}
	slices.Sort(names)
	return names
}

// syncBackground shows what the adapter has running on the session. The
// conversation is locked.
func (c *conversation) syncBackground(ad *adapter) {
	var now []string
	if ad != nil && c.adapter == ad {
		now = ad.background()
	}
	if slices.Equal(now, c.session.Background) {
		return
	}
	c.session.Background = now
	c.markSession()
	c.flush(true)
}

// Background is what an agent's session has running in the background, by
// description: nothing, when it has no session.
func (m *Manager) Background(ref string) []string {
	c := m.existing(ref)
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.session.Background)
}

// wakes says whether an update that arrived with no turn running is the
// session starting one by itself: something the model said or did. Only
// claude-agent-acp does that, and only it ends one in a way this can see.
func (c *conversation) wakes(ad *adapter, u acp.SessionUpdate) bool {
	if c.turn != nil || c.rolling || c.capture != nil || ad.replaying || !ad.ready || c.agent.AI != "claude" {
		return false
	}
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk":
		return strings.TrimSpace(u.Text()) != ""
	case "tool_call", "plan":
		return true
	}
	// A tool_call_update for a call no turn has open is a late word on an
	// ended turn's call — a backgrounded command's card — not a new turn.
	return false
}

// wake starts the turn the session started by itself. The conversation is
// locked and no turn is running.
func (c *conversation) wake(ad *adapter) {
	it := c.add("user", "")
	it.Turn, it.Woken = it.ID, true
	it.Text = strings.Join(ad.woke, "\n")
	ad.woke = nil
	// What the turn reacts to never passes through AgentBox, so there is no
	// prompt to send, and none to send again after a restart (recordTurn).
	t := &turn{id: it.ID, woken: true, startedAt: it.CreatedAt, progressAt: it.CreatedAt}
	c.endLimit()
	c.turn = t
	c.tools, c.plan, c.open, c.openMessage = map[string]*api.ChatItem{}, nil, nil, ""
	started := it.CreatedAt
	c.session.TurnStartedAt = &started
	c.session.State = c.stateNow()
	c.markSession()
	c.flush(true)
	c.watchWoken(ad, t)
}

// endWoken ends a woken turn on the usage_update that closes it. The
// conversation is locked.
func (c *conversation) endWoken(ad *adapter, t *turn) {
	c.book(ad, state.TokensBackground, t.id, nil, generationMS(t))
	reason := "end_turn"
	if t.cancelled {
		reason = "cancelled"
	}
	c.finishTurn(t, &acp.PromptResponse{StopReason: reason}, nil)
}

// watchWoken ends t if the adapter goes quiet for wokenQuiet with none of its
// tool calls running: a woken turn has no prompt call to answer when it ends.
func (c *conversation) watchWoken(ad *adapter, t *turn) {
	time.AfterFunc(wokenQuiet, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.turn != t || c.adapter != ad {
			return
		}
		quiet := time.Since(t.progressAt) >= wokenQuiet
		for _, it := range c.tools {
			if it.Tool.Status == "pending" || it.Tool.Status == "in_progress" {
				quiet = false
			}
		}
		if !quiet {
			c.watchWoken(ad, t)
			return
		}
		c.m.logf("chat %s: the turn its session started by itself went quiet for %s; taking it as ended", c.agent.Ref(), wokenQuiet)
		c.finishTurn(t, &acp.PromptResponse{StopReason: "end_turn"}, nil)
	})
}

// adapterGone ends what only an adapter's own updates could have ended: its
// background tasks, which went with its process, and a woken turn, which has
// no prompt call to fail. The conversation is locked and c.adapter is no
// longer the adapter that went.
func (c *conversation) adapterGone(why error) {
	c.syncBackground(nil)
	if t := c.turn; t != nil && t.woken {
		if t.cancelled {
			c.finishTurn(t, &acp.PromptResponse{StopReason: "cancelled"}, nil)
		} else {
			c.finishTurn(t, nil, why)
		}
	}
}

var errAdapterStopped = errors.New("the session was stopped")
