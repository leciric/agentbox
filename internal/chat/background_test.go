package chat

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// watchingTurn is a turn that starts a CI watch in the background and ends
// on a promise, the way Claude Code does with Bash's run_in_background: the
// command's card completes at once, and the adapter reports the command
// itself as an async task that is still running.
func watchingTurn(f *fakeTool, s, _ string) acp.PromptResponse {
	f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"bash-1","title":"gh run watch 42 --exit-status","kind":"execute","status":"in_progress"}`)
	f.update(s, `{"sessionUpdate":"async_task_spawned","asyncTaskId":"b1","name":"Bash","taskType":"local_bash","description":"Watch CI run 42","toolCallId":"bash-1"}`)
	f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"bash-1","status":"completed"}`)
	f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CI is running; I'll report when it finishes."}}`)
	f.update(s, `{"sessionUpdate":"usage_update","used":1000,"size":200000,"cost":{"amount":0.10,"currency":"USD"}}`)
	return acp.PromptResponse{StopReason: "end_turn"}
}

// finishes records what Finished is told, in order.
type finishes struct {
	mu   sync.Mutex
	list []api.ChatTurnResult
}

func (f *finishes) add(_ state.Agent, r api.ChatTurnResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = append(f.list, r)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *finishes) all() []api.ChatTurnResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.list)
}

// The bug this file fixes: an agent watching CI in the background ended its
// turn on "I'll report when it finishes", the watch ended and woke its
// session, and what the session said then was dropped — never shown, never
// stored, never reported. Now the turn it ends on knows the watch is still
// running, and the watch ending starts a turn of its own that is shown,
// stored and finished like any other.
func TestABackgroundTaskEndingWakesATurnThatIsShownStoredAndFinished(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(watchingTurn)
	m, _ := newManager(t, store, f)
	var fin finishes
	m.Finished = fin.add

	if _, err := m.Send(testAgent, "watch CI and tell me"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the first turn to end", turnsEnded(1))
	first := find(th, "user", 0)
	if first.Result == nil || !slices.Equal(first.Result.Background, []string{"Watch CI run 42"}) {
		t.Fatalf("the turn ended with %+v, want it to say the watch still runs", first.Result)
	}
	if !slices.Equal(th.Session.Background, []string{"Watch CI run 42"}) || !slices.Equal(m.Background(testAgent.Ref()), th.Session.Background) {
		t.Fatalf("the session's background = %q, Background() = %q", th.Session.Background, m.Background(testAgent.Ref()))
	}

	// The watch ends, and the session carries on by itself.
	f.update("session-1", `{"sessionUpdate":"async_task_state_update","asyncTaskId":"b1","state":"completed","summary":"exit 0"}`)
	f.update("session-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CI passed: all 14 jobs green."}}`)
	f.update("session-1", `{"sessionUpdate":"tool_call","toolCallId":"bash-2","title":"gh pr view 7","kind":"execute","status":"in_progress"}`)
	f.update("session-1", `{"sessionUpdate":"tool_call_update","toolCallId":"bash-2","status":"completed"}`)
	th = waitThread(t, m, testAgent, "the woken turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if len(th.Session.Background) != 0 {
		t.Errorf("the finished watch is still in the background: %q", th.Session.Background)
	}
	f.update("session-1", `{"sessionUpdate":"usage_update","used":2000,"size":200000,"cost":{"amount":0.25,"currency":"USD"},"_meta":{"_claude/origin":{"kind":"task-notification"}}}`)
	th = waitThread(t, m, testAgent, "the woken turn to end", turnsEnded(2))

	woken := find(th, "user", 1)
	if !woken.Woken || woken.Text != "Watch CI run 42" {
		t.Fatalf("the second turn's head = %+v, want a woken turn naming the watch", woken)
	}
	if woken.Result == nil || woken.Result.State != "completed" || woken.Result.StopReason != "end_turn" || len(woken.Result.Background) != 0 {
		t.Errorf("the woken turn ended %+v", woken.Result)
	}
	var said, ran bool
	for _, it := range th.Items {
		if it.Turn != woken.ID {
			continue
		}
		said = said || (it.Kind == "assistant" && it.Text == "CI passed: all 14 jobs green.")
		ran = ran || (it.Kind == "tool" && it.Tool.Title == "gh pr view 7" && it.Tool.Status == "completed")
	}
	if !said || !ran {
		t.Errorf("the woken turn lost its text (%t) or its tool call (%t): %s", said, ran, summary(th))
	}
	if got := m.LastMessage(testAgent); got != "CI passed: all 14 jobs green." {
		t.Errorf("LastMessage = %q, want the follow-up, which is what the lead is told", got)
	}

	// Both turns reached Finished: the first with the watch still running, so
	// the daemon doesn't call it a finish; the second clean.
	waitFor(t, "both turns to be reported", func() bool { return len(fin.all()) == 2 })
	if got := fin.all(); len(got[0].Background) != 1 || len(got[1].Background) != 0 || got[1].State != "completed" {
		t.Errorf("Finished was told %+v", got)
	}

	// Stored, not just shown: the woken turn and its answer are rows.
	rows, err := store.ChatItems(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, row := range rows {
		var it api.ChatItem
		if json.Unmarshal(row.Data, &it) == nil {
			stored[it.Kind+":"+it.Text] = it.Woken || stored[it.Kind+":"+it.Text]
			if it.Kind == "assistant" && it.Text == "CI passed: all 14 jobs green." {
				stored["answer"] = true
			}
		}
	}
	if !stored["user:Watch CI run 42"] || !stored["answer"] {
		t.Errorf("the woken turn wasn't stored: %v", stored)
	}

	// And its cost went in the ledger as background work, under its turn.
	var booked bool
	for _, r := range ledger(t, store, 2) {
		booked = booked || (r.Kind == state.TokensBackground && r.Turn == woken.ID && near(r.CostUSD, 0.15))
	}
	if !booked {
		t.Errorf("the woken turn's cost wasn't booked under it: %+v", ledger(t, store, 2))
	}
}

// Stopping a woken turn ends it, though no prompt call is there to answer.
func TestAWokenTurnCanBeStopped(t *testing.T) {
	t.Parallel()
	f := newFakeTool(watchingTurn)
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "watch CI"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn to end", turnsEnded(1))
	f.update("session-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CI is red; looking at why."}}`)
	waitThread(t, m, testAgent, "the woken turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })

	if _, err := m.Cancel(testAgent); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the woken turn to end", turnsEnded(2))
	if r := find(th, "user", 1).Result; r.State != "cancelled" {
		t.Errorf("the stopped woken turn ended %+v", r)
	}
	if len(f.called(acp.MethodSessionCancel)) == 0 {
		t.Error("the tool was never told to stop")
	}
}

// A session that ends — stopped, or exited — takes its background tasks with
// it, and a woken turn with them: it has no prompt call that would fail.
func TestStoppingTheSessionEndsItsBackgroundAndItsWokenTurn(t *testing.T) {
	t.Parallel()
	f := newFakeTool(watchingTurn)
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "watch CI"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn to end", turnsEnded(1))
	f.update("session-1", `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"The watch printed something."}}`)
	waitThread(t, m, testAgent, "the woken turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })

	m.Stop(testAgent.Ref(), "the agent was stopped")
	th := waitThread(t, m, testAgent, "the woken turn to end", turnsEnded(2))
	if r := find(th, "user", 1).Result; r.State != "cancelled" || len(r.Background) != 0 {
		t.Errorf("the woken turn of a stopped session ended %+v", r)
	}
	if len(th.Session.Background) != 0 || len(m.Background(testAgent.Ref())) != 0 {
		t.Errorf("a stopped session still has background work: %q", th.Session.Background)
	}
}

// What doesn't wake a session: a late word on a call of a turn that has
// ended, a cost alone (TestWorkBetweenTurnsIsBooked), and anything from an
// adapter other than Claude Code's, which never ends such a turn.
func TestOnlyWordsOfItsOwnWakeASession(t *testing.T) {
	t.Parallel()
	f := newFakeTool(watchingTurn)
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "watch CI"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn to end", turnsEnded(1))
	f.update("session-1", `{"sessionUpdate":"tool_call_update","toolCallId":"bash-1","status":"completed"}`)
	f.update("session-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"   "}}`)
	// Something that would wake it, after, so the test knows the two above
	// were taken by the time it sees the turn this one starts.
	f.update("session-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Now."}}`)
	th := waitThread(t, m, testAgent, "the woken turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if n := len(slices.DeleteFunc(slices.Clone(th.Items), func(it api.ChatItem) bool { return it.Kind != "user" })); n != 2 {
		t.Errorf("%d turns, want the prompted one and one woken by the words: %s", n, summary(th))
	}

	codex := testAgent
	codex.Name, codex.AI = "agent-02", "codex"
	g := newFakeTool(answerHello)
	n, _ := newManager(t, openStore(t), g)
	if _, err := n.Send(codex, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, n, codex, "the turn to end", turnsEnded(1))
	g.update("session-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"a late chunk"}}`)
	g.update("session-1", `{"sessionUpdate":"usage_update","used":1,"size":2}`)
	waitThread(t, n, codex, "the usage to land", func(th api.ChatThread) bool { return th.Session.ContextUsed == 1 })
	if th, _ := n.Thread(codex); th.Session.State != api.ChatReady {
		t.Errorf("a stray update started a turn on codex, which would never end: %s", summary(th))
	}
}

// The project's chat wakes the same way: a lead watching a CI run itself
// gets its follow-up turn, and is idle again once it ends.
func TestALeadsSessionWakesToo(t *testing.T) {
	t.Parallel()
	f := newFakeTool(watchingTurn)
	m, _ := newManager(t, openStore(t), f)
	lead := state.Agent{Project: "hello", Name: state.LeadName, Role: state.RoleLead, AI: "claude", Worktree: "/work/hello"}
	idle := make(chan struct{}, 4)
	m.LeadIdle = func(state.Agent) { idle <- struct{}{} }
	if _, err := m.Send(lead, "watch the release"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, lead, "the first turn to end", turnsEnded(1))
	<-idle
	f.update("session-1", `{"sessionUpdate":"async_task_state_update","asyncTaskId":"b1","state":"failed"}`)
	f.update("session-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The release build failed."}}`)
	f.update("session-1", `{"sessionUpdate":"usage_update","used":2000,"size":200000,"cost":{"amount":0.2,"currency":"USD"}}`)
	th := waitThread(t, m, lead, "the woken turn to end", turnsEnded(2))
	if woken := find(th, "user", 1); !woken.Woken || woken.Hidden {
		t.Errorf("the lead's woken turn = %+v", woken)
	}
	<-idle
}
