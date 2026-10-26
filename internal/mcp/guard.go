package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// A Guard runs another MCP server and gives each of its tool calls a deadline.
//
// The Playwright MCP server has none for some of its calls: browser_evaluate
// is Playwright's page.evaluate, which waits for as long as the page takes,
// and a page that never answers — stuck in a loop, behind a dialog, on a hung
// GPU — kept the call, the AI tool's turn and the agent busy with it until
// somebody stopped the agent. Its --timeout-action and --timeout-navigation
// bound clicks and page loads, not that.
//
// The guard sits between the AI tool and the server on stdin and stdout and
// passes every message through. A tools/call that isn't answered within
// Timeout is answered by the guard instead, as a failed call that says what
// happened, and the server, which may well be stuck behind it, is killed and
// started again: the guard replays the client's initialize to it, and drops
// its answer, so the AI tool carries on with the same session. Calls still
// running in the old server are answered the same way, and whatever it sends
// afterwards is dropped.
type Guard struct {
	Command []string      // the server and its arguments
	Timeout time.Duration // how long one tool call may take
	Name    string        // the server's, for the message: "Playwright"
	Stderr  io.Writer     // where the server's stderr and the guard's own notes go

	mu      sync.Mutex
	out     io.Writer
	child   *guardChild
	gen     int
	init    []byte // the client's initialize request, as it sent it
	inited  []byte // its notifications/initialized, likewise
	pending map[string]*guardCall
	failed  chan error // a server that exited by itself, or couldn't be started again
}

type guardChild struct {
	gen   int
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{} // closed once its stdout has ended
}

type guardCall struct {
	id    json.RawMessage
	tool  string
	gen   int
	timer *time.Timer
}

// guardInitID is the id the guard gives the initialize it replays to a
// restarted server, whose answer is the guard's and not the client's.
func guardInitID(gen int) string { return fmt.Sprintf(`"agentbox-guard-init-%d"`, gen) }

// Serve relays in to the server and the server to out until in ends, ctx is
// done or the server exits by itself, which ends it with an error.
func (g *Guard) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if len(g.Command) == 0 {
		return errors.New("no MCP server to run")
	}
	if g.Stderr == nil {
		g.Stderr = os.Stderr
	}
	g.out, g.pending, g.failed = out, map[string]*guardCall{}, make(chan error, 1)
	g.mu.Lock()
	err := g.start()
	g.mu.Unlock()
	if err != nil {
		return err
	}
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		r := bufio.NewReader(in)
		for {
			line, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				lines <- line
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()
	defer g.stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-g.failed:
			return err
		case err := <-readErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case line := <-lines:
			g.fromClient(line)
		}
	}
}

// relayed is what the guard reads of a JSON-RPC message.
type relayed struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}

// fromClient passes one message from the client on to the server, noting an
// initialize to replay and a tool call to time.
func (g *Guard) fromClient(line []byte) {
	var m relayed
	_ = json.Unmarshal(line, &m)
	g.mu.Lock()
	defer g.mu.Unlock()
	switch m.Method {
	case "initialize":
		g.init = bytes.Clone(line)
	case "notifications/initialized":
		g.inited = bytes.Clone(line)
	case "tools/call":
		if len(m.ID) > 0 && g.Timeout > 0 {
			key := string(m.ID)
			c := &guardCall{id: bytes.Clone(m.ID), tool: m.Params.Name, gen: g.gen}
			c.timer = time.AfterFunc(g.Timeout, func() { g.expire(key, c) })
			g.pending[key] = c
		}
	}
	if g.child != nil {
		_, _ = g.child.stdin.Write(line)
	}
}

// fromServer passes one message from a server on to the client, unless that
// server has since been replaced, or it is the answer to a replayed
// initialize. The guard is locked.
func (g *Guard) fromServer(gen int, line []byte) {
	if gen != g.gen {
		return
	}
	var m relayed
	if json.Unmarshal(line, &m) == nil && len(m.ID) > 0 && m.Method == "" {
		if string(m.ID) == guardInitID(gen) {
			return
		}
		if c := g.pending[string(m.ID)]; c != nil {
			c.timer.Stop()
			delete(g.pending, string(m.ID))
		}
	}
	_, _ = g.out.Write(line)
}

// expire answers a tool call that ran out of time, and every other call the
// same server still had, and starts the server again.
func (g *Guard) expire(key string, c *guardCall) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[key] != c || c.gen != g.gen {
		return // answered in the meantime
	}
	_, _ = fmt.Fprintf(g.Stderr, "agentbox: %s didn't answer within %s; restarting the %s MCP server\n", cmpStr(c.tool, "a tool call"), g.Timeout, g.Name)
	for k, other := range g.pending {
		other.timer.Stop()
		delete(g.pending, k)
		why := fmt.Sprintf("%s was stopped: it ran in the %s MCP server alongside %s, which didn't answer within %s, so AgentBox restarted the server. Try it again.",
			cmpStr(other.tool, "This call"), g.Name, cmpStr(c.tool, "another call"), g.Timeout)
		if other == c {
			why = fmt.Sprintf("%s didn't answer within %s, so AgentBox stopped it and restarted the %s MCP server. "+
				"The page itself may be stuck (a script that never finishes, a dialog open, the page not responding): "+
				"take a screenshot of the display to see it before trying again, and reload or close the page if it is.",
				cmpStr(c.tool, "The call"), g.Timeout, g.Name)
		}
		g.answer(other.id, why)
	}
	g.kill()
	if err := g.start(); err != nil {
		g.fail(fmt.Errorf("restarting the %s MCP server: %w", g.Name, err))
		return
	}
	if g.init != nil {
		var req map[string]json.RawMessage
		if json.Unmarshal(g.init, &req) == nil {
			req["id"] = json.RawMessage(guardInitID(g.gen))
			if b, err := json.Marshal(req); err == nil {
				_, _ = g.child.stdin.Write(append(b, '\n'))
			}
		}
	}
	if g.inited != nil {
		_, _ = g.child.stdin.Write(g.inited)
	}
}

// answer tells the client its call failed. The guard is locked.
func (g *Guard) answer(id json.RawMessage, why string) {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{"content": []Content{Text(why)}, "isError": true},
	})
	if err == nil {
		_, _ = g.out.Write(append(b, '\n'))
	}
}

func (g *Guard) fail(err error) {
	select {
	case g.failed <- err:
	default:
	}
}

// start starts the server as the next generation. The guard is locked.
func (g *Guard) start() error {
	cmd := exec.Command(g.Command[0], g.Command[1:]...)
	cmd.Stderr = g.Stderr
	ownGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	g.gen++
	c := &guardChild{gen: g.gen, cmd: cmd, stdin: stdin, done: make(chan struct{})}
	g.child = c
	go func() {
		defer close(c.done)
		r := bufio.NewReader(stdout)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				g.mu.Lock()
				g.fromServer(c.gen, line)
				g.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		g.mu.Lock()
		current := g.child == c
		g.mu.Unlock()
		if current {
			g.fail(fmt.Errorf("the %s MCP server exited: %v", g.Name, cmd.ProcessState))
		}
	}()
	return nil
}

// kill ends the current server, and whatever it started. The guard is locked.
func (g *Guard) kill() {
	if g.child == nil {
		return
	}
	_ = g.child.stdin.Close()
	killGroup(g.child.cmd)
	g.child = nil
}

// stop ends the server once the client has gone: its stdin closes, which is
// how an MCP server is told to exit, and it is killed if it hasn't within a
// few seconds.
func (g *Guard) stop() {
	g.mu.Lock()
	c := g.child
	g.child = nil
	for k, call := range g.pending {
		call.timer.Stop()
		delete(g.pending, k)
	}
	g.mu.Unlock()
	if c == nil {
		return
	}
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		killGroup(c.cmd)
		<-c.done
	}
}

func cmpStr(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}
