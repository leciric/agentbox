package cli

// Tests for the half of `agentbox exec` and `agentbox shell` that talks to the
// daemon, without one: a stub serves the exec route's protocol, which is what
// the CLI has to read and write correctly. A daemon and a real pod are exercised
// by the daemon's own tests and on a cluster.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// TestClusterExecCopiesTheStreamsAndTheExitCode is the command end to end as
// the CLI sees it: stdin goes out as frames, stdout and stderr arrive apart,
// and the code comes back as the exit code the incus path returns.
func TestClusterExecCopiesTheStreamsAndTheExitCode(t *testing.T) {
	t.Parallel()
	// A command's output arrives in whatever size the daemon's own buffers
	// filled: larger than the WebSocket library's default read limit, which a
	// big build would otherwise end with "message too big".
	big := strings.Repeat("x", 40000)
	hs := execStub(t, func(ctx context.Context, conn *websocket.Conn, command string) {
		var stdin strings.Builder
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				var eof api.ExecStdin
				if json.Unmarshal(data, &eof) == nil && eof.Stdin == api.ExecStdinEOF {
					break
				}
				continue
			}
			stdin.Write(data)
		}
		_ = conn.Write(ctx, websocket.MessageBinary, api.ExecOutput(api.ExecStdout, []byte(command+"\n")))
		_ = conn.Write(ctx, websocket.MessageBinary, api.ExecOutput(api.ExecStdout, []byte(stdin.String())))
		_ = conn.Write(ctx, websocket.MessageBinary, api.ExecOutput(api.ExecStdout, []byte(big)))
		_ = conn.Write(ctx, websocket.MessageBinary, api.ExecOutput(api.ExecStderr, []byte("a warning\n")))
		exit, _ := json.Marshal(api.ExecExit{Exit: 7})
		_ = conn.Write(ctx, websocket.MessageText, exit)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})

	out, errOut, err := runClusterExec(t, hs, "cat")
	var code exitCodeError
	if !errors.As(err, &code) || int(code) != 7 {
		t.Fatalf("clusterExec() = %v, want exit status 7", err)
	}
	if want := "cat\ntyped\n" + big; out != want {
		t.Errorf("stdout = %d bytes, want %d", len(out), len(want))
	}
	if want := "a warning\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

// A command that never ran comes back as the daemon's reason, not as a stream
// that broke: the message is shown and the status it sent is kept.
func TestClusterExecReportsACommandThatNeverRan(t *testing.T) {
	t.Parallel()
	hs := execStub(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
		exit, _ := json.Marshal(api.ExecExit{Exit: 1, Error: "demo/agent-01 is stopped: run agentbox start demo/agent-01"})
		_ = conn.Write(ctx, websocket.MessageText, exit)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})

	_, errOut, err := runClusterExec(t, hs, "true")
	var code exitCodeError
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("clusterExec() = %v, want exit status 1", err)
	}
	if want := "error: demo/agent-01 is stopped: run agentbox start demo/agent-01\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

// execStub serves one command's stream, and answers it as answer says.
func execStub(t *testing.T, answer func(ctx context.Context, conn *websocket.Conn, command string)) *httptest.Server {
	t.Helper()
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		answer(r.Context(), conn, r.URL.Query().Get("command"))
	}))
	t.Cleanup(hs.Close)
	return hs
}

// runClusterExec runs one command through the CLI's cluster path against hs,
// with "typed" on its stdin, and returns what each stream got and the error.
func runClusterExec(t *testing.T, hs *httptest.Server, command string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader("typed\n"))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	c := api.NewRemoteClient(hs.URL, "")
	err = clusterExec(context.Background(), cmd, c, api.Agent{Ref: "demo/agent-01", Cluster: "e2e"}, command)
	return out.String(), errOut.String(), err
}
