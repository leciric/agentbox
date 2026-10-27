package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/api"
	"agentbox/internal/gateway"
	"agentbox/internal/state"
)

// testPodRef is the cluster agent these tests run commands on.
var testPodRef = gateway.Ref{Project: "demo", Agent: "agent-01"}

// testPodToken is what the store is told the pod authenticates with.
const testPodToken = "fedcba9876543210fedcba9876543210"

// TestExecRouteRunsACommand is the whole point of the route: a command on a
// cluster agent, its two output streams kept apart, and the status it exited
// with, which a non-zero one proves survives the trip.
func TestExecRouteRunsACommand(t *testing.T) {
	t.Parallel()
	d := newPodDaemon(t, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := d.client.ExecStream(ctx, testPodRef.String(), "cat")
	if err != nil {
		t.Fatalf("ExecStream() = %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello\n")); err != nil {
		t.Fatalf("writing stdin: %v", err)
	}
	eof, err := json.Marshal(api.ExecStdin{Stdin: api.ExecStdinEOF})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, eof); err != nil {
		t.Fatalf("closing stdin: %v", err)
	}

	stdout, stderr, code := readExecStream(t, ctx, conn)
	// The daemon runs the line the way the incus path does, in the agent's
	// worktree, so the pod's own sh gets the cd before the command.
	if want := "sh -lc cd '/workspace' && cat\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want the command the pod was given, %q", stdout, want)
	}
	if !strings.Contains(stdout, "hello\n") {
		t.Errorf("stdout = %q, want the bytes sent on stdin", stdout)
	}
	if stderr != "a warning\n" {
		t.Errorf("stderr = %q, want the pod's own warning", stderr)
	}
	if code != 7 {
		t.Errorf("exit = %d, want 7", code)
	}
}

// A command that succeeds ends the stream with 0, the same as the incus path.
func TestExecRouteEndsAZeroExit(t *testing.T) {
	t.Parallel()
	d := newPodDaemon(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := d.client.ExecStream(ctx, testPodRef.String(), "true")
	if err != nil {
		t.Fatalf("ExecStream() = %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	// The client's end of stdin is a frame of its own, and a command that reads
	// none still waits for it.
	eof, err := json.Marshal(api.ExecStdin{Stdin: api.ExecStdinEOF})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, eof); err != nil {
		t.Fatalf("closing stdin: %v", err)
	}
	if _, _, code := readExecStream(t, ctx, conn); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// A stopped agent is refused before the upgrade, with the sentence every other
// route gives: the CLI shows that rather than a socket that closed under it.
func TestExecRouteRefusesAStoppedAgent(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	addPodProject(t, d)
	if err := d.srv.store.AddAgent(ctx, state.Agent{
		Project: "demo", Name: "agent-02", Cluster: "e2e", AI: "none",
		Branch: "agentbox/agent-02", Status: state.AgentReady, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, err := d.client.ExecStream(ctx, "demo/agent-02", "true")
	if err == nil {
		t.Fatal("ExecStream() on a stopped agent was accepted")
	}
	if want := "demo/agent-02 is stopped: run agentbox start demo/agent-02"; err.Error() != want {
		t.Errorf("ExecStream() = %q, want %q", err, want)
	}
}

// newPodDaemon is a daemon with one running cluster agent whose pod answers
// every command with code: its argv and its stdin on stdout, a line on stderr.
func newPodDaemon(t *testing.T, code int) testDaemon {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	addPodProject(t, d)
	if err := d.srv.store.AddAgent(ctx, state.Agent{
		Project: testPodRef.Project, Name: testPodRef.Agent, Cluster: "e2e", AI: "none",
		Branch: "agentbox/agent-01", Pod: "demo-agent-01", DesiredState: state.DesiredRunning,
		Status: state.AgentReady, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetAgentToken(ctx, testPodRef.Project, testPodRef.Agent, testPodToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	startPodHandle(t, d.srv.gateway, d.srv.routes(), testPodToken, podCommand(code))
	return d
}

// addPodProject is the project the test's cluster agents run in: a row, since
// this is about the route and not the project's files.
func addPodProject(t *testing.T, d testDaemon) {
	t.Helper()
	if err := d.srv.store.AddProject(context.Background(), state.Project{Name: "demo", Root: filepath.Join(t.TempDir(), "demo")}); err != nil {
		t.Fatal(err)
	}
}

// startPodHandle connects a pod whose handler is handle to h, as a cluster
// does, and waits for the daemon to have its session.
func startPodHandle(t *testing.T, gw *gateway.Server, h http.Handler, token string, handle func(context.Context, gateway.Header, *gateway.Stream) error) {
	t.Helper()
	hs := httptest.NewServer(h)
	t.Cleanup(hs.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pod := &gateway.Client{URL: hs.URL, Token: token, Handle: handle}
	go func() { _ = pod.Run(ctx) }()
	if err := gw.WaitFor(ctx, testPodRef, 10*time.Second); err != nil {
		t.Fatalf("the pod never connected: %v", err)
	}
}

// podCommand is a pod running one command: it reads its stdin to the end,
// prints its argv and then its stdin on stdout, a line on stderr, and exits
// with code.
func podCommand(code int) func(context.Context, gateway.Header, *gateway.Stream) error {
	return func(_ context.Context, h gateway.Header, s *gateway.Stream) error {
		var stdin strings.Builder
		for {
			kind, payload, err := s.Read()
			if err != nil {
				break
			}
			if kind == gateway.FrameStdin {
				stdin.Write(payload)
			}
		}
		if err := s.Write(gateway.FrameStdout, []byte(strings.Join(h.Argv, " ")+"\n")); err != nil {
			return err
		}
		if err := s.Write(gateway.FrameStdout, []byte(stdin.String())); err != nil {
			return err
		}
		if err := s.Write(gateway.FrameStderr, []byte("a warning\n")); err != nil {
			return err
		}
		return s.Exit(code)
	}
}

// readExecStream reads a stream to its exit frame, and returns what each of
// the command's streams printed and the status it ended with.
func readExecStream(t *testing.T, ctx context.Context, conn *websocket.Conn) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut strings.Builder
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		if typ == websocket.MessageBinary {
			stream, payload, err := api.SplitExecOutput(data)
			if err != nil {
				t.Fatalf("reading an output frame: %v", err)
			}
			if stream == api.ExecStderr {
				errOut.Write(payload)
			} else {
				out.Write(payload)
			}
			continue
		}
		var exit api.ExecExit
		if err := json.Unmarshal(data, &exit); err != nil {
			t.Fatalf("the last frame %q: %v", data, err)
		}
		if exit.Error != "" {
			t.Fatalf("the command failed: %s", exit.Error)
		}
		return out.String(), errOut.String(), exit.Exit
	}
}
