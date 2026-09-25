//go:build integration

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/testutil"
)

// TestDaemonOnIncus needs Incus, scripts/host-setup.sh and a built base image:
//
//	agentbox image build && go test -tags integration ./internal/daemon
func TestDaemonOnIncus(t *testing.T) {
	ctx := context.Background()
	inc := incus.Client{}
	if ready, err := image.Ready(ctx, inc); err != nil || !ready {
		t.Skipf("base image not built (ready=%v, err=%v)", ready, err)
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	root := t.TempDir()
	binary := filepath.Join(root, "agentbox")
	if out, err := exec.Command("go", "build", "-o", binary, "../../cmd/agentbox").CombinedOutput(); err != nil {
		t.Fatalf("building agentbox: %v\n%s", err, out)
	}

	t.Setenv("AGENTBOX_SOCKET", "")
	p := paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")}
	srv, err := New(Config{Paths: p, Incus: inc, User: image.User{Name: u.Username, UID: uid, GID: gid}, Binary: binary, Log: logWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})
	c := api.NewClient(p.Socket())
	waitFor(t, "the daemon", func() bool { return c.Ping(ctx) == nil })

	if _, err := c.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	events := make(chan api.Event, 4096)
	go c.Events(runCtx, func(ev api.Event) error {
		select {
		case events <- ev:
		default:
		}
		return nil
	})

	// Create through a job.
	j, err := c.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none"})
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := c.FollowJobLog(ctx, j.ID, &log); err != nil {
		t.Fatal(err)
	}
	if j, err = c.Job(ctx, j.ID); err != nil || j.Status != api.JobSucceeded {
		t.Fatalf("create job = %+v, %v\n%s", j, err, log.String())
	}
	var ag api.Agent
	if err := json.Unmarshal(j.Result, &ag); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.DestroyAgent(context.Background(), ag.Ref, true, true, true) })
	if ag.State != "running" || ag.IP == "" {
		t.Errorf("created agent = %+v", ag)
	}

	// The in-agent API, through the proxy device and the copied binary.
	whoami, err := exec.Command("incus", "exec", ag.Instance, "--", "runuser", "-l", u.Username, "-c", "agentbox whoami").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(whoami), ag.Ref+"\n") {
		t.Errorf("agentbox whoami inside the agent: %v\n%s", err, whoami)
	}
	refused, _ := exec.Command("incus", "exec", ag.Instance, "--", "curl", "-s", "--unix-socket", api.InAgentSocket, "http://agentbox/v1/projects").CombinedOutput()
	if !strings.Contains(string(refused), "not available inside an agent") {
		t.Errorf("listing projects from inside the agent: %s", refused)
	}

	// Two terminal clients share one tmux session.
	agentPath, _ := api.AgentPath(ag.Ref)
	dial := func() *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, "ws://agentbox"+agentPath+"/terminal?cols=100&rows=30", &websocket.DialOptions{HTTPClient: c.HTTPClient()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	first, second := dial(), dial()
	readUntil := func(name string, conn *websocket.Conn, want string) {
		t.Helper()
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		var seen strings.Builder
		for !strings.Contains(seen.String(), want) {
			_, data, err := conn.Read(readCtx)
			if err != nil {
				t.Fatalf("%s terminal waiting for %q: %v; saw:\n%q", name, want, err, seen.String())
			}
			seen.Write(data)
		}
	}
	time.Sleep(time.Second)
	if err := first.Write(ctx, websocket.MessageText, []byte(`{"cols": 90, "rows": 25}`)); err != nil {
		t.Fatal(err)
	}
	if err := first.Write(ctx, websocket.MessageBinary, []byte("echo agentbox-$((6*7))\r")); err != nil {
		t.Fatal(err)
	}
	readUntil("first", first, "agentbox-42")
	readUntil("second", second, "agentbox-42")
	first.Close(websocket.StatusNormalClosure, "")
	second.Close(websocket.StatusNormalClosure, "")

	// State changes and resource samples arrive on the event stream.
	if _, err := c.AgentAction(ctx, ag.Ref, "pause"); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, "agent paused", func(ev api.Event) bool {
		var ch api.AgentChange
		return ev.Type == api.EventAgent && json.Unmarshal(ev.Data, &ch) == nil && ch.Ref == ag.Ref && ch.State == "paused"
	})
	if _, err := c.AgentAction(ctx, ag.Ref, "resume"); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, "a usage sample with the agent", func(ev api.Event) bool {
		var u api.Usage
		if ev.Type != api.EventUsage || json.Unmarshal(ev.Data, &u) != nil {
			return false
		}
		for _, a := range u.Agents {
			if a.Ref == ag.Ref && a.Memory > 0 {
				return true
			}
		}
		return false
	})
}

func waitForEvent(t *testing.T, events <-chan api.Event, what string, match func(api.Event) bool) {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev := <-events:
			if match(ev) {
				return
			}
		case <-timeout:
			t.Fatalf("no event: %s", what)
		}
	}
}

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
