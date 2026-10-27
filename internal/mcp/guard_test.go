package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGuardFakeServer is the server the guard's tests run: the test binary
// again, as an MCP server whose "hang" tool never answers, whose "pid" tool
// says which process answered, and which refuses calls before initialize.
func TestGuardFakeServer(t *testing.T) {
	if os.Getenv("AGENTBOX_FAKE_MCP") != "1" {
		t.Skip("run by the guard's tests")
	}
	in := bufio.NewScanner(os.Stdin)
	initialized := false
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		text := ""
		switch {
		case m.Method == "initialize":
			initialized = true
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", m.ID)
			continue
		case !initialized:
			text = "not initialized"
		case m.Params.Name == "hang":
			continue
		default:
			text = fmt.Sprint(os.Getpid())
		}
		fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":%q}]}}`+"\n", m.ID, text)
	}
	os.Exit(0)
}

func TestGuardAnswersAHungCallAndRestartsTheServer(t *testing.T) {
	t.Setenv("AGENTBOX_FAKE_MCP", "1")
	g := &Guard{Command: []string{os.Args[0], "-test.run=^TestGuardFakeServer$"}, Timeout: 300 * time.Millisecond, Name: "Fake", Stderr: io.Discard}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- g.Serve(ctx, inR, outW) }()

	out := bufio.NewScanner(outR)
	send := func(line string) {
		t.Helper()
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	type answer struct {
		ID     json.RawMessage `json:"id"`
		Result struct {
			Content []Content `json:"content"`
			IsError bool      `json:"isError"`
		} `json:"result"`
	}
	read := func() answer {
		t.Helper()
		if !out.Scan() {
			t.Fatalf("no answer: %v", out.Err())
		}
		if strings.Contains(out.Text(), "agentbox-guard") {
			t.Fatalf("the replayed initialize's answer reached the client: %s", out.Text())
		}
		var a answer
		if err := json.Unmarshal(out.Bytes(), &a); err != nil {
			t.Fatalf("%v: %s", err, out.Text())
		}
		return a
	}
	call := func(id int, tool string) {
		send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{}}}`, id, tool))
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	if a := read(); string(a.ID) != "1" {
		t.Fatalf("initialize answered as %s", a.ID)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	call(2, "pid")
	first := read().Result.Content[0].Text

	start := time.Now()
	call(3, "hang")
	a := read()
	if string(a.ID) != "3" || !a.Result.IsError || !strings.Contains(a.Result.Content[0].Text, "hang didn't answer within 300ms") {
		t.Fatalf("the hung call was answered %s %+v", a.ID, a.Result)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the hung call took %s to answer", took)
	}

	call(4, "pid")
	a = read()
	if string(a.ID) != "4" || a.Result.IsError {
		t.Fatalf("the call after the restart was answered %s %+v", a.ID, a.Result)
	}
	if second := a.Result.Content[0].Text; second == "not initialized" || second == first {
		t.Fatalf("after the restart, %q answered (before it, %q): want a new, initialized server", second, first)
	}

	_ = inW.Close()
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestGuardEndsWhenTheServerExits(t *testing.T) {
	g := &Guard{Command: []string{"true"}, Timeout: time.Minute, Name: "Fake", Stderr: io.Discard}
	inR, _ := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := g.Serve(ctx, inR, io.Discard); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("Serve = %v, want the server's exit", err)
	}
}
