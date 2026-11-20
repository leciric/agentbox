package chat

import (
	"slices"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Timing (state/timing.go): where a turn's time goes. ACP carries no timing
// of its own, so the turn keeps its own clock on its updates as they
// arrive, under the lock that already holds for them: a few fields set per
// update, and a row queued for the store (state.Store.RecordTiming) as each
// tool call ends and as the turn does. Nothing here waits on the database.
//
// The model's time is what turn.generated already measures for the
// average TPS: from each request — the prompt, a tool's result — to the
// last token it streamed before handing over to a tool. Its wait for the
// first token is from the request to the first thing streamed. A tool
// call runs from when the tool says it does (in_progress), or else from
// the last the model wrote of it, to when it returned; a permission it
// waited for is in that. Calls in parallel overlap, so the turn's tool
// time is when at least one ran.

// maxToolDetail is how much of a tool call's title a timing row keeps.
const maxToolDetail = 200

// Tool call phases, as one update about it says (toolPhase).
const (
	toolQuiet   = iota // says nothing of who's working: progress, output
	toolWriting        // the model writes it: it opens, its title or input streams
	toolRunning        // the tool runs it
	toolDone           // it returned: completed, failed, or an Agent call's subagent did
)

// toolPhase is which phase a tool_call or tool_call_update says its call is
// in. claude-agent-acp opens a call as the model starts writing it and
// refines it as its input streams; codex-acp opens it as it runs.
func toolPhase(u acp.SessionUpdate) int {
	switch {
	case u.Status == "completed" || u.Status == "failed" || u.Returned():
		return toolDone
	case u.Status == "in_progress":
		return toolRunning
	case u.SessionUpdate == "tool_call" || len(u.RawInput) > 0 || (u.Title != nil && *u.Title != "") || u.Kind != "":
		return toolWriting
	}
	return toolQuiet
}

// toolClock times one tool call until it returns.
type toolClock struct {
	wrote time.Time // when the model last wrote it
	ran   time.Time // when the tool said it runs; zero until then
}

// from is when the call started running, as far as its updates say.
func (k *toolClock) from() time.Time {
	if !k.ran.IsZero() {
		return k.ran
	}
	return k.wrote
}

// span is a stretch of time a tool call ran.
type span struct{ from, to time.Time }

// clockTool times tool, which u just updated, keyed in clocks; open is
// whether it hadn't returned before u. Once it returns its row is queued,
// made by sub (a subagent's name, "" for the main loop) in turn, and the
// stretch it ran is returned. Caller holds c.mu.
func (c *conversation) clockTool(clocks map[string]*toolClock, tool *api.ChatTool, open bool, u acp.SessionUpdate, sub, turn string, now time.Time) (span, bool) {
	if !open {
		return span{}, false // it returned already, and was timed then
	}
	k := clocks[tool.CallID]
	if k == nil {
		k = &toolClock{wrote: now}
		clocks[tool.CallID] = k
	}
	switch toolPhase(u) {
	case toolDone:
		delete(clocks, tool.CallID)
		ran := span{k.from(), now}
		c.recordTool(tool, sub, turn, ran, tool.Status)
		return ran, true
	case toolRunning:
		if k.ran.IsZero() {
			k.ran = now
		}
	case toolWriting:
		if k.ran.IsZero() {
			k.wrote = now
		}
	}
	return span{}, false
}

// toolOpen reports whether a tool call hasn't returned.
func toolOpen(t *api.ChatTool) bool { return t.Status == "pending" || t.Status == "in_progress" }

// recordTool queues a tool call's timing row. Caller holds c.mu.
func (c *conversation) recordTool(tool *api.ChatTool, sub, turn string, ran span, status string) {
	c.m.recordTiming(state.TimingRow{
		Kind: state.TimingTool, Project: c.agent.Project, Agent: c.agent.Name, AgentID: c.agent.ID, AI: c.agent.AI,
		Turn: turn, Name: cmp(tool.Name, tool.Kind, "other"), Detail: headText(firstLine(tool.Title), maxToolDetail),
		Sub: sub, Status: status, At: ran.from, Wall: ran.to.Sub(ran.from),
	})
}

// stopClock ends the timing of a main-loop tool call still running as its
// turn ends at now. Caller holds c.mu.
func (c *conversation) stopClock(t *turn, tool *api.ChatTool, now time.Time) {
	k := t.clocks[tool.CallID]
	if k == nil {
		return
	}
	delete(t.clocks, tool.CallID)
	ran := span{k.from(), now}
	t.toolSpans = append(t.toolSpans, ran)
	c.recordTool(tool, "", t.id, ran, "stopped")
}

// timeTurn queues t's timing row: it ended at now, as ended says
// (api.ChatTurnResult.State). Caller holds c.mu.
func (c *conversation) timeTurn(t *turn, ended string, now time.Time) {
	if t.startedAt.IsZero() {
		return
	}
	row := turnTiming(t, now)
	row.Project, row.Agent, row.AgentID, row.AI = c.agent.Project, c.agent.Name, c.agent.ID, c.agent.AI
	row.Name, row.Status = optionValueOf(c.session.Options, "model"), ended
	c.m.recordTiming(row)
}

// turnTiming is t's timing row, as it ends at now: who ran it left out.
func turnTiming(t *turn, now time.Time) state.TimingRow {
	row := state.TimingRow{
		Kind: state.TimingTurn, Turn: t.id, At: t.startedAt, Wall: now.Sub(t.startedAt),
		Model: t.generated, FirstToken: t.firstToken, TTFT: t.ttft, ModelCalls: t.modelCalls,
		Tools: busy(t.toolSpans), ToolCalls: len(t.toolSpans),
	}
	switch {
	case !t.promptAt.IsZero():
		row.Start = t.promptAt.Sub(t.startedAt)
	case !t.woken:
		// Its prompt never went: it waited for the tool all along.
		row.Start = row.Wall
	}
	return row
}

// busy is how long at least one of spans ran.
func busy(spans []span) time.Duration {
	spans = slices.Clone(spans)
	slices.SortFunc(spans, func(a, b span) int { return a.from.Compare(b.from) })
	var total time.Duration
	var end time.Time
	for _, s := range spans {
		switch {
		case !s.to.After(s.from):
		case s.from.After(end):
			total += s.to.Sub(s.from)
			end = s.to
		case s.to.After(end):
			total += s.to.Sub(end)
			end = s.to
		}
	}
	return total
}

// recordTiming queues timing rows for the store, when there is one.
func (m *Manager) recordTiming(rows ...state.TimingRow) {
	if m.Store != nil {
		m.Store.RecordTiming(rows...)
	}
}
