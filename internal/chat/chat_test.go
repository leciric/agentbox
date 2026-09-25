package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// fakeTool is an ACP agent inside the test. It answers the chat's requests; a
// test's turn function sends updates and asks for permission.
type fakeTool struct {
	resume      bool // offers session/resume
	steering    bool // takes a message into a running turn, like the real adapters
	steerFails  bool // ...but the request errors
	steerIdle   bool // ...or answers that no turn was running
	resumeFails bool
	resumeBare  bool // ...and answers session/resume with no configOptions
	effort      bool // also offers an "effort" setting, like Claude Code's
	images      bool // says it takes images in a prompt, like claude-agent-acp
	plainOpus   bool // model choices are "opus", not "opus[1m]" — an account with no 1M-context entry
	// strictModel refuses a model that isn't literally one of its choices,
	// the way claude-agent-acp answers an unresolvable one ("Invalid value for
	// config option model: fable").
	strictModel bool
	// promptErr fails every turn with it, the way an adapter passes the
	// provider's own refusal back.
	promptErr *acp.Error

	mu       sync.Mutex
	turn     func(f *fakeTool, sessionID, text string) acp.PromptResponse
	conn     *acp.Conn
	stop     func()
	calls    []call
	sessions int
	values   map[string]string
	cancels  chan struct{}
	// steered is every message the running turn was given, in the order it
	// arrived, and steers wakes whoever waits for one.
	steered []string
	steers  chan string
}

type call struct {
	method string
	params json.RawMessage
}

func newFakeTool(turn func(f *fakeTool, sessionID, text string) acp.PromptResponse) *fakeTool {
	return &fakeTool{
		turn: turn, values: map[string]string{"mode": "default", "model": "sonnet"},
		cancels: make(chan struct{}, 4), steers: make(chan string, 8),
	}
}

// steering is a fake tool that takes messages into a running turn, as
// claude-agent-acp and codex-acp both do.
func newSteeringTool(turn func(f *fakeTool, sessionID, text string) acp.PromptResponse) *fakeTool {
	f := newFakeTool(turn)
	f.steering = true
	return f
}

func (f *fakeTool) launch(_ context.Context, _ state.Agent, status func(string)) (*Process, error) {
	status("Warming up")
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			fromTool.Close()
			toTool.Close()
			close(exited)
		})
	}
	f.mu.Lock()
	f.conn = acp.NewConn(toTool, fromTool, f)
	f.stop = stop
	f.mu.Unlock()
	return &Process{
		Stdin: fromChat, Stdout: toChat, Stop: stop,
		Wait:   func() error { <-exited; return nil },
		Stderr: func() string { return "starting\nfake tool: something broke\n" },
	}, nil
}

func (f *fakeTool) Request(method string, params json.RawMessage, reply func(any, error)) {
	f.mu.Lock()
	f.calls = append(f.calls, call{method, params})
	turn, promptErr := f.turn, f.promptErr
	f.mu.Unlock()
	switch method {
	case acp.MethodInitialize:
		sessionCaps := map[string]any{}
		if f.resume {
			sessionCaps["resume"] = map[string]any{}
		}
		res := map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{"loadSession": false, "sessionCapabilities": sessionCaps, "promptCapabilities": map[string]any{"image": f.images}},
			"agentInfo":         map[string]any{"name": "fake", "title": "Fake Tool", "version": "1.0"},
		}
		if f.steering {
			res["_meta"] = map[string]any{"steering": map[string]any{"supported": true}}
		}
		reply(res, nil)
	case acp.MethodSessionNew:
		f.mu.Lock()
		f.sessions++
		id := fmt.Sprintf("session-%d", f.sessions)
		f.mu.Unlock()
		reply(map[string]any{"sessionId": id, "configOptions": f.options()}, nil)
	case acp.MethodSessionResume:
		if f.resumeFails {
			reply(nil, &acp.Error{Code: -32002, Message: "no such session"})
			return
		}
		if f.resumeBare {
			reply(map[string]any{}, nil)
			return
		}
		reply(map[string]any{"configOptions": f.options()}, nil)
	case acp.MethodSetConfigOption:
		var req struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		json.Unmarshal(params, &req)
		if f.strictModel && req.ConfigID == "model" && !slices.ContainsFunc(f.options(), func(o map[string]any) bool {
			if o["id"] != "model" {
				return false
			}
			for _, c := range o["options"].([]map[string]any) {
				if c["value"] == req.Value {
					return true
				}
			}
			return false
		}) {
			reply(nil, &acp.Error{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"details":"Invalid value for config option model: ` + req.Value + `"}`)})
			return
		}
		f.mu.Lock()
		f.values[req.ConfigID] = req.Value
		f.mu.Unlock()
		reply(map[string]any{"configOptions": f.options()}, nil)
	case acp.MethodSessionPrompt:
		if promptErr != nil {
			reply(nil, promptErr)
			return
		}
		var req acp.PromptRequest
		json.Unmarshal(params, &req)
		go func() { reply(turn(f, req.SessionID, req.Prompt[0].Text), nil) }()
	case acp.MethodSessionSteer:
		if !f.steering {
			reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
			return
		}
		if f.steerFails {
			reply(nil, &acp.Error{Code: acp.CodeInternalError, Message: "the turn wouldn't take it"})
			return
		}
		if f.steerIdle {
			reply(acp.SteerResponse{Outcome: acp.SteerPromptRequired, Reason: "noRunningTurn"}, nil)
			return
		}
		var req acp.SteerRequest
		json.Unmarshal(params, &req)
		text := req.Prompt[0].Text
		f.mu.Lock()
		f.steered = append(f.steered, text)
		f.mu.Unlock()
		reply(acp.SteerResponse{Outcome: acp.SteerInjected}, nil)
		f.steers <- text
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

func (f *fakeTool) Notify(method string, params json.RawMessage) {
	f.mu.Lock()
	f.calls = append(f.calls, call{method, params})
	f.mu.Unlock()
	if method == acp.MethodSessionCancel {
		f.cancels <- struct{}{}
	}
}

func (f *fakeTool) options() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	modelChoices := []map[string]any{
		{"value": "sonnet", "name": "Sonnet"},
		{"value": "haiku", "name": "Haiku"},
	}
	if f.plainOpus {
		modelChoices = append(modelChoices, map[string]any{"value": "opus", "name": "Opus 5"})
	} else {
		modelChoices = append(modelChoices, map[string]any{"value": "opus[1m]", "name": "Opus 5", "description": "Opus 5 with 1M context"})
	}
	options := []map[string]any{
		{"id": "mode", "name": "Mode", "category": "mode", "type": "select", "currentValue": f.values["mode"], "options": []map[string]any{
			{"value": "default", "name": "Manual", "_meta": map[string]string{"kind": "standard"}},
			{"value": "bypassPermissions", "name": "Bypass permissions", "_meta": map[string]string{"kind": "full_access"}},
		}},
		{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": f.values["model"], "options": modelChoices},
	}
	if f.effort {
		options = append(options, map[string]any{"id": "effort", "name": "Effort", "category": "thought_level", "type": "select", "currentValue": f.values["effort"], "options": []map[string]any{
			{"value": "default", "name": "Default"},
			{"value": "high", "name": "High"},
			{"value": "xhigh", "name": "Xhigh"},
		}})
	}
	return options
}

func (f *fakeTool) connection() *acp.Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn
}

func (f *fakeTool) update(sessionID, update string) {
	f.connection().Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": sessionID, "update": json.RawMessage(update)})
}

// ask requests permission for a tool call and returns the option chosen, or "cancelled".
func (f *fakeTool) ask(sessionID, toolCall string) string {
	var out acp.RequestPermissionResponse
	err := f.connection().Call(context.Background(), acp.MethodRequestPermission, map[string]any{
		"sessionId": sessionID,
		"toolCall":  json.RawMessage(toolCall),
		"options": []map[string]string{
			{"optionId": "allow-once", "name": "Yes", "kind": "allow_once"},
			{"optionId": "reject", "name": "No", "kind": "reject_once"},
		},
	}, &out)
	switch {
	case err != nil:
		return "error: " + err.Error()
	case out.Outcome.Outcome == "cancelled":
		return "cancelled"
	}
	return out.Outcome.OptionID
}

// waitSteer waits for the next message steered into the running turn.
func (f *fakeTool) waitSteer(t *testing.T) string {
	t.Helper()
	select {
	case text := <-f.steers:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the running turn")
		return ""
	}
}

// steeredTexts is every message the running turn was given, in order, with the
// mid-turn framing stripped. Messages waiting together arrive as one steer, so
// this splits them apart again: what a test cares about is the messages and
// their order, not how many requests carried them.
func (f *fakeTool) steeredTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, text := range f.steered {
		if _, after, ok := strings.Cut(text, "]\n\n"); ok {
			text = after
		}
		out = append(out, strings.Split(text, "\n\n")...)
	}
	return out
}

// steerCount is how many steering requests carried them.
func (f *fakeTool) steerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.steered)
}

func (f *fakeTool) crash() {
	f.mu.Lock()
	stop := f.stop
	f.mu.Unlock()
	stop()
}

func (f *fakeTool) setTurn(turn func(f *fakeTool, sessionID, text string) acp.PromptResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turn = turn
}

func (f *fakeTool) called(method string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var params []json.RawMessage
	for _, c := range f.calls {
		if c.method == method {
			params = append(params, c.params)
		}
	}
	return params
}

func answerHello(f *fakeTool, sessionID, _ string) acp.PromptResponse {
	f.update(sessionID, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`)
	return acp.PromptResponse{StopReason: "end_turn"}
}

type recorded struct {
	mu     sync.Mutex
	events []api.ChatEvent
}

func (r *recorded) publish(ev api.ChatEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, clone(ev))
}

func (r *recorded) all() []api.ChatEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]api.ChatEvent(nil), r.events...)
}

var testAgent = state.Agent{Project: "hello", Name: "agent-01", AI: "claude", Worktree: "/work/hello"}

func openStore(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newManager(t *testing.T, store *state.Store, f *fakeTool) (*Manager, *recorded) {
	t.Helper()
	rec := &recorded{}
	m := &Manager{Store: store, Launch: f.launch, Publish: rec.publish, Version: "test"}
	t.Cleanup(m.Close)
	return m, rec
}

func waitThread(t *testing.T, m *Manager, a state.Agent, what string, cond func(api.ChatThread) bool) api.ChatThread {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		th, err := m.Thread(a)
		if err != nil {
			t.Fatal(err)
		}
		if cond(th) {
			return th
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %s", what, summary(th))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// turnsEnded waits for n turns to have ended.
func turnsEnded(n int) func(api.ChatThread) bool {
	return func(th api.ChatThread) bool {
		ended := 0
		for _, it := range th.Items {
			if it.Kind == "user" && it.Result != nil {
				ended++
			}
		}
		return ended >= n && th.Session.TurnStartedAt == nil
	}
}

func summary(th api.ChatThread) string {
	var parts []string
	for _, it := range th.Items {
		parts = append(parts, it.Kind)
	}
	return fmt.Sprintf("session %s %q, items %v", th.Session.State, th.Session.Error, parts)
}

func kinds(th api.ChatThread) []string {
	var out []string
	for _, it := range th.Items {
		out = append(out, it.Kind)
	}
	return out
}

func find(th api.ChatThread, kind string, n int) api.ChatItem {
	for _, it := range th.Items {
		if it.Kind == kind {
			if n == 0 {
				return it
			}
			n--
		}
	}
	return api.ChatItem{}
}

// replay rebuilds a thread from its events, the way the app does.
func replay(t *testing.T, events []api.ChatEvent) api.ChatThread {
	t.Helper()
	var th api.ChatThread
	for _, ev := range events {
		if ev.Seq != th.Seq+1 {
			t.Fatalf("event %d came after %d", ev.Seq, th.Seq)
		}
		th.Seq = ev.Seq
		switch {
		case ev.Cleared:
			th.Items = nil
		case ev.Session != nil:
			th.Session = *ev.Session
		case ev.Item != nil:
			if i := slices.IndexFunc(th.Items, func(it api.ChatItem) bool { return it.ID == ev.Item.ID }); i >= 0 {
				th.Items[i] = *ev.Item
			} else {
				th.Items = append(th.Items, *ev.Item)
			}
		case ev.Append != nil:
			i := slices.IndexFunc(th.Items, func(it api.ChatItem) bool { return it.ID == ev.Append.ID })
			if i < 0 {
				t.Fatalf("event %d adds text to an item that doesn't exist", ev.Seq)
			}
			th.Items[i].Text += ev.Append.Text
		}
	}
	return th
}

func sameJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s differ:\n got %s\nwant %s", what, g, w)
	}
}

func TestATurnWithToolCallsAndAPermissionRequest(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		if text != "Write hello.txt" {
			return acp.PromptResponse{StopReason: "refusal"}
		}
		f.update(s, `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"They want a file."}}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"\n\n"}}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"I'll write "}}`)
		f.update(s, `{"sessionUpdate":"plan","entries":[{"content":"Write hello.txt","priority":"high","status":"in_progress"}]}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"the file."}}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Preparing file…","kind":"edit","status":"pending","_meta":{"claudeCode":{"toolName":"Write"}}}`)
		answer := f.ask(s, `{"toolCallId":"t1","title":"Write hello.txt","kind":"edit","status":"pending",`+
			`"content":[{"type":"diff","path":"/work/hello/hello.txt","oldText":null,"newText":"hi\n"}],"locations":[{"path":"/work/hello/hello.txt"}]}`)
		if answer != "allow-once" {
			return acp.PromptResponse{StopReason: "refusal"}
		}
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","rawOutput":"File created successfully"}`)
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t2","title":"ls","kind":"execute","status":"in_progress","rawInput":{"command":["bash","-lc","ls -1"]}}`)
		f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t2","status":"completed","content":[{"type":"content","content":{"type":"text","text":"`+"```console\\nhello.txt\\n```"+`"}}]}`)
		f.update(s, `{"sessionUpdate":"plan","entries":[{"content":"Write hello.txt","priority":"high","status":"completed"}]}`)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m2","content":{"type":"text","text":"Done: hello.txt says hi."}}`)
		f.update(s, `{"sessionUpdate":"usage_update","used":1234,"size":200000}`)
		f.update(s, `{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"review","description":"Review the changes","input":{"hint":"[files]"}}]}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, rec := newManager(t, store, f)

	user, err := m.Send(testAgent, "  Write hello.txt ")
	if err != nil {
		t.Fatal(err)
	}
	if user.Kind != "user" || user.Text != "Write hello.txt" || user.Turn != user.ID || user.Result != nil {
		t.Errorf("Send() = %+v", user)
	}
	th := waitThread(t, m, testAgent, "the permission request", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	perm := find(th, "permission", 0)
	if perm.Permission.Title != "Write hello.txt" || len(perm.Permission.Options) != 2 || perm.Permission.Outcome != "" {
		t.Errorf("the permission request is %+v", perm.Permission)
	}
	if _, err := m.Answer(testAgent, perm.ID, "maybe"); err == nil {
		t.Error("an option the request doesn't offer was accepted")
	}
	if th, _ := m.Thread(testAgent); find(th, "permission", 0).Permission.Outcome != "" || th.Session.State != api.ChatWaiting {
		t.Errorf("a refused answer changed the request: %+v, session %s", find(th, "permission", 0).Permission, th.Session.State)
	}
	if _, err := m.Answer(testAgent, perm.ID, "allow-once"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Answer(testAgent, perm.ID, "allow-once"); err == nil {
		t.Error("a request was answered twice")
	}

	th = waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	want := []string{"user", "thought", "assistant", "plan", "tool", "permission", "tool", "assistant"}
	if got := kinds(th); !slices.Equal(got, want) {
		t.Fatalf("items %v, want %v", got, want)
	}
	if it := find(th, "thought", 0); it.Text != "They want a file." || it.Streaming {
		t.Errorf("thought = %+v", it)
	}
	if it := find(th, "assistant", 0); it.Text != "I'll write the file." || it.Streaming || it.Turn != user.ID {
		t.Errorf("first message = %+v", it)
	}
	if it := find(th, "assistant", 1); it.Text != "Done: hello.txt says hi." {
		t.Errorf("second message = %+v", it)
	}
	if plan := find(th, "plan", 0).Plan; len(plan) != 1 || plan[0].Status != "completed" {
		t.Errorf("plan = %+v", plan)
	}
	write := find(th, "tool", 0).Tool
	if write.Name != "Write" || write.Title != "Write hello.txt" || write.Kind != "edit" || write.Status != "completed" ||
		!slices.Equal(write.Paths, []string{"/work/hello/hello.txt"}) || write.Output != "File created successfully" {
		t.Errorf("write = %+v", write)
	}
	if len(write.Diffs) != 1 || !write.Diffs[0].Created || write.Diffs[0].NewText != "hi\n" {
		t.Errorf("write's diffs = %+v", write.Diffs)
	}
	if ls := find(th, "tool", 1).Tool; ls.Command != "ls -1" || ls.Output != "hello.txt" || ls.Status != "completed" {
		t.Errorf("ls = %+v", ls)
	}
	if outcome := find(th, "permission", 0).Permission.Outcome; outcome != "allow-once" {
		t.Errorf("the request's outcome is %q", outcome)
	}
	if r := th.Items[0].Result; r == nil || r.State != "completed" || r.StopReason != "end_turn" {
		t.Errorf("the turn ended with %+v", r)
	}
	s := th.Session
	if s.State != api.ChatReady || s.TurnStartedAt != nil || s.ContextUsed != 1234 || s.ContextSize != 200000 || s.Adapter != "Fake Tool 1.0" {
		t.Errorf("session = %+v", s)
	}
	// The adapter's mode and model, and AgentBox's own context window beside
	// the model: Sonnet has two (D91).
	if len(s.Options) != 3 || s.Options[2].ID != state.ChatOptionContextWindow || len(s.Commands) != 1 || s.Commands[0].Hint != "[files]" {
		t.Errorf("session options %+v, commands %+v", s.Options, s.Commands)
	}

	// What the app rebuilds from the events is the same thread.
	rebuilt := replay(t, rec.all())
	sameJSON(t, "items rebuilt from events", rebuilt.Items, th.Items)
	sameJSON(t, "session rebuilt from events", rebuilt.Session, th.Session)
	if rebuilt.Seq != th.Seq {
		t.Errorf("the thread is at event %d, the events end at %d", th.Seq, rebuilt.Seq)
	}

	// And another daemon reads the same conversation from the store.
	m.Close()
	other, _ := newManager(t, store, newFakeTool(answerHello))
	stored, err := other.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "stored items", stored.Items, th.Items)
	if stored.Session.State != api.ChatOff {
		t.Errorf("a new daemon's session is %s", stored.Session.State)
	}
}

func TestTheSessionIsResumedByTheNextAdapter(t *testing.T) {
	store := openStore(t)
	m, _ := newManager(t, store, newFakeTool(answerHello))
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	m.Close()

	second := newFakeTool(answerHello)
	second.resume = true
	m2, _ := newManager(t, store, second)
	if _, err := m2.Send(testAgent, "hi again"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m2, testAgent, "the second turn", turnsEnded(2))
	resumed := second.called(acp.MethodSessionResume)
	if len(resumed) != 1 || !strings.Contains(string(resumed[0]), `"sessionId":"session-1"`) || len(second.called(acp.MethodSessionNew)) != 0 {
		t.Errorf("resumed %s, started %d new sessions", resumed, len(second.called(acp.MethodSessionNew)))
	}
	if slices.Contains(kinds(th), "notice") {
		t.Errorf("a resumed session left a notice: %v", kinds(th))
	}
	m2.Close()

	// When resuming fails, a new session starts, and the conversation says so.
	third := newFakeTool(answerHello)
	third.resume, third.resumeFails = true, true
	m3, _ := newManager(t, store, third)
	if _, err := m3.Send(testAgent, "are you there?"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m3, testAgent, "the third turn", turnsEnded(3))
	notice := find(th, "notice", 0)
	if !strings.Contains(notice.Text, "doesn't remember the conversation above") || !strings.Contains(notice.Text, "no such session") {
		t.Errorf("notice = %q", notice.Text)
	}
	if len(third.called(acp.MethodSessionNew)) != 1 {
		t.Errorf("%d new sessions, want 1", len(third.called(acp.MethodSessionNew)))
	}
	if r := th.Items[len(th.Items)-1]; r.Kind != "assistant" || r.Text != "Hello" {
		t.Errorf("the last item is %+v", r)
	}
}

func TestCancelStopsTheTurn(t *testing.T) {
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"sleep 600","kind":"execute","status":"in_progress"}`)
		answer := make(chan string, 1)
		go func() { answer <- f.ask(s, `{"toolCallId":"t2","title":"rm -rf build","kind":"execute"}`) }()
		<-f.cancels
		if <-answer != "cancelled" {
			return acp.PromptResponse{StopReason: "end_turn"}
		}
		return acp.PromptResponse{StopReason: "cancelled"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "build it"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the permission request", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	if _, err := m.Cancel(testAgent); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if r := th.Items[0].Result; r.State != "cancelled" || r.StopReason != "cancelled" {
		t.Errorf("the turn ended with %+v", r)
	}
	if tool := find(th, "tool", 0).Tool; tool.Status != "stopped" {
		t.Errorf("the running tool call is %s", tool.Status)
	}
	if outcome := find(th, "permission", 0).Permission.Outcome; outcome != "cancelled" {
		t.Errorf("the permission request's outcome is %q", outcome)
	}
	if th.Session.State != api.ChatReady {
		t.Errorf("session state %s", th.Session.State)
	}
}

func TestTheToolExitingFailsTheTurn(t *testing.T) {
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Working on it"}}`)
		time.Sleep(2 * flushDelay)
		f.crash()
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", func(th api.ChatThread) bool {
		return turnsEnded(1)(th) && th.Session.State == api.ChatError
	})
	if r := th.Items[0].Result; r.State != "failed" {
		t.Errorf("the turn ended with %+v", r)
	}
	if e := find(th, "error", 0); !strings.Contains(e.Text, acp.ErrClosed.Error()) {
		t.Errorf("error = %q", e.Text)
	}
	if msg := find(th, "assistant", 0); msg.Text != "Working on it" || msg.Streaming {
		t.Errorf("message = %+v", msg)
	}
	if !strings.Contains(th.Session.Error, "fake tool: something broke") {
		t.Errorf("the session's error is %q", th.Session.Error)
	}

	// The next message starts the tool again, in a new session.
	f.setTurn(answerHello)
	if _, err := m.Send(testAgent, "try again"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if th.Session.State != api.ChatReady || th.Session.Error != "" {
		t.Errorf("session %s %q", th.Session.State, th.Session.Error)
	}
	if !strings.Contains(find(th, "notice", 0).Text, "can't resume sessions") {
		t.Errorf("items %v", kinds(th))
	}
}

func TestSettingsAreAppliedToEverySession(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	a.Autonomous = true
	m, _ := newManager(t, store, newFakeTool(answerHello))
	if _, err := m.SetOption(ctx, a, "model", "haiku"); err == nil {
		t.Error("a setting was accepted before the tool said what it offers")
	}
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if mode := optionValue(th.Session, "mode"); mode != "bypassPermissions" {
		t.Errorf("an autonomous agent's mode is %q", mode)
	}
	// A model the live menu doesn't list is no longer refused: it's stored for
	// the next session, because the tool resolves a model at launch against a
	// wider catalogue than it validates against mid-session (D45). What has to
	// hold is that it says when it takes effect rather than implying it's on.
	named, err := m.SetOption(ctx, a, "model", "claude-fable-5-1")
	if err != nil {
		t.Errorf("naming a model outside the menu = %v", err)
	}
	if got := optionValue(named, "model"); got != "claude-fable-5-1" {
		t.Errorf("after naming a model, the session reports %q", got)
	}
	th = waitThread(t, m, a, "the notice", func(th api.ChatThread) bool {
		return strings.Contains(find(th, "notice", 0).Text, "starts on it next time")
	})
	if !strings.Contains(find(th, "notice", 0).Text, "claude-fable-5-1") {
		t.Errorf("the notice doesn't name the model: %q", find(th, "notice", 0).Text)
	}
	// It still refuses a value for a setting that isn't the model.
	if _, err := m.SetOption(ctx, a, "mode", "nonsense"); err == nil {
		t.Error("a mode the tool doesn't offer was accepted")
	}
	session, err := m.SetOption(ctx, a, "model", "haiku")
	if err != nil || optionValue(session, "model") != "haiku" {
		t.Errorf("SetOption() = %v, %v", session.Options, err)
	}
	m.Close()

	next := newFakeTool(answerHello)
	m2, _ := newManager(t, store, next)
	if _, err := m2.Start(a); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m2, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if optionValue(th.Session, "model") != "haiku" || optionValue(th.Session, "mode") != "bypassPermissions" {
		t.Errorf("the next session's options are %+v", th.Session.Options)
	}
	if n := len(next.called(acp.MethodSetConfigOption)); n != 2 {
		t.Errorf("%d settings changed, want 2", n)
	}
}

// TestNewAgentStartsOnItsDefaults mirrors what agent.Create seeds a new Claude
// Code agent's stored options with, before it ever starts a session: full
// access, Opus, and the highest thinking effort. The model is stored the way
// agents made before D91 have it, "opus[1m]", which now reads as "opus".
func TestNewAgentStartsOnItsDefaults(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	a.Autonomous = true
	if err := store.SaveChat(ctx, a.Project, a.Name, state.Chat{Options: map[string]string{"model": "opus[1m]", "effort": "xhigh"}}); err != nil {
		t.Fatal(err)
	}
	f := newFakeTool(answerHello)
	f.effort = true
	m, _ := newManager(t, store, f)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if mode, model, effort := optionValue(th.Session, "mode"), optionValue(th.Session, "model"), optionValue(th.Session, "effort"); mode != "bypassPermissions" || model != "opus" || effort != "xhigh" {
		t.Errorf("a new agent's mode, model, effort = %q, %q, %q", mode, model, effort)
	}
}

// TestNewAgentAppliesModelDefaultWithoutAnExactChoice covers an account whose
// Claude Code plan doesn't offer the 1M-context "opus[1m]" entry, only plain
// "opus" (no separate 1M row at all): a real agent hit exactly this and kept
// running on Sonnet 5, because wanted() required the stored default to be a
// literal choice before ever asking the adapter, which resolves an alias like
// this itself. The agent still needs to end up asking for Opus 5, even though
// "opus[1m]" is never a choice this account offers.
func TestNewAgentAppliesModelDefaultWithoutAnExactChoice(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	a.Autonomous = true
	if err := store.SaveChat(ctx, a.Project, a.Name, state.Chat{Options: map[string]string{"model": "opus[1m]", "effort": "xhigh"}}); err != nil {
		t.Fatal(err)
	}
	f := newFakeTool(answerHello)
	f.effort = true
	f.plainOpus = true
	m, _ := newManager(t, store, f)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if model := optionValue(th.Session, "model"); model == "sonnet" {
		t.Errorf("a new agent whose account has no exact %q choice stayed on %q instead of asking the adapter for Opus 5", "opus[1m]", model)
	}
}

// TestModelThatWontApplyIsReported covers a stored model the account doesn't
// offer and the adapter won't resolve either — an entitlement that lapsed, or
// a preference carried over from another account. The turn still runs, on
// whatever the tool defaulted to, so the one thing that must not happen is
// silence: agents ran for weeks on Sonnet 5 while their menu said Opus,
// because a refused model was only ever written to the daemon's log.
func TestModelThatWontApplyIsReported(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	if err := store.SaveChat(ctx, a.Project, a.Name, state.Chat{Options: map[string]string{"model": "fable"}}); err != nil {
		t.Fatal(err)
	}
	f := newFakeTool(answerHello)
	f.strictModel = true
	m, _ := newManager(t, store, f)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the notice", func(th api.ChatThread) bool {
		return slices.ContainsFunc(th.Items, func(it api.ChatItem) bool { return it.Kind == "notice" && strings.Contains(it.Text, "fable") })
	})
	i := slices.IndexFunc(th.Items, func(it api.ChatItem) bool { return it.Kind == "notice" })
	notice := th.Items[i].Text
	if !strings.Contains(notice, "fable") {
		t.Errorf("the notice doesn't name the model that was refused: %q", notice)
	}
	// It must also name what is running instead, or it only says something is
	// wrong without saying what you actually got.
	if !strings.Contains(notice, "Sonnet") {
		t.Errorf("the notice doesn't name the model running instead: %q", notice)
	}
}

// TestSessionRemembersTheModelMenu checks the adapter's real model list is
// kept for the overview's default-model control. AgentBox never composes that
// list: it arrives over ACP and differs per account.
func TestSessionRemembersTheModelMenu(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })

	var raw string
	for range 50 {
		raw, _ = store.Setting(ctx, state.SettingClaudeModelChoices)
		if raw != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var choices []api.ChatOptionChoice
	if err := json.Unmarshal([]byte(raw), &choices); err != nil {
		t.Fatalf("remembered model menu = %q: %v", raw, err)
	}
	if !slices.ContainsFunc(choices, func(c api.ChatOptionChoice) bool { return c.Value == "sonnet" }) {
		t.Errorf("the remembered menu doesn't hold the tool's own choices: %+v", choices)
	}
}

// TestPinnedClaudeModelsAreOfferedButNeverRemembered checks AgentBox's small
// pinned list (state.MergePinnedClaudeModels) shows up on a running session's
// live model menu even though the adapter never advertised it, that picking
// one goes through the same "named outside the menu" notice as typing it
// would (D46 — the running adapter was never told about it), and that it
// never gets written into the remembered menu as though the adapter had sent
// it (rememberChoices).
func TestPinnedClaudeModelsAreOfferedButNeverRemembered(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })

	model, ok := findOption(th.Session, "model")
	if !ok {
		t.Fatal("no model option on a ready session")
	}
	if !slices.ContainsFunc(model.Choices, func(c api.ChatOptionChoice) bool { return c.Value == "claude-fable-5-1" }) {
		t.Errorf("Fable isn't on the live model menu: %+v", model.Choices)
	}

	session, err := m.SetOption(ctx, a, "model", "claude-fable-5-1")
	if err != nil {
		t.Fatalf("SetOption(\"claude-fable-5-1\") on a running session = %v", err)
	}
	if got := optionValue(session, "model"); got != "claude-fable-5-1" {
		t.Errorf("after picking a pinned model, the session reports %q", got)
	}
	th = waitThread(t, m, a, "the notice", func(th api.ChatThread) bool {
		return strings.Contains(find(th, "notice", 0).Text, "starts on it next time")
	})
	if strings.Contains(find(th, "notice", 0).Text, "isn't a choice") {
		t.Errorf("a pinned model was rejected instead of named for next time: %q", find(th, "notice", 0).Text)
	}

	var raw string
	for range 50 {
		raw, _ = store.Setting(ctx, state.SettingClaudeModelChoices)
		if raw != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var choices []api.ChatOptionChoice
	if err := json.Unmarshal([]byte(raw), &choices); err != nil {
		t.Fatalf("remembered model menu = %q: %v", raw, err)
	}
	if slices.ContainsFunc(choices, func(c api.ChatOptionChoice) bool { return c.Value == "claude-fable-5-1" }) {
		t.Errorf("a pinned model was remembered as though the adapter sent it: %+v", choices)
	}
}

func findOption(s api.ChatSession, id string) (api.ChatOption, bool) {
	i := slices.IndexFunc(s.Options, func(o api.ChatOption) bool { return o.ID == id })
	if i < 0 {
		return api.ChatOption{}, false
	}
	return s.Options[i], true
}

// TestSessionRemembersTheEffortMenu checks the effort levels are kept the same
// way as the models, so a new agent can be given one that Claude Code really
// named. Their sources differ in one way worth remembering: this menu is the
// levels "available for this model", and a model can advertise none at all, so
// a session that sends no menu leaves the last one alone rather than erasing it.
func TestSessionRemembersTheEffortMenu(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	f := newFakeTool(answerHello)
	f.effort = true
	m, _ := newManager(t, store, f)
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })

	var raw string
	for range 50 {
		raw, _ = store.Setting(ctx, state.SettingClaudeEffortChoices)
		if raw != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var choices []api.ChatOptionChoice
	if err := json.Unmarshal([]byte(raw), &choices); err != nil {
		t.Fatalf("remembered effort menu = %q: %v", raw, err)
	}
	if !slices.ContainsFunc(choices, func(c api.ChatOptionChoice) bool { return c.Value == "xhigh" }) {
		t.Errorf("the remembered menu doesn't hold the tool's own levels: %+v", choices)
	}

	// A model with no effort levels sends no menu. Forgetting the last one
	// would leave a new agent's effort unvalidated for no reason.
	quiet := newFakeTool(answerHello)
	m2, _ := newManager(t, store, quiet)
	if _, err := m2.Start(a); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m2, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if kept, _ := store.Setting(ctx, state.SettingClaudeEffortChoices); kept != raw {
		t.Errorf("a session with no effort menu changed the remembered one to %q", kept)
	}
}

// TestModelChoosableBeforeFirstMessage covers the gap a user found in the
// picker work: SessionOptions in Composer.tsx only ever showed a menu once
// session.options existed, which arrived with the first real session — so
// there was no way to choose a model before typing anything. load() now
// seeds a "model" option from the remembered menu the moment the chat is
// looked at, before Start is ever called, and SetOption stores a choice made
// against it. A fresh agent has no stored preference at all, so the seeded
// option defaults to "default" — what an untouched session actually reports —
// rather than an empty value the composer would show as a blank button.
func TestModelChoosableBeforeFirstMessage(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	remember(t, store, "sonnet", "opus", "haiku")

	th, err := (&Manager{Store: store}).Thread(a)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.State != api.ChatOff {
		t.Fatalf("session state = %q before Start, want %q", th.Session.State, api.ChatOff)
	}
	if got := optionValue(th.Session, "model"); got != "default" {
		t.Errorf("an untouched agent's pre-session model = %q, want %q", got, "default")
	}

	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	updated, err := m.SetOption(ctx, a, "model", "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if got := optionValue(updated, "model"); got != "haiku" {
		t.Fatalf("SetOption before Start reports %q, want %q", got, "haiku")
	}

	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	started := waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if got := optionValue(started.Session, "model"); got != "haiku" {
		t.Errorf("the session started on %q, want the model chosen before the first message, %q", got, "haiku")
	}
}

// TestModelChoiceBeforeFirstMessageAcceptsANamedModel checks the escape hatch
// from the other end: a model the remembered menu doesn't list can be named
// before any session exists, which is the only way a model that's advertised
// only once a session runs on it — Fable — can ever be chosen a first time
// (see modelNamedOutsideMenu). It is stored, and PrepareChatModel puts it in
// the tool's settings before the next session starts.
func TestModelChoiceBeforeFirstMessageAcceptsANamedModel(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	remember(t, store, "sonnet", "opus", "haiku")
	m := &Manager{Store: store}
	session, err := m.SetOption(ctx, testAgent, "model", "claude-fable-5-1")
	if err != nil {
		t.Fatalf("SetOption(\"claude-fable-5-1\") before Start = %v", err)
	}
	if got := optionValue(session, "model"); got != "claude-fable-5-1" {
		t.Errorf("the named model reports %q", got)
	}
	stored, err := store.Chat(ctx, testAgent.Project, testAgent.Name)
	if err != nil || stored.Options["model"] != "claude-fable-5-1" {
		t.Errorf("stored model = %q, %v", stored.Options["model"], err)
	}
	// Only the model has the escape hatch. Every other setting is still
	// checked against what the tool said it offers.
	if _, err := m.SetOption(ctx, testAgent, "effort", "made-up"); err == nil {
		t.Error("a value for another setting was accepted off the menu")
	}
}

// TestModelChoiceBeforeFirstMessageNeedsARememberedMenu checks the original
// error survives on a fresh install where no Claude Code chat has ever run:
// there's no menu to choose from yet, so SetOption still says so plainly
// instead of accepting a value it can't validate against anything real.
func TestModelChoiceBeforeFirstMessageNeedsARememberedMenu(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	m := &Manager{Store: store}
	_, err := m.SetOption(ctx, testAgent, "model", "haiku")
	if err == nil || !strings.Contains(err.Error(), "known once it has started") {
		t.Fatalf("SetOption() before any menu is remembered = %v, want the \"known once it has started\" error", err)
	}
}

// remember stores a model menu shaped like Claude Code's own, as if a chat had
// already started once (see rememberChoices).
func remember(t *testing.T, store *state.Store, values ...string) {
	t.Helper()
	choices := []api.ChatOptionChoice{{Value: "default", Name: "Default (recommended)", Description: "Sonnet"}}
	for _, v := range values {
		choices = append(choices, api.ChatOptionChoice{Value: v, Name: v})
	}
	raw, err := json.Marshal(choices)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(context.Background(), state.SettingClaudeModelChoices, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func optionValue(s api.ChatSession, id string) string {
	for _, o := range s.Options {
		if o.ID == id {
			return o.Value
		}
	}
	return ""
}

// TestModelChangeClearsStaleContextSize checks that switching models drops
// the last usage_update reading: it's the window of whichever model was
// actually running, so carrying it over would claim a size that was never
// measured for the model now selected.
func TestModelChangeClearsStaleContextSize(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"usage_update","used":1234,"size":200000}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	if th.Session.ContextSize != 200000 || th.Session.ContextUsed != 1234 {
		t.Fatalf("session after the turn = %+v, want a 200000-token reading", th.Session)
	}

	if _, err := m.SetOption(ctx, testAgent, "model", "haiku"); err != nil {
		t.Fatal(err)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.ContextSize != 0 || th.Session.ContextUsed != 0 {
		t.Errorf("session after switching models = %+v, want the stale reading cleared", th.Session)
	}
}

func TestClearStartsOver(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, rec := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	if err := m.Clear(testAgent); err != nil {
		t.Fatal(err)
	}
	th, err := m.Thread(testAgent)
	if err != nil || len(th.Items) != 0 || th.Session.State != api.ChatOff {
		t.Fatalf("after Clear: %s, %v", summary(th), err)
	}
	if !slices.ContainsFunc(rec.all(), func(ev api.ChatEvent) bool { return ev.Cleared }) {
		t.Error("no event said the conversation was cleared")
	}
	if stored, _ := store.Chat(context.Background(), testAgent.Project, testAgent.Name); stored.SessionID != "" {
		t.Errorf("the stored session is still %q", stored.SessionID)
	}
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the turn", turnsEnded(1))
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant"}) {
		t.Errorf("items %v", got)
	}
	if n := len(f.called(acp.MethodSessionNew)); n != 2 {
		t.Errorf("%d sessions started, want 2", n)
	}
}

func TestATurnLeftRunningByAStoppedDaemonIsSettled(t *testing.T) {
	store := openStore(t)
	now := time.Now()
	var rows []state.ChatItem
	for i, it := range []api.ChatItem{
		{ID: "u1", Turn: "u1", Kind: "user", Text: "make it", CreatedAt: now, UpdatedAt: now},
		{ID: "t1", Turn: "u1", Kind: "tool", Tool: &api.ChatTool{CallID: "c1", Title: "make", Kind: "execute", Status: "in_progress"}, CreatedAt: now, UpdatedAt: now},
		{ID: "a1", Turn: "u1", Kind: "assistant", Text: "Building", Streaming: true, CreatedAt: now, UpdatedAt: now},
	} {
		data, _ := json.Marshal(it)
		rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(i), Data: data})
	}
	if err := store.SaveChatItems(context.Background(), testAgent.Project, testAgent.Name, rows); err != nil {
		t.Fatal(err)
	}
	m, _ := newManager(t, store, newFakeTool(answerHello))
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(th); !slices.Equal(got, []string{"user", "tool", "assistant", "error"}) {
		t.Fatalf("items %v", got)
	}
	if r := th.Items[0].Result; r == nil || r.State != "failed" {
		t.Errorf("the turn ended with %+v", r)
	}
	if th.Items[1].Tool.Status != "stopped" || th.Items[2].Streaming || !strings.Contains(th.Items[3].Text, "AgentBox stopped") {
		t.Errorf("settled items: %+v %+v %+v", th.Items[1].Tool, th.Items[2], th.Items[3])
	}
}

// LastMessage is how the daemon learns what an agent did when it finishes,
// without replaying the whole conversation into the project's chat.
func TestLastMessage(t *testing.T) {
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		if text == "sum up" {
			f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"I changed "}}`)
			f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"the notice.\n"}}`)
			return acp.PromptResponse{StopReason: "end_turn"}
		}
		f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"ls","kind":"execute","status":"completed"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, openStore(t), f)

	if got := m.LastMessage(testAgent); got != "" {
		t.Errorf("a conversation with nothing in it has a last message: %q", got)
	}
	if _, err := m.Send(testAgent, "sum up"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if got, want := m.LastMessage(testAgent), "I changed the notice."; got != want {
		t.Errorf("LastMessage() = %q, want %q", got, want)
	}

	// A turn that ended on a tool call said nothing: the message before it
	// belongs to the turn before, and isn't this one's summary.
	if _, err := m.Send(testAgent, "now just look"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn to end", turnsEnded(2))
	if got := m.LastMessage(testAgent); got != "" {
		t.Errorf("LastMessage() = %q after a turn that said nothing, want nothing", got)
	}

	shell := testAgent
	shell.AI = "none"
	if got := m.LastMessage(shell); got != "" {
		t.Errorf("an agent with no AI tool said %q", got)
	}
}

func TestAnAgentWithoutAnAIToolHasNoChat(t *testing.T) {
	m, _ := newManager(t, openStore(t), newFakeTool(answerHello))
	shell := testAgent
	shell.AI = "none"
	if _, err := m.Thread(shell); err == nil {
		t.Error("an agent with only a shell has a chat")
	}
}

// TestPrepareRunsBeforeEverySessionWithTheStoredModel checks the launch-time
// half of choosing a model: what the chat has stored reaches the tool's own
// configuration before its adapter starts, on every session rather than once,
// because that file is read only at launch (D45).
func TestPrepareRunsBeforeEverySessionWithTheStoredModel(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	var mu sync.Mutex
	var prepared []string
	var launched int
	launch := m.Launch
	m.Prepare = func(_ context.Context, a state.Agent, model string, _ int64) error {
		mu.Lock()
		defer mu.Unlock()
		if a.Ref() != testAgent.Ref() {
			t.Errorf("prepared %q", a.Ref())
		}
		prepared = append(prepared, model)
		return nil
	}
	m.Launch = func(ctx context.Context, a state.Agent, status func(string)) (*Process, error) {
		mu.Lock()
		launched++
		if len(prepared) != launched {
			t.Errorf("the adapter was launched before the model was prepared (%d prepared, %d launched)", len(prepared), launched)
		}
		mu.Unlock()
		return launch(ctx, a, status)
	}

	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if _, err := m.SetOption(ctx, testAgent, "model", "haiku"); err != nil {
		t.Fatal(err)
	}
	m.Stop(testAgent.Ref(), "")
	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })

	mu.Lock()
	defer mu.Unlock()
	if len(prepared) != 2 {
		t.Fatalf("prepared %v, want one per session", prepared)
	}
	if prepared[0] != "" {
		t.Errorf("the first session prepared %q, want no stored preference", prepared[0])
	}
	if prepared[1] != "haiku" {
		t.Errorf("the second session prepared %q, want the model chosen in between", prepared[1])
	}
}

// TestPrepareFailureIsSaidInTheChat: a model that never reached the tool means
// the session is answering on something else. That was the failure that had
// agents running on Sonnet for weeks with nothing on screen, so it is a notice,
// not a log line — and the session still starts, because for a model the menu
// does offer set_config_option applies it anyway.
func TestPrepareFailureIsSaidInTheChat(t *testing.T) {
	store := openStore(t)
	m, _ := newManager(t, store, newFakeTool(answerHello))
	m.Prepare = func(context.Context, state.Agent, string, int64) error {
		return errors.New("its machine is unreachable")
	}
	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	notice := find(th, "notice", 0).Text
	if !strings.Contains(notice, "its machine is unreachable") || !strings.Contains(notice, "Claude Code") {
		t.Errorf("notice = %q, want the failure named", notice)
	}
	if th.Session.Error != "" {
		t.Errorf("the session failed to start over it: %q", th.Session.Error)
	}
}

// A message sent while a turn runs isn't refused: it joins that turn, so the
// model reads it mid-work and decides for itself what to do about it.
func TestSendDuringTurn(t *testing.T) {
	store := openStore(t)
	release := make(chan struct{})
	f := newSteeringTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Counting."}}`)
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)

	user, err := m.Send(testAgent, "Count to a hundred")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })

	aside, err := m.Send(testAgent, "Actually, stop at ten")
	if err != nil {
		t.Fatalf("a message sent while the turn ran was refused: %v", err)
	}
	if aside.Kind != "aside" || aside.Text != "Actually, stop at ten" || aside.Turn != user.ID {
		t.Errorf("the message = %+v, want an aside of turn %s", aside, user.ID)
	}

	got := f.waitSteer(t)
	if !strings.Contains(got, "Actually, stop at ten") {
		t.Errorf("the tool was given %q", got)
	}
	// The framing is the whole point: without it the model can't tell this
	// arrived mid-work, so "is it worth changing course for" isn't answerable.
	for _, want := range []string{"while you were working", "isn't finished", "change course now", "carry on"} {
		if !strings.Contains(got, want) {
			t.Errorf("the tool was given %q, which doesn't say %q", got, want)
		}
	}
	// It joined the running turn rather than starting one of its own.
	if calls := f.called(acp.MethodSessionPrompt); len(calls) != 1 {
		t.Errorf("%d prompts, want 1: the message should have joined the turn, not started one", len(calls))
	}
	th := waitThread(t, m, testAgent, "the message to be marked sent", func(th api.ChatThread) bool {
		return find(th, "aside", 0).Delivery == api.ChatAsideSent
	})
	if th.Session.State != api.ChatRunning {
		t.Errorf("session %s, want the turn to still be running", th.Session.State)
	}

	close(release)
	th = waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if got := kinds(th); !slices.Equal(got, []string{"user", "assistant", "aside"}) {
		t.Errorf("items %v", got)
	}
	// One turn, ended once: the message didn't make a second one.
	if find(th, "user", 0).Result.State != "completed" {
		t.Errorf("the turn = %+v", find(th, "user", 0).Result)
	}
}

// Several messages during one turn all arrive, in the order they were sent.
func TestSendDuringTurnKeepsOrder(t *testing.T) {
	store := openStore(t)
	release := make(chan struct{})
	f := newSteeringTool(func(_ *fakeTool, _, _ string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })

	sent := []string{"first", "second", "third", "fourth"}
	for _, text := range sent {
		if _, err := m.Send(testAgent, text); err != nil {
			t.Fatalf("sending %q: %v", text, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.steeredTexts()) < len(sent) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := f.steeredTexts(); !slices.Equal(got, sent) {
		t.Errorf("the tool was given %v, want %v", got, sent)
	}
	// Each steer pre-empts the generation, and adapters given several in quick
	// succession take minutes to settle the turn afterwards, so messages that
	// are waiting together go in together.
	if n := f.steerCount(); n > len(sent) {
		t.Errorf("%d steering requests for %d messages", n, len(sent))
	}
	close(release)

	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	var asides []string
	for _, it := range th.Items {
		if it.Kind == "aside" {
			asides = append(asides, it.Text)
			if it.Delivery != api.ChatAsideSent {
				t.Errorf("%q was left %q", it.Text, it.Delivery)
			}
		}
	}
	if !slices.Equal(asides, sent) {
		t.Errorf("the conversation holds %v, want %v", asides, sent)
	}
}

// A tool that can't take a message mid-turn still mustn't lose it: it goes in
// as a turn of its own once the running one ends, and the sender is told which
// happened rather than being left to assume.
func TestSendDuringTurnWithoutSteering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fakeTool)
	}{
		{"no support", func(f *fakeTool) {}},
		{"the request fails", func(f *fakeTool) { f.steering, f.steerFails = true, true }},
		{"no turn was running after all", func(f *fakeTool) { f.steering, f.steerIdle = true, true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openStore(t)
			release := make(chan struct{})
			var prompts []string
			var mu sync.Mutex
			f := newFakeTool(func(_ *fakeTool, _, text string) acp.PromptResponse {
				mu.Lock()
				first := len(prompts) == 0
				prompts = append(prompts, text)
				mu.Unlock()
				if first {
					<-release
				}
				return acp.PromptResponse{StopReason: "end_turn"}
			})
			tc.setup(f)
			m, _ := newManager(t, store, f)

			if _, err := m.Send(testAgent, "Start"); err != nil {
				t.Fatal(err)
			}
			waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
			for _, text := range []string{"first", "second"} {
				if _, err := m.Send(testAgent, text); err != nil {
					t.Fatalf("a message sent while the turn ran was refused: %v", err)
				}
			}
			th := waitThread(t, m, testAgent, "the messages to be held back", func(th api.ChatThread) bool {
				return find(th, "aside", 0).Delivery == api.ChatAsideDeferred
			})
			if th.Session.State != api.ChatRunning {
				t.Errorf("session %s, want the first turn still running", th.Session.State)
			}
			close(release)

			th = waitThread(t, m, testAgent, "both turns to end", turnsEnded(2))
			mu.Lock()
			got := append([]string(nil), prompts...)
			mu.Unlock()
			if !slices.Equal(got, []string{"Start", "first\n\nsecond"}) {
				t.Errorf("prompts %q, want the held messages as one turn after the first", got)
			}
			// The oldest held message heads the turn it now belongs to, where it
			// already stood: its text isn't repeated under a new message.
			if k := kinds(th); !slices.Equal(k, []string{"user", "user", "aside"}) {
				t.Errorf("items %v, want the first held message promoted in place", k)
			}
			second := find(th, "user", 1)
			if second.Text != "first" || second.Turn != second.ID || second.Result == nil {
				t.Errorf("the follow-up turn = %+v", second)
			}
			if a := find(th, "aside", 0); a.Text != "second" || a.Turn != second.ID || a.Delivery != "" {
				t.Errorf("the second held message = %+v, want it inside turn %s", a, second.ID)
			}
		})
	}
}

// Stopping a turn still works with a message waiting, and doesn't strand it.
func TestCancelWithMessageWaiting(t *testing.T) {
	store := openStore(t)
	var prompts []string
	var mu sync.Mutex
	f := newSteeringTool(func(f *fakeTool, _, text string) acp.PromptResponse {
		mu.Lock()
		first := len(prompts) == 0
		prompts = append(prompts, text)
		mu.Unlock()
		if !first {
			return acp.PromptResponse{StopReason: "end_turn"}
		}
		<-f.cancels
		return acp.PromptResponse{StopReason: "cancelled"}
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if _, err := m.Send(testAgent, "and also this"); err != nil {
		t.Fatal(err)
	}
	f.waitSteer(t)

	if _, err := m.Cancel(testAgent); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to be stopped", turnsEnded(1))
	if find(th, "user", 0).Result.State != "cancelled" {
		t.Errorf("the turn = %+v, want it cancelled", find(th, "user", 0).Result)
	}
	// It reached the tool before the stop, so it isn't sent again: stopping a
	// turn mustn't quietly re-ask for work the tool already had.
	mu.Lock()
	got := append([]string(nil), prompts...)
	mu.Unlock()
	if !slices.Equal(got, []string{"Start"}) {
		t.Errorf("prompts %q, want only the first: the message had already been delivered", got)
	}
	if a := find(th, "aside", 0); a.Delivery != api.ChatAsideSent {
		t.Errorf("the message = %+v", a)
	}
}

// A message the tool never got, on a turn that is then stopped, goes in as its
// own turn: cancelling ends the work, not the message that was waiting on it.
func TestCancelSendsWhatWasHeldBack(t *testing.T) {
	store := openStore(t)
	var prompts []string
	var mu sync.Mutex
	f := newFakeTool(func(f *fakeTool, _, text string) acp.PromptResponse {
		mu.Lock()
		first := len(prompts) == 0
		prompts = append(prompts, text)
		mu.Unlock()
		if !first {
			return acp.PromptResponse{StopReason: "end_turn"}
		}
		<-f.cancels
		return acp.PromptResponse{StopReason: "cancelled"}
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if _, err := m.Send(testAgent, "held back"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the message to be held back", func(th api.ChatThread) bool {
		return find(th, "aside", 0).Delivery == api.ChatAsideDeferred
	})

	if _, err := m.Cancel(testAgent); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "both turns to end", turnsEnded(2))
	mu.Lock()
	got := append([]string(nil), prompts...)
	mu.Unlock()
	if !slices.Equal(got, []string{"Start", "held back"}) {
		t.Errorf("prompts %q, want the held message sent after the stop", got)
	}
}

// A session that ends with a message still waiting says so: one that was taken
// and then dropped in silence is worse than one that was refused outright.
func TestStopLosesWhatWasHeldBackVisibly(t *testing.T) {
	store := openStore(t)
	release := make(chan struct{})
	f := newFakeTool(func(_ *fakeTool, _, _ string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	t.Cleanup(func() { close(release) })
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if _, err := m.Send(testAgent, "held back"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the message to be held back", func(th api.ChatThread) bool {
		return find(th, "aside", 0).Delivery == api.ChatAsideDeferred
	})

	m.Stop(testAgent.Ref(), "the agent was stopped")
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if a := find(th, "aside", 0); a.Delivery != api.ChatAsideLost {
		t.Errorf("the message = %+v, want it marked lost", a)
	}
	if e := find(th, "error", 0); !strings.Contains(e.Text, "never reached") {
		t.Errorf("nothing told the sender the message was dropped: %+v", e)
	}
	// It didn't quietly restart the session to deliver it either.
	if calls := f.called(acp.MethodSessionPrompt); len(calls) != 1 {
		t.Errorf("%d prompts, want 1", len(calls))
	}
}

// A message still waiting when AgentBox stops is marked when it comes back: the
// outbox only ever lived in memory, so nothing will deliver it now.
func TestHeldBackMessageIsLostAcrossRestart(t *testing.T) {
	store := openStore(t)
	release := make(chan struct{})
	f := newFakeTool(func(_ *fakeTool, _, _ string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	if _, err := m.Send(testAgent, "held back"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the message to be stored", func(th api.ChatThread) bool {
		return find(th, "aside", 0).Delivery == api.ChatAsideDeferred
	})
	close(release)

	// A new manager over the same store is the daemon starting again.
	second, _ := newManager(t, store, newFakeTool(answerHello))
	th, err := second.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if a := find(th, "aside", 0); a.Delivery != api.ChatAsideLost {
		t.Errorf("the message = %+v, want it marked lost", a)
	}
	if e := find(th, "error", 1); !strings.Contains(e.Text, "before the messages above reached") {
		t.Errorf("nothing said the message never arrived: %v", kinds(th))
	}
}

// A notice never joins a running turn, however many arrive: a lead that reacts
// to one by messaging the agent can be told again by the turn that causes, and
// landing those mid-turn would tighten that into a loop.
func TestNoticesDoNotJoinTheRunningTurn(t *testing.T) {
	store := openStore(t)
	release := make(chan struct{})
	f := newSteeringTool(func(_ *fakeTool, _, _ string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	for _, text := range []string{"agent-02 finished", "agent-03 finished"} {
		if err := m.Notice(testAgent, text, NoticeOptions{Act: true, Hidden: true}); err != nil {
			t.Fatal(err)
		}
	}
	close(release)

	waitThread(t, m, testAgent, "the notices to be delivered", turnsEnded(2))
	if got := f.steeredTexts(); len(got) != 0 {
		t.Errorf("notices reached the running turn: %v", got)
	}
	// Both finishes still woke the chat once, not twice.
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.called(acp.MethodSessionPrompt)); n != 2 {
		t.Errorf("%d prompts, want 2: several notices become one turn", n)
	}
	if notice := find(th, "notice", 0); !strings.Contains(notice.Text, "agent-02") || !strings.Contains(notice.Text, "agent-03") {
		t.Errorf("the notice = %+v, want both", notice)
	}
}

// A dead login is not a failure of this agent: the token is stored once and
// shared, so the chat says who refused it and lets the daemon mark the
// account, rather than every agent on it hitting the same 401 in turn.
func TestARefusedLoginIsReported(t *testing.T) {
	f := newFakeTool(answerHello)
	f.promptErr = &acp.Error{Code: acp.CodeInternalError,
		Message: `API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`}
	m, _ := newManager(t, openStore(t), f)
	refused := make(chan string, 1)
	m.AuthFailed = func(a state.Agent, detail string) { refused <- a.Ref() + ": " + detail }
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-refused:
		if !strings.Contains(got, testAgent.Ref()) || !strings.Contains(got, "authentication_error") {
			t.Errorf("reported %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn failed on the login and nothing was reported")
	}
	// The chat still shows it where it happened.
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if e := find(th, "error", 0); !strings.Contains(e.Text, "OAuth token has expired") {
		t.Errorf("the chat says %q", e.Text)
	}
}

// A turn can fail for a hundred reasons that aren't the login, and sending
// someone to log in again for one of those wastes their time.
func TestAnOrdinaryFailureIsNotAReport(t *testing.T) {
	f := newFakeTool(answerHello)
	f.promptErr = &acp.Error{Code: acp.CodeInternalError, Message: "API Error: 529 {\"type\":\"overloaded_error\"}"}
	m, _ := newManager(t, openStore(t), f)
	refused := make(chan string, 1)
	m.AuthFailed = func(_ state.Agent, detail string) { refused <- detail }
	if _, err := m.Send(testAgent, "go"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	select {
	case got := <-refused:
		t.Errorf("an overloaded model was reported as a dead login: %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAuthFailure(t *testing.T) {
	refused := []string{
		`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth access token is invalid."}}`,
		"OAuth token has expired. Please obtain a new token or refresh your existing token.",
		"Invalid API key · Please run /login",
		"the agent exited: 401 Unauthorized",
	}
	fine := []string{
		"Claude Code didn't start: exec: \"claude-agent-acp\": executable file not found in $PATH",
		"API Error: 529 overloaded_error",
		"Your credit balance is too low to access the Anthropic API",
		"the chat was stopped",
		"Permission denied: you are not authorized to write /etc/hosts",
	}
	for _, text := range refused {
		if !AuthFailure(text) {
			t.Errorf("a refused login went unnoticed: %q", text)
		}
	}
	for _, text := range fine {
		if AuthFailure(text) {
			t.Errorf("this isn't a refused login: %q", text)
		}
	}
}

// An OpenCode chat's menus are remembered under OpenCode's own keys, never
// Claude Code's: a "provider/model" id means nothing to Claude Code, and
// mixing the two lists would offer models no account can run. The same
// session also lets a model be chosen before it has started, from the menu
// OpenCode last named.
func TestOpenCodeSessionKeepsItsOwnModelMenu(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	a.AI = "opencode"
	m, _ := newManager(t, store, newFakeTool(answerHello))
	if _, err := m.Start(a); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })

	var raw string
	for range 50 {
		raw, _ = store.Setting(ctx, state.SettingOpenCodeModelChoices)
		if raw != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Contains(state.ChoiceValues(raw), "sonnet") {
		t.Errorf("OpenCode's remembered menu = %q, want the menu its session advertised", raw)
	}
	if claude, _ := store.Setting(ctx, state.SettingClaudeModelChoices); claude != "" {
		t.Errorf("an OpenCode session wrote Claude Code's menu too: %q", claude)
	}
}

// Before an OpenCode session has ever started there are no live options, and
// the model is still worth choosing: it comes off the menu OpenCode named
// last, and is applied when the session starts.
func TestOpenCodeModelChosenBeforeTheSessionStarts(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	a := testAgent
	a.AI = "opencode"
	raw, err := json.Marshal([]api.ChatOptionChoice{{Value: "anthropic/claude-sonnet-5", Name: "anthropic / claude-sonnet-5"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, state.SettingOpenCodeModelChoices, string(raw)); err != nil {
		t.Fatal(err)
	}
	m, _ := newManager(t, store, newFakeTool(answerHello))

	s, err := m.SetOption(ctx, a, "model", "anthropic/claude-sonnet-5")
	if err != nil {
		t.Fatalf("choosing an OpenCode model before the session started: %v", err)
	}
	if got := optionValue(s, "model"); got != "anthropic/claude-sonnet-5" {
		t.Errorf("the chosen model reads back as %q", got)
	}
	// A model OpenCode never named is refused: unlike Claude Code, OpenCode
	// has no launch-time channel that reaches past its own menu.
	if _, err := m.SetOption(ctx, a, "model", "made-up/model"); err == nil || !strings.Contains(err.Error(), "isn't a choice") {
		t.Errorf("a model outside OpenCode's menu: %v", err)
	}
}

// TestTheContextWindowRestartsTheAdapterAndResumesTheSession: the context
// window is AgentBox's option, applied as the compact window the adapter reads
// when it starts (D91). Choosing another mid-chat says it applies from the next
// message, and that message restarts the adapter on it, resuming the same
// session rather than starting over.
func TestTheContextWindowRestartsTheAdapterAndResumesTheSession(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	f := newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"usage_update","used":1234,"size":1000000}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	f.resume = true
	m, _ := newManager(t, store, f)
	var mu sync.Mutex
	var windows []int64
	m.Prepare = func(_ context.Context, _ state.Agent, _ string, window int64) error {
		mu.Lock()
		defer mu.Unlock()
		windows = append(windows, window)
		return nil
	}
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	// Sonnet reports 1M, but compacts at the installation's 200k: that is
	// the room the session has.
	if th.Session.ContextSize != state.DefaultClaudeCompactWindow {
		t.Errorf("context size = %d, want the compact window", th.Session.ContextSize)
	}
	if got := optionValue(th.Session, state.ChatOptionContextWindow); got != "200000" {
		t.Errorf("context window = %q, want 200000", got)
	}

	if _, err := m.SetOption(ctx, testAgent, state.ChatOptionContextWindow, "1m"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the notice", func(th api.ChatThread) bool { return slices.Contains(kinds(th), "notice") })
	if !strings.Contains(find(th, "notice", 0).Text, "applies from your next message") {
		t.Errorf("notice = %q", find(th, "notice", 0).Text)
	}
	if got := optionValue(th.Session, state.ChatOptionContextWindow); got != "1000000" {
		t.Errorf("context window = %q after choosing 1M", got)
	}
	if stored, _ := store.Chat(ctx, testAgent.Project, testAgent.Name); stored.Options[state.ChatOptionContextWindow] != "1000000" {
		t.Errorf("stored %v", stored.Options)
	}
	if _, err := m.SetOption(ctx, testAgent, state.ChatOptionContextWindow, "300k"); err == nil {
		t.Error("a window Sonnet doesn't offer here was accepted")
	}

	if _, err := m.Send(testAgent, "and again"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if th.Session.ContextSize != 1_000_000 {
		t.Errorf("context size = %d on the 1M window", th.Session.ContextSize)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(windows, []int64{200_000, 1_000_000}) {
		t.Errorf("the adapter started on %v, want 200k and then 1M", windows)
	}
	if resumed := f.called(acp.MethodSessionResume); len(resumed) != 1 {
		t.Errorf("resumed %d times, want the restart to resume the session", len(resumed))
	}
	// And what the session reported is remembered as Sonnet's window.
	w, _ := store.ClaudeWindows(ctx)
	if w.Seen["sonnet"] != 1_000_000 {
		t.Errorf("remembered %v", w.Seen)
	}
}

// TestTheContextWindowIsChosenBeforeTheChatStarts: the window is AgentBox's
// option, so it needs no live session. A chat that has never started, on an
// installation that has no model menu to remember yet, still offers it for
// its stored model, and a choice made then is what the adapter starts on.
func TestTheContextWindowIsChosenBeforeTheChatStarts(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if err := store.SaveChat(ctx, testAgent.Project, testAgent.Name, state.Chat{Options: map[string]string{"model": "opus"}}); err != nil {
		t.Fatal(err)
	}
	m, _ := newManager(t, store, newFakeTool(answerHello))
	var mu sync.Mutex
	var windows []int64
	m.Prepare = func(_ context.Context, _ state.Agent, _ string, window int64) error {
		mu.Lock()
		defer mu.Unlock()
		windows = append(windows, window)
		return nil
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if th.Session.State != api.ChatOff {
		t.Fatalf("state = %s, want a chat that hasn't started", th.Session.State)
	}
	if got := optionValue(th.Session, state.ChatOptionContextWindow); got != "200000" {
		t.Errorf("context window = %q before the chat started, want the 200k default offered", got)
	}

	s, err := m.SetOption(ctx, testAgent, state.ChatOptionContextWindow, "1m")
	if err != nil {
		t.Fatalf("choosing the window before the chat started: %v", err)
	}
	if got := optionValue(s, state.ChatOptionContextWindow); got != "1000000" {
		t.Errorf("context window = %q after choosing 1M", got)
	}
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(windows, []int64{1_000_000}) {
		t.Errorf("the adapter started on %v, want the 1M chosen before it started", windows)
	}
}

// TestTheContextWindowStaysWhileTheAdapterRestarts: a window change restarts
// the adapter, and the picker mustn't go with it, even when the resumed
// session reports no options of its own.
func TestTheContextWindowStaysWhileTheAdapterRestarts(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	f := newFakeTool(answerHello)
	f.resume = true
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the first turn", turnsEnded(1))
	if _, err := m.SetOption(ctx, testAgent, state.ChatOptionContextWindow, "1m"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.resumeBare = true
	f.mu.Unlock()
	if _, err := m.Send(testAgent, "and again"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the second turn", turnsEnded(2))
	if len(f.called(acp.MethodSessionResume)) != 1 {
		t.Fatalf("resumed %d times, want the window change to restart the adapter", len(f.called(acp.MethodSessionResume)))
	}
	if got := optionValue(th.Session, state.ChatOptionContextWindow); got != "1000000" {
		t.Errorf("context window = %q after the restart, want the 1M still offered: %+v", got, th.Session.Options)
	}
}

// TestHaikuHasNoContextWindowChoice: a model with one window gets no control.
func TestHaikuHasNoContextWindowChoice(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	m, _ := newManager(t, store, newFakeTool(answerHello))
	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	session, err := m.SetOption(ctx, testAgent, "model", "haiku")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(session.Options, func(o api.ChatOption) bool { return o.ID == state.ChatOptionContextWindow }) {
		t.Errorf("Haiku was offered a context window: %+v", session.Options)
	}
	if _, err := m.SetOption(ctx, testAgent, state.ChatOptionContextWindow, "1m"); err == nil {
		t.Error("Haiku was given the 1M window")
	}
}
