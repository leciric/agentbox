package incus

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// fakeExec is an Incus that runs one command, an echo of its stdin, over the
// exec websockets, and pings each of them as incusd does: every interval,
// with a five-second deadline of its own.
type fakeExec struct {
	interval time.Duration
	mu       sync.Mutex
	conns    map[string]*websocket.Conn
	ready    chan struct{} // all four websockets are connected
	done     chan struct{} // the command has exited
	once     sync.Once
}

func startFakeExec(t *testing.T) *fakeExec {
	t.Helper()
	f := &fakeExec{interval: 50 * time.Millisecond, conns: map[string]*websocket.Conn{}, ready: make(chan struct{}), done: make(chan struct{})}
	socket := filepath.Join(t.TempDir(), "incus.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go func() { _ = srv.Serve(l) }()
	t.Setenv("INCUS_SOCKET", socket)
	resetShared()
	t.Cleanup(func() {
		f.exit()
		_ = srv.Close()
		resetShared()
	})
	return f
}

func (f *fakeExec) exit() { f.once.Do(func() { close(f.done) }) }

func (f *fakeExec) op(status api.StatusCode) api.Operation {
	op := api.Operation{ID: "exec", Class: "websocket", Status: status.String(), StatusCode: status,
		Metadata: map[string]any{"fds": map[string]any{"0": "s0", "1": "s1", "2": "s2", "control": "sc"}}}
	if status == api.Success {
		op.Metadata["return"] = 0
	}
	return op
}

func (f *fakeExec) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/1.0")
	switch path {
	case "":
		reply(w, api.Server{Environment: api.ServerEnvironment{Server: "incus"}})
	case "/instances/agent-01/exec":
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "async", "status": "Operation created", "status_code": 100,
			"operation": "/1.0/operations/exec", "metadata": f.op(api.Running),
		})
	case "/operations/exec":
		select {
		case <-f.done:
			reply(w, f.op(api.Success))
		default:
			reply(w, f.op(api.Running))
		}
	case "/operations/exec/wait":
		select {
		case <-f.done:
		case <-r.Context().Done():
		}
		reply(w, f.op(api.Success))
	case "/operations/exec/websocket":
		f.upgrade(w, r)
	default:
		replyError(w, http.StatusNotFound, "not found")
	}
}

func (f *fakeExec) upgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	fd := strings.TrimPrefix(r.URL.Query().Get("secret"), "s")
	go func() { // incusd's keepalive
		for {
			if conn.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(5*time.Second)) != nil {
				return
			}
			select {
			case <-time.After(f.interval):
			case <-f.done:
				return
			}
		}
	}()
	f.mu.Lock()
	f.conns[fd] = conn
	if len(f.conns) == 4 {
		close(f.ready)
	}
	f.mu.Unlock()
	switch fd {
	case "0":
		<-f.ready
		f.mu.Lock()
		out, errs := f.conns["1"], f.conns["2"]
		f.mu.Unlock()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil || kind == websocket.TextMessage { // stdin closed
				break
			}
			_ = out.WriteMessage(websocket.BinaryMessage, data)
		}
		_ = out.WriteMessage(websocket.TextMessage, []byte{})
		_ = errs.WriteMessage(websocket.TextMessage, []byte{})
		f.exit()
	default:
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}
}

// stall makes every write on the connections the client dials wait while
// it's on, as a socket does when its host is too loaded to run the reader, or
// the writer: a write's deadline passes and it fails, as the real one did,
// with "write unix @->…/incus.sock: i/o timeout".
type stall struct {
	mu  sync.Mutex
	off chan struct{} // closed while writes go through
}

func newStall(t *testing.T) *stall {
	s := &stall{off: make(chan struct{})}
	close(s.off)
	srv, err := (Client{}).server(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hc, err := srv.GetHTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	transport := hc.Transport.(*http.Transport)
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &stallConn{Conn: conn, stall: s}, nil
	}
	return s
}

func (s *stall) start() {
	s.mu.Lock()
	s.off = make(chan struct{})
	s.mu.Unlock()
}

func (s *stall) stop() {
	s.mu.Lock()
	close(s.off)
	s.mu.Unlock()
}

type stallConn struct {
	net.Conn
	stall    *stall
	mu       sync.Mutex
	deadline time.Time
}

func (c *stallConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}

func (c *stallConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

func (c *stallConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	c.stall.mu.Lock()
	off := c.stall.off
	c.stall.mu.Unlock()
	var expired <-chan time.Time
	if !deadline.IsZero() {
		expired = time.After(time.Until(deadline))
	}
	select {
	case <-off:
	case <-expired:
		return 0, &net.OpError{Op: "write", Net: "unix", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: os.ErrDeadlineExceeded}
	}
	return c.Conn.Write(p)
}

// stallFor stalls the client's writes for longer than the second its pongs
// get, while the server pings it: what a host short of memory does to it.
func stallFor(s *stall) {
	s.start()
	time.Sleep(1500 * time.Millisecond)
	s.stop()
}

// echoes writes line to stdin and reports whether it comes back on stdout
// within a few seconds.
func echoes(t *testing.T, stdin interface{ Write([]byte) (int, error) }, stdout *bufio.Reader, line string) bool {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		s, _ := stdout.ReadString('\n')
		got <- s
	}()
	go func() { _, _ = stdin.Write([]byte(line + "\n")) }()
	select {
	case s := <-got:
		return s == line+"\n"
	case <-time.After(3 * time.Second):
		return false
	}
}

// What `incus exec -T` does, through Incus' own client: one stall past the
// pong's one-second deadline and stdin is gone for good. This is the chat's
// "Claude Code exited (exit status 1): Error: write unix
// @->/var/lib/incus/unix.socket: i/o timeout".
func TestIncusExecDiesOnAStalledPong(t *testing.T) {
	f := startFakeExec(t)
	s := newStall(t)
	srv, err := (Client{}).server(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stdinR, stdinW, _ := os.Pipe()
	stdoutR, stdoutW, _ := os.Pipe()
	t.Cleanup(func() { _ = stdinW.Close(); _ = stdoutR.Close() })
	_, err = srv.ExecInstance("agent-01", api.InstanceExecPost{Command: []string{"cat"}, WaitForWS: true},
		&incusclient.InstanceExecArgs{Stdin: stdinR, Stdout: stdoutW, Stderr: stdoutW})
	if err != nil {
		t.Fatal(err)
	}
	<-f.ready
	out := bufio.NewReader(stdoutR)
	if !echoes(t, stdinW, out, "before") {
		t.Fatal("the echo didn't work before the stall")
	}
	stallFor(s)
	if echoes(t, stdinW, out, "after") {
		t.Fatal("Incus' own exec survived the stall; the chat's stream would too, and StartExec is no longer needed")
	}
}

// StartExec's stream outlives the same stall: its pongs are late, not fatal.
func TestStartExecSurvivesAStalledPong(t *testing.T) {
	f := startFakeExec(t)
	s := newStall(t)
	var stderr strings.Builder
	p, err := (Client{}).StartExec("agent-01", []string{"cat"}, &lockedWriter{w: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	<-f.ready
	out := bufio.NewReader(p.Stdout)
	if !echoes(t, p.Stdin, out, "before") {
		t.Fatal("the echo didn't work before the stall")
	}
	stallFor(s)
	if !echoes(t, p.Stdin, out, "after") {
		t.Fatal("the stream died in the stall")
	}
	_ = p.Stdin.Close()
	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait didn't return after stdin closed")
	}
}

// Kill ends a command that won't exit, and Wait returns.
func TestStartExecKill(t *testing.T) {
	f := startFakeExec(t)
	p, err := (Client{}).StartExec("agent-01", []string{"cat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-f.ready
	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()
	p.Kill()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("Wait after Kill returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait didn't return after Kill")
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
