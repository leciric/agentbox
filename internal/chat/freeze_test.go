package chat

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Freezing an agent's machine for memory (daemon/agentfreeze.go) stops its
// processes where they are, the ACP adapter among them, while the chat keeps
// its end of the pipes. These tests put a real process between the chat and
// a fake tool, a relay copying both ways as the adapter does, freeze it mid
// turn, and check that the turn carries on when it thaws, with nothing lost,
// and that a message sent meanwhile is handed over.
//
// The freeze is the cgroup freezer when AGENTBOX_TEST_CGROUP names a cgroup
// the test may make children in (what the daemon writes), and SIGSTOP
// otherwise, which stops the process the same way as far as its pipes go.

// relayEnv makes the test binary the relay: fd 0 to fd 3, fd 4 to fd 1.
const relayEnv = "AGENTBOX_TEST_ACP_RELAY"

func init() {
	if os.Getenv(relayEnv) != "1" {
		return
	}
	toTool, fromTool := os.NewFile(3, "to-tool"), os.NewFile(4, "from-tool")
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(toTool, os.Stdin); done <- struct{}{} }()
	go func() { _, _ = io.Copy(os.Stdout, fromTool); done <- struct{}{} }()
	<-done
	os.Exit(0)
}

// relayed launches f behind a relay process, and reports it as it starts.
func relayed(t *testing.T, f *fakeTool, started chan<- *os.Process) Launcher {
	return func(_ context.Context, a state.Agent, status func(string)) (*Process, error) {
		toolIn, relayOut, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		relayIn, toolOut, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), relayEnv+"=1")
		cmd.ExtraFiles = []*os.File{relayOut, relayIn}
		p, err := StartCommand(cmd)
		if err != nil {
			return nil, err
		}
		_ = relayOut.Close()
		_ = relayIn.Close()
		f.mu.Lock()
		f.conn = acp.NewConn(toolIn, toolOut, f)
		f.stop = func() { _ = toolOut.Close(); _ = toolIn.Close() }
		f.mu.Unlock()
		stop := p.Stop
		p.Stop = func() {
			_ = cmd.Process.Signal(syscall.SIGCONT)
			stop()
			_ = toolOut.Close()
		}
		started <- cmd.Process
		return p, nil
	}
}

// freezer freezes and thaws a process as the daemon does a machine.
type freezer struct {
	t   *testing.T
	pid int
	dir string // the cgroup, "" for SIGSTOP
}

func newFreezer(t *testing.T, proc *os.Process) *freezer {
	fz := &freezer{t: t, pid: proc.Pid}
	parent := os.Getenv("AGENTBOX_TEST_CGROUP")
	if parent == "" {
		return fz
	}
	dir := filepath.Join(parent, "acp-"+strconv.Itoa(proc.Pid))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("making %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(proc.Pid)), 0); err != nil {
		t.Fatalf("moving the relay into %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte("0"), 0)
		_ = os.WriteFile(filepath.Join(parent, "cgroup.procs"), []byte(strconv.Itoa(proc.Pid)), 0)
		_ = os.Remove(dir)
	})
	fz.dir = dir
	return fz
}

func (fz *freezer) set(on bool) {
	fz.t.Helper()
	if fz.dir == "" {
		sig := syscall.SIGCONT
		if on {
			sig = syscall.SIGSTOP
		}
		if err := syscall.Kill(fz.pid, sig); err != nil {
			fz.t.Fatal(err)
		}
		return
	}
	v := "0"
	if on {
		v = "1"
	}
	if err := os.WriteFile(filepath.Join(fz.dir, "cgroup.freeze"), []byte(v), 0); err != nil {
		fz.t.Fatal(err)
	}
	// The freezer is asynchronous: wait until it says it is done.
	want := "frozen " + v
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		b, _ := os.ReadFile(filepath.Join(fz.dir, "cgroup.events"))
		if strings.Contains(string(b), want) {
			return
		}
	}
	fz.t.Fatalf("the cgroup never said %q", want)
}

func messageText(th api.ChatThread) string {
	var b strings.Builder
	for _, it := range th.Items {
		if it.Kind == "assistant" {
			b.WriteString(it.Text)
		}
	}
	return b.String()
}

func TestAFrozenAdapterCarriesItsTurnOnWhenItThaws(t *testing.T) {
	if os.Getenv(relayEnv) == "1" {
		return
	}
	f := newSteeringTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Thinking"}}`)
		// The model takes a while: the machine is frozen meanwhile.
		time.Sleep(500 * time.Millisecond)
		f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":" and done"}}`)
		select {
		case <-f.steers:
			f.update(s, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":", and heard you"}}`)
		case <-time.After(5 * time.Second):
		}
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	started := make(chan *os.Process, 1)
	store := openStore(t)
	m, _ := newManager(t, store, f)
	m.Launch = relayed(t, f, started)
	if _, err := m.Send(testAgent, "work"); err != nil {
		t.Fatal(err)
	}
	proc := <-started
	fz := newFreezer(t, proc)
	waitThread(t, m, testAgent, "the turn to begin", func(th api.ChatThread) bool {
		return strings.Contains(messageText(th), "Thinking")
	})
	fz.set(true)
	thawed := false
	defer func() {
		if !thawed {
			fz.set(false)
		}
	}()

	// Frozen well past the model's answer: nothing more arrives, and the
	// chat neither ends the turn nor calls its tool gone.
	time.Sleep(1500 * time.Millisecond)
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messageText(th), "done") {
		t.Fatal("the turn went on while its adapter was frozen: the freeze didn't hold it")
	}
	if th.Session.TurnStartedAt == nil || th.Session.State == "error" {
		t.Fatalf("frozen, the turn ended or failed: %s", summary(th))
	}
	// A message for the frozen agent, as the daemon holds it.
	if _, err := m.HoldMessage(testAgent, api.ChatMessageRequest{Text: "also this"}); err != nil {
		t.Fatal(err)
	}

	fz.set(false)
	thawed = true
	th = waitThread(t, m, testAgent, "the turn to carry on and end", turnsEnded(1))
	if got := messageText(th); got != "Thinking and done, and heard you" {
		t.Fatalf("after thawing the turn said %q", got)
	}
	if th.Session.State == "error" {
		t.Fatalf("the session failed: %s", th.Session.Error)
	}
	// The same process carried on: nothing was relaunched.
	if f.sessions != 1 {
		t.Fatalf("%d sessions, want the one that was frozen", f.sessions)
	}
}
