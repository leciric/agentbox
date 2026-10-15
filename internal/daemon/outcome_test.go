package daemon

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

func ended(text string) chat.TurnEnd {
	return chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "completed", StopReason: "end_turn"}, LastMessage: text}
}

// judgeTurn places a turn's end from the turn alone; the cases are the ones
// that were got wrong, and the ones next to them that must stay right.
func TestJudgeTurn(t *testing.T) {
	t.Parallel()
	failed := func(err string) chat.TurnEnd {
		return chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Error: err}
	}
	withOpen := ended("Started the migration.")
	withOpen.Open = []string{"npm run migrate"}
	background := ended("CI is running; I'll report when it finishes.")
	background.Background = []string{"Watch CI run 42"}
	cases := []struct {
		name string
		end  chat.TurnEnd
		want verdict
	}{
		// Asking, in its own words.
		{"a plain question", ended("The page is built.\n\nWhat do you want me to do next: launch, change, or stop?"), verdictAsking},
		{"a question over a list of options", ended("Two ways to do it. Which do you want?\n\n1. Paginate\n2. Infinite scroll"), verdictAsking},
		{"a question in bold", ended("**Should I go ahead with the migration?**"), verdictAsking},
		{"a plan waiting for approval", ended("Here's my plan:\n\n- add the table\n- backfill\n\nShall I proceed?"), verdictAsking},
		{"a question inside code doesn't count", ended("Done. The check reads:\n\n```\nif ok?\n```"), verdictEnded},
		{"perhaps asking, without a question mark", ended("I'll wait for your go before I deploy."), verdictUnsure},
		{"perhaps asking, a question mid-paragraph", ended("Was the cache stale? It was. Fixed in abc123."), verdictUnsure},
		{"a courtesy line is for the model to place", ended("Opened PR #12. Let me know if you need anything else."), verdictUnsure},
		// Finished, done or idle by what it has to show.
		{"a report", ended("Opened PR #12: the reminders page, with tests."), verdictEnded},
		{"nothing said", ended(""), verdictEnded},
		// Waiting on work.
		{"background work it reports", background, verdictBackground},
		{"a promise with nothing to keep it (Codex, OpenCode)", ended("CI is running; I'll report when it finishes."), verdictPromise},
		{"waiting on the build", ended("Pushed. Waiting for the build to finish."), verdictPromise},
		{"left running in the background", ended("The load test is running in the background."), verdictPromise},
		{"a tool call still open as the turn ended", withOpen, verdictPromise},
		// Stopped.
		{"a websocket that dropped mid-turn", failed("stream error: WebSocket closed with code 1006"), verdictFailed},
		{"the AI tool exiting", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Lost: true, Error: "connection closed"}, verdictFailed},
		{"the output limit", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "completed", StopReason: "max_tokens"}}, verdictFailed},
		{"a refusal", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "completed", StopReason: "refusal"}}, verdictFailed},
		{"cancelled", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "cancelled", StopReason: "cancelled"}}, verdictCancelled},
		// Still working, whatever it said.
		{"another turn already running", func() chat.TurnEnd { e := ended("Done?"); e.Continues = true; return e }(), verdictWorking},
		{"a usage limit is a pause", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Limited: true}, verdictPaused},
		{"a usage limit with another turn running", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Limited: true, Continues: true}, verdictWorking},
		{"AgentBox stopping", chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Shutdown: true}, verdictWorking},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, _ := judgeTurn(c.end); got != c.want {
				t.Errorf("judgeTurn(%q) = %s, want %s", c.end.LastMessage, got, c.want)
			}
		})
	}
	if _, note := judgeTurn(failed("stream error: WebSocket closed with code 1006\nat ...")); note != "stream error: WebSocket closed with code 1006" {
		t.Errorf("a failure's note is %q, want its first line", note)
	}
	if q := question(closingWords("Built it.\n\nWhat do you want me to do next: launch, change, or stop?")); q != "What do you want me to do next: launch, change, or stop?" {
		t.Errorf("question = %q", q)
	}
}

// An agent that ended its turn on a plain question, without the question
// tool, is asking, not finished: the lead is told what it asked and still
// waits for its finish, and the rail shows it asking.
func TestAnAgentEndingOnAPlainQuestionIsAsking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Landing page")
	const q = "What do you want me to do next: launch, change, or stop?"
	saidLast(t, d, a, "The landing page builds and the preview is up.\n\n"+q)

	d.srv.leadAsked(a)
	d.srv.agentFinished(a, chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "completed", StopReason: "end_turn"}})
	notice := noticeTo(t, d, lead)
	if !strings.Contains(notice, "ended its turn on a question") || !strings.Contains(notice, q) {
		t.Errorf("the lead was told %q", notice)
	}
	waitFor(t, "the lead to take up the question", func() bool {
		state := d.srv.chat.State(lead.Ref())
		return state != "" && state != api.ChatOff && state != api.ChatReady
	})
	if !d.srv.leadIsWaiting(a) {
		t.Error("the lead stopped waiting on an agent that only asked a question")
	}
	info := d.srv.agentInfo(agent.Status{Agent: a, State: "running"})
	if info.Outcome != api.AgentOutcomeAsking || info.OutcomeNote != q {
		t.Errorf("the agent's outcome is %q (%q), want asking with its question", info.Outcome, info.OutcomeNote)
	}
	kinds := eventKinds(t, d, a.Project)
	if len(kinds) != 1 || kinds[0] != api.AgentAsking {
		t.Errorf("events = %q, want one asking and no finish", kinds)
	}
}

// Closing words the rules can't place go to the cheap model, once, with
// nothing but those words; its answer decides between asking and finished.
func TestTheCheapModelPlacesAnUnclearEnding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Look into the flaky test")
	a.AI = "claude"
	var mu sync.Mutex
	var asked []string
	answer := "ASKING"
	d.srv.askAside = func(_ context.Context, _ state.Agent, model, ask string) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, model+": "+ask)
		return answer, model, nil
	}
	d.srv.agentFinished(a, ended("The test is flaky because of the clock. I'll wait for your go before I change the fixture."))
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeAsking {
		t.Errorf("with the model saying ASKING, the outcome is %q", o.kind)
	}
	mu.Lock()
	if len(asked) != 1 || !strings.HasPrefix(asked[0], state.CheapModelFor("claude")+": ") || !strings.Contains(asked[0], "wait for your go") {
		t.Errorf("the model was asked %q: want once, on the cheap model, with the closing words only", asked)
	}
	answer = "DONE."
	mu.Unlock()

	d.srv.agentFinished(a, ended("Found it: the clock. Let me know if you need anything else."))
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeIdle {
		t.Errorf("with the model saying DONE and nothing to show, the outcome is %q, want idle", o.kind)
	}

	// A plain report never reaches the model.
	d.srv.agentFinished(a, ended("Found it: the clock was mocked twice."))
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 2 {
		t.Errorf("the model was asked %d times, want 2: a plain report isn't worth a look", len(asked))
	}
}

// A turn that dies mid-way — a dropped websocket (1006), its AI tool exiting —
// is the agent stopped on an error. The lead hears it from the one place
// that judges every turn's end, not a notice of its own, and only once.
func TestATurnThatDiesMidWayIsStoppedWithAnError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var told []string
	d.srv.stallTell = func(_ context.Context, _ string, note string) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, note)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	d.srv.leadAsked(a)
	d.srv.agentFinished(a, chat.TurnEnd{
		ChatTurnResult: api.ChatTurnResult{State: "failed"},
		Error:          "API Error: WebSocket closed with code 1006 (abnormal closure)",
	})
	mu.Lock()
	if len(told) != 1 || !strings.Contains(told[0], "stopped in the middle of its turn") || !strings.Contains(told[0], "1006") {
		t.Errorf("the lead was told %q", told)
	}
	mu.Unlock()
	info := d.srv.agentInfo(agent.Status{Agent: a, State: "running"})
	if info.Outcome != api.AgentOutcomeFailed || !strings.Contains(info.OutcomeNote, "1006") {
		t.Errorf("the agent's outcome is %q (%q), want failed with the error", info.Outcome, info.OutcomeNote)
	}
	if kinds := eventKinds(t, d, a.Project); len(kinds) != 1 || kinds[0] != api.AgentFailed {
		t.Errorf("events = %q, want one failure", kinds)
	}
	if !d.srv.leadIsWaiting(a) {
		t.Error("a failure stopped the lead waiting for the agent's finish")
	}

	// Its AI tool exits mid-turn: the turn fails on the closed output, and
	// agentLost says so with the exit status. One notice, not two.
	d.srv.agentFinished(a, chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "failed"}, Lost: true, Error: "connection closed"})
	d.srv.agentLost(a, "Claude Code exited (exit status 137)")
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 2 || !strings.Contains(told[1], "exit status 137") {
		t.Errorf("the lead was told %q; want one more notice, with the exit status", told)
	}
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.kind != api.AgentOutcomeFailed || !strings.Contains(o.note, "137") {
		t.Errorf("after its tool exited, the outcome is %+v", o)
	}
}

// sayTool is an AI tool that answers each prompt with what it was given to
// say, then ends its turn: a lead's turns say nothing.
type sayTool struct {
	mu      sync.Mutex
	replies []string // said in order; the last one again once they run out
	prompts []string
}

func (s *sayTool) launch(_ context.Context, a state.Agent, _ func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	h := &sayHandler{tool: s, lead: a.IsLead()}
	h.conn = acp.NewConn(toTool, fromTool, h)
	return &chat.Process{
		Stdin: fromChat, Stdout: toChat,
		Stop: func() {
			once.Do(func() {
				_ = fromTool.Close()
				_ = toTool.Close()
				close(exited)
			})
		},
		Wait:   func() error { <-exited; return nil },
		Stderr: func() string { return "" },
	}, nil
}

func (s *sayTool) got(text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.prompts {
		if strings.Contains(p, text) {
			n++
		}
	}
	return n
}

type sayHandler struct {
	tool *sayTool
	lead bool
	conn *acp.Conn
}

func (h *sayHandler) Notify(string, json.RawMessage) {}

func (h *sayHandler) Request(method string, params json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionPrompt:
		var text string
		if !h.lead {
			h.tool.mu.Lock()
			h.tool.prompts = append(h.tool.prompts, string(params))
			if len(h.tool.replies) > 0 {
				text = h.tool.replies[0]
				if len(h.tool.replies) > 1 {
					h.tool.replies = h.tool.replies[1:]
				}
			}
			h.tool.mu.Unlock()
		}
		if text != "" {
			chunk, _ := json.Marshal(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
			_ = h.conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "s1", "update": json.RawMessage(chunk)})
		}
		reply(map[string]any{"stopReason": "end_turn"}, nil)
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

// An agent that ends its turn promising to report on work nothing will wake
// it for — what Codex and OpenCode do, having no background tasks to report —
// is told once to wait for the work inside a turn. Ending the same way again,
// it is idle, and the lead hears that nothing will wake it.
func TestAPromiseNothingWillKeepIsCaught(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Deploy started; I'll report back when it finishes."}}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Deploy the preview")
	d.srv.leadAsked(a)
	if _, err := d.client.SendChat(ctx, a.Ref(), "Deploy the preview."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to be told to wait on its work", func() bool { return tool.got("Nothing wakes you") == 1 })
	notice := noticeTo(t, d, lead)
	if !strings.Contains(notice, "nothing will wake it") {
		t.Errorf("the lead was told %q", notice)
	}
	waitFor(t, "the agent's outcome", func() bool {
		return d.srv.outcomeOf(ctx, a.Project, a.Ref()).kind == api.AgentOutcomeIdle
	})
	if n := tool.got("Nothing wakes you"); n != 1 {
		t.Errorf("the agent was told to wait %d times, want once", n)
	}
	if kinds := eventKinds(t, d, a.Project); len(kinds) != 0 {
		t.Errorf("events = %q; a promise isn't a finish", kinds)
	}
}

// After a restart, the turn carried on that ends on a plan waiting for
// approval is asking, and the lead — whose waiting the old daemon took with
// it — hears it and takes it up.
func TestAResumedTurnEndingOnAPlanIsAsking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{
		"Built the first half.",
		"Picking up where I left off. Plan for the rest:\n\n- the API\n- the page\n\nShall I go ahead?",
	}}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	res, err := d.client.SendChat(ctx, a.Ref(), "Build the reminders page.")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first turn to end", func() bool {
		return d.srv.outcomeOf(ctx, a.Project, a.Ref()).kind == api.AgentOutcomeIdle
	})
	// That turn is now one a restart cut short, and the daemon that knew
	// the lead waited on it is gone.
	if err := d.srv.store.SaveRunningTurn(ctx, state.RunningTurn{Project: a.Project, Agent: a.Name, Kind: state.TurnOfAgent,
		Turn: res.ID, Prompt: "Build the reminders page.", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d.srv.mu.Lock()
	clear(d.srv.leadWaits)
	d.srv.mu.Unlock()

	d.srv.continueTurns(ctx)
	waitFor(t, "the carried-on turn to be judged", func() bool {
		return d.srv.outcomeOf(ctx, a.Project, a.Ref()).kind == api.AgentOutcomeAsking
	})
	if o := d.srv.outcomeOf(ctx, a.Project, a.Ref()); o.note != "Shall I go ahead?" {
		t.Errorf("the question is %q", o.note)
	}
	// The first turn's "finished" notice reaches the lead too, and
	// noticeTo answers the moment any notice exists: wait for the one that
	// carries the question instead of racing it.
	waitFor(t, "the lead to be told the question", func() bool {
		th, err := d.srv.chat.Thread(lead)
		if err != nil {
			return false
		}
		for _, it := range th.Items {
			if it.Kind == "notice" && strings.Contains(it.Text, "Shall I go ahead?") {
				return true
			}
		}
		return false
	})
	if !d.srv.leadIsWaiting(a) {
		t.Error("the lead isn't waiting on the agent whose turn was carried on")
	}
}

// After a restart, the rail reads each agent's outcome from the events the
// last daemon recorded.
func TestOutcomesAreReadBackFromEvents(t *testing.T) {
	t.Parallel()
	at := time.Now()
	for _, c := range []struct {
		ev   api.AgentEvent
		want agentOutcome
		says bool
	}{
		{api.AgentEvent{Kind: api.AgentFinished, Outcome: api.AgentOutcomeDone}, agentOutcome{kind: api.AgentOutcomeDone}, true},
		{api.AgentEvent{Kind: api.AgentFinished, PR: &api.PullRequest{Number: 3}}, agentOutcome{kind: api.AgentOutcomeDone}, true},
		{api.AgentEvent{Kind: api.AgentFinished}, agentOutcome{kind: api.AgentOutcomeIdle}, true},
		{api.AgentEvent{Kind: api.AgentAsking, Summary: "Which?"}, agentOutcome{kind: api.AgentOutcomeAsking, note: "Which?"}, true},
		{api.AgentEvent{Kind: api.AgentChatLost, Summary: "exited"}, agentOutcome{kind: api.AgentOutcomeFailed, note: "exited"}, true},
		{api.AgentEvent{Kind: api.AgentLimited, Summary: "Paused"}, agentOutcome{kind: api.AgentOutcomeLimited, note: "Paused"}, true},
		{api.AgentEvent{Kind: api.AgentLimited, Summary: "Paused", ResumeAt: &at}, agentOutcome{kind: api.AgentOutcomeLimited, note: "Paused", until: at}, true},
		{api.AgentEvent{Kind: api.AgentCreated}, agentOutcome{}, true},
		{api.AgentEvent{Kind: api.AgentStalled, At: at}, agentOutcome{}, false},
	} {
		if got, says := outcomeOfEvent(c.ev); got != c.want || says != c.says {
			t.Errorf("outcomeOfEvent(%s) = %+v, %t; want %+v, %t", c.ev.Kind, got, says, c.want, c.says)
		}
	}
}

func eventKinds(t *testing.T, d testDaemon, project string) []string {
	t.Helper()
	rows, err := d.srv.store.AgentEvents(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, row := range rows {
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) == nil && ev.Kind != api.AgentCreated {
			out = append(out, ev.Kind)
		}
	}
	return out
}

// A usage limit is a pause: no finished event, so no review item and no chime,
// a distinct outcome with the time it resumes at, and one short notice for the
// lead however many times the limit is met again.
func TestAUsageLimitPausesTheAgentRatherThanFinishingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var told []string
	d.srv.stallTell = func(_ context.Context, _ string, note string) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, note)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	// It had finished once before, with work to show.
	d.srv.record(ctx, api.AgentEvent{Project: a.Project, Agent: a.Name, Ref: a.Ref(), Kind: api.AgentFinished, Outcome: api.AgentOutcomeDone, At: time.Now().Add(-time.Hour)})

	resume := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	end := chat.TurnEnd{
		ChatTurnResult: api.ChatTurnResult{State: "failed"},
		Limited:        true, ResumeAt: resume,
		Error: `Internal error: You've hit your session limit · resets 4:10pm (UTC): {"errorKind":"rate_limit"}`,
	}
	d.srv.agentFinished(a, end)
	d.srv.agentFinished(a, end) // the same limit again: nothing new to say

	info := d.srv.agentInfo(agent.Status{Agent: a, State: "running"})
	if info.Outcome != api.AgentOutcomeLimited || info.OutcomeUntil == nil || !info.OutcomeUntil.Equal(resume) {
		t.Errorf("outcome %q until %v, want limited until %v", info.Outcome, info.OutcomeUntil, resume)
	}
	if !strings.HasPrefix(info.OutcomeNote, "Paused: usage limit, resumes at ") {
		t.Errorf("outcome note %q", info.OutcomeNote)
	}
	kinds := eventKinds(t, d, a.Project)
	if len(kinds) != 2 || kinds[0] != api.AgentLimited {
		t.Errorf("events = %q, want one limited after the earlier finish", kinds)
	}
	if _, ok := d.srv.finishOf(ctx, a); ok {
		t.Error("an agent paused on a usage limit is ready to review on its earlier finish")
	}
	// No failure notice for the lead: a pause isn't one.
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 0 {
		t.Errorf("the lead was told %q through the stall path", told)
	}
}

func TestResumeNowRefusesAnAgentThatIsNotPaused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	if err := d.srv.resumeLimited(a); err == nil {
		t.Error("Resume now on an agent that isn't paused succeeded")
	}
}

func TestPausedNotes(t *testing.T) {
	t.Parallel()
	if got := pausedNote(time.Time{}); got != "Paused: usage limit" {
		t.Errorf("no time: %q", got)
	}
	at := time.Date(2026, 10, 15, 16, 11, 0, 0, time.Local)
	if got := pausedNote(at); got != "Paused: usage limit, resumes at 16:11" {
		t.Errorf("with a time: %q", got)
	}
}
