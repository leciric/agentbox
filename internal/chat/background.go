package chat

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
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
// (api.ChatSession.Background). Each is also listed with what the adapter
// says of it (api.ChatSession.BackgroundTasks): its kind, how it ended, the
// file its output goes to, which BackgroundTask reads from the tool's
// machine. codex-acp reports its background terminals the same way, without
// a summary or a file. A subagent still running when its turn ends is
// listed too, pointing at its card.

// wokenQuiet is how long a woken turn may go without a word from the adapter,
// with none of its tool calls running, before it is taken to have ended. The
// adapter ends one with a usage_update; this is for one that never came.
const wokenQuiet = 10 * time.Minute

// bgTask is one of an adapter's background tasks.
type bgTask struct {
	id          string
	kind        string // api.BackgroundCommand, …
	description string
	// callID is the tool call that started it, and outputPath the file on
	// the tool's machine its output goes to, when the adapter said.
	callID, outputPath string
	state              string // the adapter's: running, paused, completed, failed or stopped
	summary            string
	done               bool
	startedAt, endedAt time.Time
	// tail is the end of its output as it was when it ended, starting at
	// byte tailStart of tailSize: what there is to show once the file is gone.
	tail                string
	tailStart, tailSize int64
}

// How much of a task's output one read gives, and how much it may ask for.
const (
	outputChunk    = 64 << 10
	outputChunkMax = 1 << 20
	// endedTasksKept is how many ended tasks a session goes on listing.
	endedTasksKept = 20
)

var exitCodeIn = regexp.MustCompile(`(?i)exit code:? (-?\d+)`)

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
		t = &bgTask{id: u.AsyncTaskID, kind: api.BackgroundOther, state: "running", startedAt: c.m.now()}
		ad.tasks[u.AsyncTaskID] = t
	}
	if d := firstLine(cmp(u.Description, u.Name)); d != "" {
		t.description = d
	}
	switch u.TaskType {
	case "":
	case "shell", "local_bash":
		t.kind = api.BackgroundCommand
	case "monitor", "local_monitor":
		t.kind = api.BackgroundMonitor
	default:
		t.kind = api.BackgroundOther
	}
	if u.ToolCallID != "" {
		t.callID = u.ToolCallID
	}
	if u.OutputFilePath != "" {
		t.outputPath = u.OutputFilePath
	}
	if u.Summary != "" {
		t.summary = u.Summary
	}
	switch u.State {
	case "completed", "failed", "stopped":
		t.state = u.State
		if !t.done {
			t.done = true
			t.endedAt = c.m.now()
			// What woke the session, for the woken turn that follows.
			ad.woke = append(ad.woke, t.taskName())
			c.keepTail(ad, t)
		}
	case "running", "paused":
		t.state, t.done, t.endedAt = u.State, false, time.Time{}
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

// callItem is the conversation's tool call with this call id, the agent's own
// or a subagent's: nil when it has none. The conversation is locked.
func (c *conversation) callItem(ad *adapter, callID string) *api.ChatItem {
	if callID == "" {
		return nil
	}
	if it := c.tools[callID]; it != nil {
		return it
	}
	for _, sa := range ad.subagents {
		if it := sa.tools[callID]; it != nil {
			return it
		}
	}
	// A call of a turn that ended: its command went into the background.
	for i := len(c.items) - 1; i >= 0 && i >= len(c.items)-2000; i-- {
		if it := c.items[i]; it.Tool != nil && it.Tool.CallID == callID {
			return it
		}
	}
	return nil
}

// task is t as the API gives it. The conversation is locked.
func (c *conversation) task(ad *adapter, t *bgTask) api.ChatBackgroundTask {
	out := api.ChatBackgroundTask{
		ID: t.id, Kind: t.kind, Title: t.taskName(), Summary: t.summary,
		StartedAt: t.startedAt, HasOutput: t.outputPath != "" || t.tail != "",
	}
	if it := c.callItem(ad, t.callID); it != nil {
		out.Item, out.Command = it.ID, it.Tool.Command
		out.HasOutput = out.HasOutput || it.Tool.Output != ""
	}
	switch {
	case !t.done:
		out.Status = api.BackgroundRunning
	case t.state == "completed":
		out.Status = api.BackgroundDone
	case t.state == "failed" && strings.Contains(strings.ToLower(t.summary), "timed out"):
		out.Status = api.BackgroundTimedOut
	case t.state == "failed":
		out.Status = api.BackgroundFailed
	default:
		out.Status = api.BackgroundStopped
	}
	if t.done {
		ended := t.endedAt
		out.EndedAt = &ended
		if m := exitCodeIn.FindStringSubmatch(t.summary); m != nil {
			if code, err := strconv.Atoi(m[1]); err == nil {
				out.ExitCode = &code
			}
		}
	}
	return out
}

// subagentTask is a subagent that outlived its turn, as a background task.
func subagentTask(sa *subagent) api.ChatBackgroundTask {
	card := sa.card
	out := api.ChatBackgroundTask{
		ID: "subagent-" + card.ID, Kind: api.BackgroundSubagent, Title: card.Subagent.Name,
		Command: card.Subagent.Task, StartedAt: card.CreatedAt, Item: card.ID,
	}
	switch card.Subagent.State {
	case subagentRunning:
		out.Status = api.BackgroundRunning
	case "completed":
		out.Status = api.BackgroundDone
	case "failed":
		out.Status = api.BackgroundFailed
	default: // cancelled, disconnected, stopped
		out.Status = api.BackgroundStopped
	}
	if out.Status != api.BackgroundRunning {
		ended := card.UpdatedAt
		if !sa.endedAt.IsZero() {
			ended = sa.endedAt
		}
		out.EndedAt = &ended
	}
	return out
}

// backgroundTasks is the adapter's background work item by item, oldest
// first: all that runs, and the last endedTasksKept that ended. The
// conversation is locked.
func (c *conversation) backgroundTasks(ad *adapter) []api.ChatBackgroundTask {
	var tasks []api.ChatBackgroundTask
	for _, t := range ad.tasks {
		tasks = append(tasks, c.task(ad, t))
	}
	for _, sa := range ad.subagents {
		if sa.background {
			tasks = append(tasks, subagentTask(sa))
		}
	}
	slices.SortFunc(tasks, func(a, b api.ChatBackgroundTask) int {
		if d := a.StartedAt.Compare(b.StartedAt); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	ended := 0
	for _, t := range tasks {
		if t.EndedAt != nil {
			ended++
		}
	}
	return slices.DeleteFunc(tasks, func(t api.ChatBackgroundTask) bool {
		if t.EndedAt == nil || ended <= endedTasksKept {
			return false
		}
		ended--
		return true
	})
}

// outliveTurn marks the subagents still running as a turn ends as background
// work: from here on they are what the session does between turns. The
// conversation is locked.
func (c *conversation) outliveTurn(ad *adapter) {
	for _, sa := range ad.subagents {
		if sa.card.Subagent.State == subagentRunning {
			sa.background = true
		}
	}
	c.syncBackground(ad)
}

// syncBackground shows what the adapter has running on the session. The
// conversation is locked.
func (c *conversation) syncBackground(ad *adapter) {
	var now []string
	var tasks []api.ChatBackgroundTask
	if ad != nil && c.adapter == ad {
		now, tasks = ad.background(), c.backgroundTasks(ad)
	}
	if slices.Equal(now, c.session.Background) && reflect.DeepEqual(tasks, c.session.BackgroundTasks) {
		return
	}
	c.session.Background, c.session.BackgroundTasks = now, tasks
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

// keepTail reads the end of an ended task's output while its file is still
// there, off the lock. The conversation is locked.
func (c *conversation) keepTail(ad *adapter, t *bgTask) {
	if t.outputPath == "" || c.m.ReadOutput == nil {
		return
	}
	agent, path := c.agent, t.outputPath
	c.m.background.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		data, start, size, err := c.m.ReadOutput(ctx, agent, path, 0, outputChunk)
		if err != nil {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		had := t.tail != ""
		t.tail, t.tailStart, t.tailSize = string(data), start, size
		if !had && t.tail != "" {
			c.syncBackground(ad) // HasOutput
		}
	})
}

// BackgroundTask is one of the background tasks of an agent's session, with
// a piece of its output: its last limit bytes, or the limit bytes that end
// at offset before. state.ErrNotFound when the session has no such task.
func (m *Manager) BackgroundTask(ctx context.Context, a state.Agent, id string, before, limit int64) (api.ChatBackgroundDetail, error) {
	if limit <= 0 {
		limit = outputChunk
	}
	limit = min(limit, outputChunkMax)
	var d api.ChatBackgroundDetail
	c := m.existing(a.Ref())
	if c == nil {
		return d, fmt.Errorf("%w: %s has no background task %q", state.ErrNotFound, a.Ref(), id)
	}
	c.mu.Lock()
	var path, tail, toolOutput string
	var tailStart, tailSize int64
	found := false
	if ad := c.adapter; ad != nil {
		if t := ad.tasks[id]; t != nil {
			found, d.Task = true, c.task(ad, t)
			path, tail, tailStart, tailSize = t.outputPath, t.tail, t.tailStart, t.tailSize
			if it := c.callItem(ad, t.callID); it != nil {
				toolOutput = it.Tool.Output
			}
		}
		for _, sa := range ad.subagents {
			if sa.background && "subagent-"+sa.card.ID == id {
				found, d.Task = true, subagentTask(sa)
			}
		}
	}
	c.mu.Unlock()
	if !found {
		return d, fmt.Errorf("%w: %s has no background task %q", state.ErrNotFound, a.Ref(), id)
	}
	switch {
	case path != "" && m.ReadOutput != nil:
		data, start, size, err := m.ReadOutput(ctx, a, path, before, limit)
		switch {
		case err == nil:
			d.Output, d.OutputStart, d.OutputSize = string(data), start, size
		case tail != "" && (before <= 0 || before >= tailSize):
			// The file went with the command; this is what was read as it ended.
			d.Output, d.OutputStart, d.OutputSize = tail, tailStart, tailSize
		default:
			d.OutputError = err.Error()
		}
	case toolOutput != "":
		// No file to read (codex-acp): what the chat kept of the command's
		// own card, which is its end.
		d.Output, d.OutputSize = toolOutput, int64(len(toolOutput))
	}
	d.Output = strings.ToValidUTF8(d.Output, "\uFFFD")
	return d, nil
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
	c.book(ad, state.TokensBackground, t.id, nil, generationMS(t), 0)
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

// TurnEnd is what Manager.Finished is told about an agent's turn as it
// ends: how it ended, and what the chat saw at that moment, which may not be
// there to read by the time the daemon looks.
type TurnEnd struct {
	api.ChatTurnResult
	// LastMessage is what the agent said last in the turn, its own words
	// rather than a subagent's (LastMessage).
	LastMessage string
	// Error is what a failed turn failed on.
	Error string
	// Open is the tool calls still running when the turn ended, by title.
	Open []string
	// Lost is a turn that failed because the AI tool went away mid-turn: its
	// Lost hook tells the rest.
	Lost bool
	// Limited is a turn that failed on a spent usage limit, which the chat
	// carries on by itself once the limit resets (limit.go).
	Limited bool
	// ResumeAt is when the chat carries a Limited turn on by itself: the
	// reset time and a minute. Zero when it won't, as when the setting is
	// off or it has waited enough times; Until is when the tool said the
	// limit resets, zero when it named no time.
	ResumeAt, Until time.Time
	// Shutdown is a turn AgentBox stopping cut short: the next daemon
	// carries it on (restart.go).
	Shutdown bool
	// Woken is a turn the session started by itself, as background work it
	// left ended.
	Woken bool
	// Continues is another turn already running after this one: a message
	// or a notice that waited on it.
	Continues bool
}
