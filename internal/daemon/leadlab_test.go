//go:build pressurelab

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// TestLeadChatLab runs two lead chats as real processes — each a fake ACP
// adapter, this test binary started again — through the daemon's own code:
// chat.StartCommand, capLeadChat placing each tree in a cgroup of its own
// under a real leads' parent with a small memory.max (LAB_LEADS_MB, 512 by
// default, no swap), and watchShared looking every second. One chat is asked
// to run a memory hog, as a lead's shell might; the test checks the kernel
// killed that chat's whole tree, its turn failed saying "killed: out of
// memory" and the leads' limit, the other chat's adapter is the same process
// and still answers, this process (the daemon) is untouched, and the next
// message starts the killed chat again. As root, in AgentBox's VM or an
// agent's machine:
//
//	sudo env PATH=$PATH go test -tags pressurelab -run TestLeadChatLab -v ./internal/daemon
func TestLeadChatLab(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to make cgroups")
	}
	limitMB := 512
	if v, err := strconv.Atoi(os.Getenv("LAB_LEADS_MB")); err == nil && v > 0 {
		limitMB = v
	}
	parent := filepath.Join("/sys/fs/cgroup", fmt.Sprintf("agentbox-leadlab.%d", os.Getpid()))
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		entries, _ := os.ReadDir(parent)
		for _, e := range entries {
			_ = agent.RemoveLeadChat(filepath.Join(parent, e.Name()), 5*time.Second)
		}
		_ = os.Remove(parent)
	})
	oldDir, oldOn := agent.LeadsDir, agent.SharedCgroups
	agent.LeadsDir, agent.SharedCgroups = parent, func() bool { return true }
	t.Cleanup(func() { agent.LeadsDir, agent.SharedCgroups = oldDir, oldOn })
	for file, v := range map[string]string{"cgroup.subtree_control": "+memory +pids", "memory.max": strconv.Itoa(limitMB << 20), "memory.swap.max": "0"} {
		if err := os.WriteFile(filepath.Join(parent, file), []byte(v), 0); err != nil {
			t.Fatal(err)
		}
	}

	// The daemon, with the real leads' functions, the agents' left alone.
	sc := newSharedCgroups()
	sc.leadsDir = parent
	sc.setAgents = func(int64) error { return nil }
	sc.setLeads = func(int64) error { return nil }
	sc.agentsDir = filepath.Join(parent, "no-agents")
	s := &Server{cfg: Config{Log: &labLog{t: t}}, shared: sc, loggedOnce: map[string]string{}, vmMemory: agent.HostMemory}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
				s.watchShared(s.vmMemory())
			}
		}
	}()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var mu sync.Mutex
	pids := map[string]int{}
	m := &chat.Manager{Store: store, Version: "lab", Launch: func(ctx context.Context, a state.Agent, status func(string)) (*chat.Process, error) {
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(self, "-test.run=^$")
		cmd.Env = append(os.Environ(), "LEADLAB_ADAPTER=1")
		proc, err := chat.StartCommand(cmd)
		if err != nil {
			return nil, err
		}
		s.capLeadChat(a, proc, cmd.Process.Pid)
		mu.Lock()
		pids[a.Ref()] = cmd.Process.Pid
		mu.Unlock()
		return proc, nil
	}}
	t.Cleanup(m.Wait)
	t.Cleanup(m.Close)
	hog := state.Agent{Project: "alpha", Name: state.LeadName, Role: state.RoleLead, AI: "claude", Worktree: t.TempDir()}
	calm := state.Agent{Project: "beta", Name: state.LeadName, Role: state.RoleLead, AI: "claude", Worktree: t.TempDir()}
	pidOf := func(a state.Agent) int {
		mu.Lock()
		defer mu.Unlock()
		return pids[a.Ref()]
	}

	say := func(a state.Agent, text string, turns int, d time.Duration) api.ChatThread {
		t.Helper()
		if _, err := m.Send(a, text); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(d)
		for {
			th, err := m.Thread(a)
			if err != nil {
				t.Fatal(err)
			}
			ended := 0
			for _, it := range th.Items {
				if it.Kind == "user" && it.Result != nil && it.Result.State != "" {
					ended++
				}
			}
			if ended >= turns {
				return th
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %q didn't end in %s", a.Ref(), text, d)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	lastText := func(th api.ChatThread, kind string) string {
		for i := len(th.Items) - 1; i >= 0; i-- {
			if th.Items[i].Kind == kind {
				return th.Items[i].Text
			}
		}
		return ""
	}

	if th := say(calm, "hello", 1, 30*time.Second); lastText(th, "assistant") != "pong" {
		t.Fatalf("calm's chat answered %q", lastText(th, "assistant"))
	}
	calmPID := pidOf(calm)
	if th := say(hog, "hello", 1, 30*time.Second); lastText(th, "assistant") != "pong" {
		t.Fatalf("hog's chat answered %q", lastText(th, "assistant"))
	}
	hogPID := pidOf(hog)
	t.Logf("leads' parent %s: memory.max %d MiB; calm's adapter pid %d, hog's %d, this daemon's %d", parent, limitMB, calmPID, hogPID, os.Getpid())
	for _, pid := range []int{calmPID, hogPID} {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
		t.Logf("pid %d is in %s", pid, strings.TrimSpace(string(b)))
	}
	self, _ := os.ReadFile("/proc/self/cgroup")
	t.Logf("the daemon is in %s", strings.TrimSpace(string(self)))

	start := time.Now()
	th := say(hog, "RUN-THE-HOG", 2, 60*time.Second)
	errText := lastText(th, "error")
	t.Logf("after %s, hog's turn failed with: %s", time.Since(start).Round(time.Millisecond), errText)
	t.Logf("hog's session error: %s", th.Session.Error)
	if !strings.Contains(errText, "Claude Code was killed: out of memory (the project chats together reached their limit of "+agent.HumanBytes(int64(limitMB)<<20)) {
		t.Errorf("hog's turn failed with %q", errText)
	}
	// Its whole tree is gone: the adapter and the hog under it.
	if err := syscall.Kill(hogPID, 0); err == nil {
		t.Errorf("hog's adapter %d is still running", hogPID)
	}
	if out, _ := exec.Command("pgrep", "-f", "leadlab-hog").Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Errorf("the hog is still running: %s", out)
	}
	if b, err := os.ReadFile(filepath.Join(parent, "memory.events.local")); err == nil {
		t.Logf("leads' parent memory.events.local: %s", strings.Join(strings.Fields(string(b)), " "))
	}
	// The other chat is the same process, and answers.
	if err := syscall.Kill(calmPID, 0); err != nil {
		t.Errorf("calm's adapter %d is gone: %v", calmPID, err)
	}
	if th := say(calm, "hello again", 2, 30*time.Second); lastText(th, "assistant") != "pong" || pidOf(calm) != calmPID {
		t.Errorf("calm's chat answered %q from pid %d (was %d)", lastText(th, "assistant"), pidOf(calm), calmPID)
	}
	// And the next message starts the killed one again.
	th = say(hog, "hello", 3, 30*time.Second)
	if lastText(th, "assistant") != "pong" || pidOf(hog) == hogPID {
		t.Errorf("hog's chat after the kill answered %q from pid %d (was %d)", lastText(th, "assistant"), pidOf(hog), hogPID)
	}
	t.Logf("hog's chat started again as pid %d and answered; the daemon, pid %d, ran throughout", pidOf(hog), os.Getpid())
}

type labLog struct{ t *testing.T }

func (l *labLog) Write(p []byte) (int, error) {
	l.t.Logf("daemon: %s", strings.TrimSpace(string(p)))
	return len(p), nil
}

// The fake adapter: this binary started again with LEADLAB_ADAPTER=1. It
// answers "pong" to anything but "hog", for which it runs a memory hog the
// way an AI tool's shell runs a command, and waits on it.
func init() {
	if os.Getenv("LEADLAB_ADAPTER") != "1" {
		return
	}
	a := &labAdapter{}
	a.conn = acp.NewConn(os.Stdin, os.Stdout, a)
	<-a.conn.Done()
	os.Exit(0)
}

type labAdapter struct {
	conn *acp.Conn
}

func (a *labAdapter) Notify(string, json.RawMessage) {}

func (a *labAdapter) Request(method string, params json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}, "agentInfo": map[string]any{"name": "leadlab", "version": "1"}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "lab"}, nil)
	case acp.MethodSessionPrompt:
		var req acp.PromptRequest
		_ = json.Unmarshal(params, &req)
		raw, _ := json.Marshal(req.Prompt)
		go func() {
			if strings.Contains(string(raw), `RUN-THE-HOG`) {
				hog := `import sys, time
b = []
while True:
    b.append(bytearray(64 << 20))
    for j in range(0, 64 << 20, 4096): b[-1][j] = 1
`
				_ = exec.Command("python3", "-c", hog, "leadlab-hog").Run()
				time.Sleep(time.Minute) // killed with it, or it hangs as an adapter would
			}
			_ = a.conn.Notify(acp.MethodSessionUpdate, map[string]any{"sessionId": "lab", "update": map[string]any{
				"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "pong"},
			}})
			reply(acp.PromptResponse{StopReason: "end_turn"}, nil)
		}()
	default:
		reply(nil, &acp.Error{Code: -32601, Message: "not in the lab: " + method})
	}
}
