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

// bgTool is an AI tool whose every turn leaves a command running in the
// background, as claude-agent-acp reports it to a client that declares the
// asyncTasks capability, and ends. finish ends the command.
type bgTool struct {
	mu   sync.Mutex
	conn *acp.Conn
}

func (b *bgTool) launch(context.Context, state.Agent, func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	b.mu.Lock()
	b.conn = acp.NewConn(toTool, fromTool, b)
	b.mu.Unlock()
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

func (b *bgTool) say(update string) {
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	_ = conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "s1", "update": json.RawMessage(update)})
}

func (b *bgTool) finish() {
	b.say(`{"sessionUpdate":"async_task_state_update","asyncTaskId":"b1","state":"completed"}`)
}

func (b *bgTool) Notify(string, json.RawMessage) {}

func (b *bgTool) Request(method string, _ json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionPrompt:
		b.say(`{"sessionUpdate":"async_task_spawned","asyncTaskId":"b1","name":"Bash","taskType":"local_bash","description":"Watch CI run 42"}`)
		reply(map[string]any{"stopReason": "end_turn"}, nil)
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

// watchInBackground has a's chat run one turn of bgTool's, and waits until
// its session reports the command it left running.
func watchInBackground(t *testing.T, d testDaemon, a state.Agent) *bgTool {
	t.Helper()
	tool := &bgTool{}
	d.srv.chat.Launch, d.srv.chat.Prepare = tool.launch, nil
	if _, err := d.srv.chat.Send(a, "watch CI and report"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the background command to be reported", func() bool {
		return len(d.srv.chat.Background(a.Ref())) == 1 && d.srv.chat.State(a.Ref()) == api.ChatReady
	})
	return tool
}

// An agent whose turn ended while a command it started runs on in the
// background is still working: auto-stop leaves its machine alone, however
// long it has been since its turn, until the command ends.
func TestAutoStopLeavesAnAgentWaitingOnBackgroundWork(t *testing.T) {
	t.Parallel()
	now := time.Now()
	d, a := newAutoStopIdleTest(t, "Running", now.Add(-3*time.Hour))
	setAutoStopIdle(t, d, true, 2*time.Hour)
	a.AI = "claude"
	tool := watchInBackground(t, d, a)

	later := now.Add(3 * time.Hour)
	d.srv.stopIdleAgents(context.Background(), later)
	if got := instanceStatus(t, d); got != "Running" {
		t.Fatalf("an agent waiting on its CI watch was stopped: instance = %q", got)
	}

	tool.finish()
	waitFor(t, "the command to end", func() bool { return len(d.srv.chat.Background(a.Ref())) == 0 })
	d.srv.stopIdleAgents(context.Background(), later)
	if got := instanceStatus(t, d); got != "Stopped" {
		t.Errorf("once its command ended, the idle agent wasn't stopped: instance = %q", got)
	}
}

// TestRetireWaitsOnBackgroundWork: retire_agent leaves an agent waiting on background work alone, saying why,
// unless told to force it.
func TestRetireWaitsOnBackgroundWork(t *testing.T) {
	t.Parallel()
	d, a := newAutoStopIdleTest(t, "Running", time.Now())
	a.AI = "claude"
	watchInBackground(t, d, a)
	ctx := context.Background()

	res, err := d.client.Retire(ctx, a.Project, api.RetireRequest{Agents: []string{a.Name}, How: api.RetireStop})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retired) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "Watch CI run 42") {
		t.Fatalf("retiring an agent waiting on its CI watch: %+v", res)
	}
	t.Logf("left alone: %s", res.Skipped[0].Reason)

	res, err = d.client.Retire(ctx, a.Project, api.RetireRequest{Agents: []string{a.Name}, How: api.RetireStop, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retired) != 1 {
		t.Errorf("forcing it didn't retire it: %+v", res)
	}
}

// A turn that ends with background work running isn't the agent finishing:
// the lead is told it is waiting, without a turn of its own, and still waits
// for the finish — which, when the work ends and the agent reports, is news
// it acts on.
func TestAnAgentWaitingOnBackgroundWorkIsNotReportedFinished(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Watch CI")
	saidLast(t, d, a, "CI is running; I'll report when it finishes.")

	d.srv.leadAsked(a)
	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn", Background: []string{"Watch CI run 42"}})
	notice := noticeTo(t, d, lead)
	if !strings.Contains(notice, "isn't finished") || !strings.Contains(notice, "Watch CI run 42") {
		t.Errorf("the lead was told %q", notice)
	}
	if events, _ := d.srv.store.AgentEvents(ctx, a.Project); len(events) != 0 {
		t.Errorf("a finish was recorded for an agent that is still waiting: %d events", len(events))
	}
	if state := d.srv.chat.State(lead.Ref()); state != api.ChatOff && state != api.ChatReady && state != "" {
		t.Errorf("the lead's chat is %q: a notice that it waits started a turn", state)
	}
	// A monitor wakes the session at every line; each of those turns ends
	// waiting too, and the lead isn't told again.
	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn", Background: []string{"Watch CI run 42"}})
	if th, _ := d.srv.chat.Thread(lead); countNotices(th) != 1 {
		t.Errorf("the lead was told %d times that the agent waits", countNotices(th))
	}
	d.srv.mu.Lock()
	waits := d.srv.leadWaits[a.Ref()]
	d.srv.mu.Unlock()
	if !waits {
		t.Fatal("the lead stopped waiting on an agent that hasn't finished")
	}

	// The command ends, the session wakes, and its turn ends clean.
	saidLast(t, d, a, "CI passed: all 14 jobs green.")
	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})
	waitFor(t, "the lead to react to the finish", func() bool {
		state := d.srv.chat.State(lead.Ref())
		return state != "" && state != api.ChatOff && state != api.ChatReady
	})
	if !strings.Contains(noticeTo(t, d, lead), "CI passed") {
		t.Errorf("the finish notice doesn't carry the follow-up: %q", noticeTo(t, d, lead))
	}
}

func countNotices(th api.ChatThread) int {
	n := 0
	for _, it := range th.Items {
		if it.Kind == "notice" {
			n++
		}
	}
	return n
}

// The transitions an agent goes through when its turn ends on background
// work: awaiting (not idle, not finished, no finish event) while the work
// runs; working when its end wakes the session; finished and idle once the
// follow-up turn ends with nothing left running.
func TestAwaitingUntilFollowUpEnds(t *testing.T) {
	t.Parallel()
	d, a := newAutoStopIdleTest(t, "Running", time.Now())
	a.AI = "claude"
	tool := watchInBackground(t, d, a)
	ctx := context.Background()
	status := agent.Status{Agent: a, State: "running"}
	finishes := func() int {
		events, err := d.srv.store.AgentEvents(ctx, a.Project)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, raw := range events {
			var ev api.AgentEvent
			if json.Unmarshal(raw.Data, &ev) == nil && ev.Kind == api.AgentFinished {
				n++
			}
		}
		return n
	}

	// Awaiting.
	info := d.srv.agentInfo(status)
	if info.Chat != api.ChatReady || len(info.Background) != 1 || info.Background[0] != "Watch CI run 42" {
		t.Fatalf("an agent waiting on its watch is %q with background %q", info.Chat, info.Background)
	}
	if busy, idle, _, _ := d.srv.idleOf(ctx, status, api.AgentChanges{}); !busy || idle {
		t.Errorf("an awaiting agent is busy %t, idle %t", busy, idle)
	}
	if n := finishes(); n != 0 {
		t.Errorf("%d finish event(s) for an agent that is awaiting", n)
	}

	// The watch ends and wakes it.
	tool.finish()
	tool.say(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"CI passed."}}`)
	waitFor(t, "the woken turn", func() bool { return d.srv.chat.State(a.Ref()) == api.ChatRunning })
	if info := d.srv.agentInfo(status); len(info.Background) != 0 {
		t.Errorf("the ended watch is still listed: %q", info.Background)
	}

	// Its follow-up ends: finished, idle.
	tool.say(`{"sessionUpdate":"usage_update","used":2,"size":200000,"cost":{"amount":0.1,"currency":"USD"}}`)
	waitFor(t, "the follow-up's finish", func() bool { return finishes() == 1 })
	if busy, idle, _, _ := d.srv.idleOf(ctx, status, api.AgentChanges{}); busy || !idle {
		t.Errorf("after its follow-up, the agent is busy %t, idle %t", busy, idle)
	}
}
