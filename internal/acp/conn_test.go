package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeAgent is the agent's end of a connection. It reads what the client writes
// as it arrives, so the client never blocks on a pipe, and writes raw lines back.
type fakeAgent struct {
	lines chan []byte
	out   *io.PipeWriter
}

func newFakeAgent(in io.Reader, out *io.PipeWriter) fakeAgent {
	a := fakeAgent{lines: make(chan []byte, 64), out: out}
	go func() {
		s := bufio.NewScanner(in)
		for s.Scan() {
			a.lines <- append([]byte(nil), s.Bytes()...)
		}
		close(a.lines)
	}()
	return a
}

func (a fakeAgent) next(t *testing.T) message {
	t.Helper()
	select {
	case line, ok := <-a.lines:
		if !ok {
			t.Fatal("the client closed the connection")
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("the client sent %q: %v", line, err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("the client sent nothing")
	}
	return message{}
}

func (a fakeAgent) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(a.out, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

type recorder struct {
	mu       sync.Mutex
	got      []string
	requests chan func(result any, err error)
}

func (r *recorder) Notify(method string, params json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, method+" "+string(params))
}

func (r *recorder) Request(method string, params json.RawMessage, reply func(result any, err error)) {
	r.Notify(method, params)
	r.requests <- reply
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func connect(t *testing.T) (*Conn, fakeAgent, *recorder) {
	t.Helper()
	toAgent, fromClient := io.Pipe()
	toClient, fromAgent := io.Pipe()
	rec := &recorder{requests: make(chan func(any, error), 4)}
	c := NewConn(toClient, fromClient, rec)
	t.Cleanup(func() {
		_ = fromAgent.Close()
		_ = toAgent.Close()
	})
	return c, newFakeAgent(toAgent, fromAgent), rec
}

func TestCall(t *testing.T) {
	c, agent, _ := connect(t)
	type result struct {
		SessionID string `json:"sessionId"`
	}
	done := make(chan error, 1)
	var got result
	go func() { done <- c.Call(context.Background(), "session/new", map[string]string{"cwd": "/work"}, &got) }()

	m := agent.next(t)
	if m.Method != "session/new" || string(m.Params) != `{"cwd":"/work"}` || m.JSONRPC != "2.0" {
		t.Fatalf("the request was %+v", m)
	}
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{"sessionId":"s-1"}}`)
	if err := <-done; err != nil || got.SessionID != "s-1" {
		t.Fatalf("Call() = %+v, %v", got, err)
	}

	go func() { done <- c.Call(context.Background(), "session/new", nil, nil) }()
	m = agent.next(t)
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"error":{"code":-32000,"message":"Authentication required"}}`)
	err := <-done
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeAuthRequired || err.Error() != "session/new: Authentication required" {
		t.Fatalf("Call() error = %v", err)
	}
}

func TestNotificationsAndRequestsArriveInOrder(t *testing.T) {
	c, agent, rec := connect(t)
	agent.send(t, `not json, like a log line`)
	agent.send(t, `{"jsonrpc":"2.0","method":"session/update","params":{"n":1}}`)
	agent.send(t, `{"jsonrpc":"2.0","method":"session/update","params":{"n":2}}`)
	agent.send(t, `{"jsonrpc":"2.0","id":"ask-7","method":"session/request_permission","params":{"n":3}}`)
	agent.send(t, `{"jsonrpc":"2.0","method":"session/update","params":{"n":4}}`)

	reply := <-rec.requests
	deadline := time.Now().Add(5 * time.Second)
	for len(rec.seen()) < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	want := []string{`session/update {"n":1}`, `session/update {"n":2}`, `session/request_permission {"n":3}`, `session/update {"n":4}`}
	got := rec.seen()
	if len(got) != len(want) {
		t.Fatalf("handled %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("handled %q, want %q", got, want)
		}
	}

	// Answered later, from another goroutine; a second answer is dropped.
	go func() {
		reply(map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": "allow-once"}}, nil)
		reply(nil, errors.New("too late"))
	}()
	m := agent.next(t)
	if string(m.ID) != `"ask-7"` || string(m.Result) != `{"outcome":{"optionId":"allow-once","outcome":"selected"}}` || m.Error != nil {
		t.Fatalf("the answer was %+v", m)
	}
	if err := c.Notify("session/cancel", map[string]string{"sessionId": "s-1"}); err != nil {
		t.Fatal(err)
	}
	if m := agent.next(t); m.Method != "session/cancel" || len(m.ID) != 0 {
		t.Fatalf("the notification was %+v", m)
	}
	select {
	case line := <-agent.lines:
		t.Fatalf("the second answer was sent too: %s", line)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRequestErrorsAreSentAsJSONRPCErrors(t *testing.T) {
	_, agent, rec := connect(t)
	agent.send(t, `{"jsonrpc":"2.0","id":3,"method":"fs/read_text_file","params":{}}`)
	reply := <-rec.requests
	go reply(nil, &Error{Code: CodeMethodNotFound, Message: "method not found"})
	if m := agent.next(t); string(m.ID) != "3" || m.Error == nil || m.Error.Code != CodeMethodNotFound || m.Result != nil {
		t.Fatalf("the answer was %+v", m)
	}
}

func TestPendingCallsFailWhenTheAgentExits(t *testing.T) {
	c, agent, _ := connect(t)
	done := make(chan error, 1)
	go func() { done <- c.Call(context.Background(), "session/prompt", nil, nil) }()
	agent.next(t)
	_ = agent.out.Close()

	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("Call() = %v, want ErrClosed", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done() wasn't closed")
	}
	if err := c.Call(context.Background(), "session/new", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Call() after the end = %v, want ErrClosed", err)
	}
}

func TestConfigOptionsReadGroupsAndBooleans(t *testing.T) {
	var options []ConfigOption
	err := json.Unmarshal([]byte(`[
		{"id":"mode","name":"Mode","category":"mode","type":"select","currentValue":"default","options":[
			{"value":"default","name":"Manual","_meta":{"kind":"standard"}},
			{"value":"bypassPermissions","name":"Bypass permissions","_meta":{"kind":"full_access"}}]},
		{"id":"model","name":"Model","category":"model","type":"select","currentValue":"gpt-5","options":[
			{"group":"openai","name":"OpenAI","options":[{"value":"gpt-5","name":"GPT-5"}]}]},
		{"id":"fast","name":"Fast mode","type":"boolean","currentValue":true}]`), &options)
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 3 {
		t.Fatalf("got %d options", len(options))
	}
	if m := options[0]; m.Value != "default" || len(m.Choices) != 2 || m.Choices[1].Kind != "full_access" {
		t.Errorf("mode = %+v", m)
	}
	if m := options[1]; len(m.Choices) != 1 || m.Choices[0].Group != "OpenAI" || m.Choices[0].Value != "gpt-5" {
		t.Errorf("model = %+v", m)
	}
	if f := options[2]; f.Type != "boolean" || f.Value != "true" {
		t.Errorf("fast = %+v", f)
	}
}
