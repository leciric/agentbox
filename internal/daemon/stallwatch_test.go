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

func TestStallFrom(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 9, 29, 17, 58, 0, 0, time.UTC)
	busy := last.Add(10 * time.Minute)
	for _, c := range []struct {
		name   string
		p      chat.Progress
		busyAt time.Time
		want   time.Time
	}{
		{"no tool call: the machine's CPU proves nothing", chat.Progress{LastProgress: last}, busy, last},
		{"a tool call, the machine working since", chat.Progress{LastProgress: last, ToolRunning: true}, busy, busy},
		{"a tool call, the machine never seen working", chat.Progress{LastProgress: last, ToolRunning: true}, time.Time{}, last},
		{"a tool call, the machine last worked before the adapter spoke", chat.Progress{LastProgress: busy, ToolRunning: true}, last, busy},
	} {
		if got := stallFrom(c.p, c.busyAt); !got.Equal(c.want) {
			t.Errorf("%s: stallFrom = %v, want %v", c.name, got, c.want)
		}
	}
}

// wedgedTool is an AI tool that starts a session, takes a prompt, says what
// it is told to, and then never answers: a turn as the daemon sees it when
// the agent's machine locks up mid-task.
type wedgedTool struct {
	mu    sync.Mutex
	first map[string]string // by agent: what it says as its turn starts
	conns map[string]*acp.Conn
}

func (w *wedgedTool) launch(_ context.Context, a state.Agent, _ func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = fromTool.Close()
			_ = toTool.Close()
			close(exited)
		})
	}
	w.mu.Lock()
	conn := acp.NewConn(toTool, fromTool, wedgedHandler{w: w, agent: a.Name})
	w.conns[a.Name] = conn
	w.mu.Unlock()
	return &chat.Process{
		Stdin: fromChat, Stdout: toChat, Stop: stop,
		Wait:   func() error { <-exited; return nil },
		Stderr: func() string { return "" },
	}, nil
}

// say sends an agent's chat a session update.
func (w *wedgedTool) say(agent, update string) {
	w.mu.Lock()
	conn := w.conns[agent]
	w.mu.Unlock()
	_ = conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "s1", "update": json.RawMessage(update)})
}

type wedgedHandler struct {
	w     *wedgedTool
	agent string
}

func (h wedgedHandler) Notify(string, json.RawMessage) {}

func (h wedgedHandler) Request(method string, _ json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionPrompt:
		h.w.mu.Lock()
		first := h.w.first[h.agent]
		h.w.mu.Unlock()
		go h.w.say(h.agent, first) // and never a reply
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

// A turn that goes quiet is found stalled after stallAfter — unless a tool
// call of its keeps its machine working — the chat is told once, naming the
// agent, the agent shows it, and the mark comes off when the turn gets on.
// One on a paused machine never is.
func TestCheckStalls(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.srv.store.AddProject(ctx, state.Project{Name: "p", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tool := &wedgedTool{
		first: map[string]string{
			// Running the tests: a tool call, and its machine busy with it.
			"testing": `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"go test ./...","kind":"execute","status":"in_progress"}`,
			// Thinking, then nothing: its machine busy with something else.
			"thinking": `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Let me look"}}`,
			"paused":   `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Let me look"}}`,
		},
		conns: map[string]*acp.Conn{},
	}
	d.srv.chat.Launch, d.srv.chat.Prepare = tool.launch, nil

	var mu sync.Mutex
	cpu := map[string]time.Duration{}
	var told []string
	var clock time.Time // the chat's, once set; the real one until then
	d.srv.chat.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if clock.IsZero() {
			return time.Now()
		}
		return clock
	}
	d.srv.cpuTime = func(instance string) (time.Duration, bool) {
		mu.Lock()
		defer mu.Unlock()
		used, ok := cpu[instance]
		return used, ok
	}
	work := func(instance string, d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		cpu[instance] += d
	}
	d.srv.stallTell = func(_ context.Context, project, note string) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, project+": "+note)
	}
	tells := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), told...)
	}

	agents := map[string]state.Agent{}
	for _, name := range []string{"testing", "thinking", "paused"} {
		a := state.Agent{Project: "p", Name: name, Title: "Fix " + name, Instance: "ab-p-" + name, AI: "claude",
			Status: state.AgentReady, Worktree: t.TempDir(), CreatedAt: time.Now()}
		if err := d.srv.store.AddAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		work(a.Instance, 0)
		agents[name] = a
	}
	if err := d.srv.store.SetPausedAt(ctx, "p", "paused", time.Now()); err != nil {
		t.Fatal(err)
	}
	agents["paused"], _ = d.srv.store.Agent(ctx, "p", "paused")
	for _, a := range agents {
		if _, err := d.srv.chat.Send(a, "go"); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, ok := d.srv.chat.Progress("p/testing")
		q, _ := d.srv.chat.Progress("p/thinking")
		r, _ := d.srv.chat.Progress("p/paused")
		if ok && p.ToolRunning && q.LastProgress.After(q.StartedAt) && r.LastProgress.After(r.StartedAt) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turns never started: %+v %+v %+v", p, q, r)
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	at := func(d time.Duration) time.Time { return start.Add(d) }
	stalled := func(name string) bool {
		p, _ := d.srv.chat.Progress("p/" + name)
		return p.Stalled != nil
	}

	d.srv.checkStalls(ctx, at(0))
	// Both machines work for the next fourteen minutes, the tests using a core.
	for m := 1; m <= 14; m++ {
		work("ab-p-testing", time.Minute)
		work("ab-p-thinking", time.Minute)
		d.srv.checkStalls(ctx, at(time.Duration(m)*time.Minute))
	}
	if got := tells(); len(got) != 0 {
		t.Fatalf("told within fourteen minutes: %q", got)
	}
	// Past stallAfter since either adapter last spoke, the one with a tool
	// call running was working only two minutes ago; the other's working
	// machine says nothing about its stuck tool.
	d.srv.checkStalls(ctx, at(16*time.Minute))
	if stalled("testing") || !stalled("thinking") || stalled("paused") {
		t.Fatalf("stalled: testing %t, thinking %t, paused %t; want only thinking", stalled("testing"), stalled("thinking"), stalled("paused"))
	}
	got := tells()
	if len(got) != 1 || !strings.HasPrefix(got[0], `p: [stall] thinking ("Fix thinking") looks stuck`) || !strings.Contains(got[0], "its AI tool has sent nothing") {
		t.Fatalf("told %q", got)
	}
	info := d.srv.agentInfo(agent.Status{Agent: agents["thinking"], State: "running"})
	if info.Chat != api.ChatRunning || info.StalledSince == nil {
		t.Errorf("the stalled agent is %q, stalled since %v", info.Chat, info.StalledSince)
	}
	events, err := d.srv.store.AgentEvents(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range events {
		var e api.AgentEvent
		if json.Unmarshal(ev.Data, &e) == nil && e.Kind == api.AgentStalled {
			kinds = append(kinds, e.Agent+" "+e.Summary)
		}
	}
	if len(kinds) != 1 || !strings.HasPrefix(kinds[0], "thinking No progress for ") {
		t.Errorf("stall events %q", kinds)
	}

	// The tests go quiet too: stalled once stallAfter has passed since the
	// machine last worked, and the chat told once more, of the other.
	d.srv.checkStalls(ctx, at(20*time.Minute))
	d.srv.checkStalls(ctx, at(28*time.Minute))
	if stalled("testing") || len(tells()) != 1 {
		t.Fatalf("stalled too soon: %t, told %q", stalled("testing"), tells())
	}
	d.srv.checkStalls(ctx, at(30*time.Minute))
	if got := tells(); !stalled("testing") || len(got) != 2 || !strings.Contains(got[1], "a tool call is running, but its AI tool has sent nothing and its machine has done no work") {
		t.Fatalf("stalled %t, told %q", stalled("testing"), got)
	}
	d.srv.checkStalls(ctx, at(45*time.Minute))
	if got := tells(); len(got) != 2 {
		t.Errorf("told of the same stalls again: %q", got)
	}

	// The tool says something: the mark comes off at once, and the watch
	// leaves it off.
	mu.Lock()
	clock = at(46 * time.Minute)
	mu.Unlock()
	tool.say("thinking", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Found it"}}`)
	deadline = time.Now().Add(5 * time.Second)
	for stalled("thinking") {
		if time.Now().After(deadline) {
			t.Fatal("the mark didn't come off")
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.srv.checkStalls(ctx, at(47*time.Minute))
	if stalled("thinking") || !stalled("testing") {
		t.Errorf("stalled: thinking %t, testing %t", stalled("thinking"), stalled("testing"))
	}
}

// The chat is told of an agent whose AI tool exited mid-turn, and it is
// recorded where the app shows it.
func TestAgentLostTellsTheLead(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	var told []string
	d.srv.stallTell = func(_ context.Context, project, note string) { told = append(told, project+": "+note) }
	a := state.Agent{Project: "p", Name: "agent-12", Title: "Import the catalogue"}
	d.srv.agentLost(a, "Claude Code exited (exit status 143): ...killed.")
	if len(told) != 1 || !strings.HasPrefix(told[0], `p: [chat lost] agent-12 ("Import the catalogue") stopped in the middle of its turn: Claude Code exited (exit status 143)`) ||
		!strings.Contains(told[0], "tell_agent") {
		t.Errorf("told %q", told)
	}
}
