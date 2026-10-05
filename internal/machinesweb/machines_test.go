package machinesweb

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/machines"
)

// fakeMachines has one running machine and one stopped. Its VNC server
// greets, then echoes what it's sent.
type fakeMachines struct {
	mu      sync.Mutex
	list    []machines.Status
	calls   []string
	release chan struct{} // holds Start until closed, when set
	dialed  chan net.Conn
	failVNC error
}

func newFakeMachines() *fakeMachines {
	return &fakeMachines{
		list: []machines.Status{
			{Name: "agentbox-machine-app-1", Worktree: "/src/app", Exists: true, Running: true, Memory: "4096m", Started: time.Now().Add(-time.Hour)},
			{Name: "agentbox-machine-api-2", Worktree: "/src/api", Exists: true},
		},
		dialed: make(chan net.Conn, 4),
	}
}

func (m *fakeMachines) List(context.Context) ([]machines.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]machines.Status(nil), m.list...), nil
}

func (m *fakeMachines) MemoryUsage(_ context.Context, names []string) (map[string]string, error) {
	if len(names) != 1 || names[0] != "agentbox-machine-app-1" {
		return nil, errors.New("asked about machines that don't run")
	}
	return map[string]string{"agentbox-machine-app-1": "312MiB"}, nil
}

func (m *fakeMachines) set(worktree string, running bool, call string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call+" "+worktree)
	for i := range m.list {
		if m.list[i].Worktree == worktree {
			m.list[i].Running = running
		}
	}
}

func (m *fakeMachines) Start(ctx context.Context, worktree string) error {
	if m.release != nil {
		<-m.release
	}
	if worktree == "/src/api" && m.failVNC != nil {
		return errors.New("no image")
	}
	m.set(worktree, true, "start")
	return nil
}

func (m *fakeMachines) Stop(_ context.Context, worktree string) error {
	m.set(worktree, false, "stop")
	return nil
}

func (m *fakeMachines) DialVNC(_ context.Context, worktree string) (io.ReadWriteCloser, error) {
	if m.failVNC != nil {
		return nil, m.failVNC
	}
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		if _, err := server.Write([]byte("RFB 003.008\n")); err != nil {
			return
		}
		_, _ = io.Copy(server, server)
	}()
	m.dialed <- client
	return client, nil
}

func machinesFixture(t *testing.T) (*fixture, *fakeMachines) {
	f := newFixture(t, nil)
	m := newFakeMachines()
	f.srv.Machines = m
	return f, m
}

func TestMachinesListed(t *testing.T) {
	f, _ := machinesFixture(t)
	var got MachineList
	f.json("/api/machines", &got)
	if got.Error != "" || len(got.Machines) != 2 {
		t.Fatalf("%+v", got)
	}
	app := got.Machines[0]
	if app.Name != "agentbox-machine-app-1" || !app.Running || app.Memory != "312MiB" || app.Limit != "4096m" || app.Started.IsZero() {
		t.Errorf("running machine: %+v", app)
	}
	if api := got.Machines[1]; api.Running || api.Memory != "" || !api.Started.IsZero() {
		t.Errorf("stopped machine: %+v", api)
	}
}

func TestMachinesWithoutRuntime(t *testing.T) {
	f := newFixture(t, nil)
	var got MachineList
	f.json("/api/machines", &got)
	if got.Machines == nil || len(got.Machines) != 0 || !strings.Contains(got.Error, "Docker or Podman") {
		t.Errorf("%+v", got)
	}
	if resp := f.do("POST", "/api/machines/x/start", "X-Machines", "1"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("start without a runtime: %d", resp.StatusCode)
	}
}

func TestMachineStartAndStop(t *testing.T) {
	f, m := machinesFixture(t)
	m.release = make(chan struct{})
	if resp := f.do("POST", "/api/machines/agentbox-machine-api-2/start"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("start without X-Machines: %d", resp.StatusCode)
	}
	if resp := f.do("POST", "/api/machines/nope/start", "X-Machines", "1"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("start of an unknown machine: %d", resp.StatusCode)
	}
	if resp := f.do("POST", "/api/machines/agentbox-machine-api-2/start", "X-Machines", "1"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	busy := func() string {
		var got MachineList
		f.json("/api/machines", &got)
		return got.Machines[1].Busy
	}
	if b := busy(); b != "starting" {
		t.Errorf("busy = %q while it starts", b)
	}
	if resp := f.do("POST", "/api/machines/agentbox-machine-api-2/stop", "X-Machines", "1"); resp.StatusCode != http.StatusConflict {
		t.Errorf("stop while it starts: %d", resp.StatusCode)
	}
	close(m.release)
	waitFor(t, func() bool { return busy() == "" })

	if resp := f.do("POST", "/api/machines/agentbox-machine-app-1/stop", "X-Machines", "1"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	waitFor(t, func() bool {
		var got MachineList
		f.json("/api/machines", &got)
		return !got.Machines[0].Running && got.Machines[0].Busy == ""
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.Join(m.calls, ",") != "start /src/api,stop /src/app" {
		t.Errorf("calls: %v", m.calls)
	}
}

func TestMachineStartFailureShown(t *testing.T) {
	f, m := machinesFixture(t)
	m.failVNC = errors.New("x") // makes the fake's start of /src/api fail
	if resp := f.do("POST", "/api/machines/agentbox-machine-api-2/start", "X-Machines", "1"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	waitFor(t, func() bool {
		var got MachineList
		f.json("/api/machines", &got)
		return got.Machines[1].Error == "no image" && got.Machines[1].Busy == ""
	})
}

func dialView(t *testing.T, f *fixture, name string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.http.URL, "http")+"/api/machines/"+name+"/view", opts)
}

// TestViewProxiesVNC relays the VNC protocol's bytes both ways, in binary
// frames, and hangs up on the machine when the page goes.
func TestViewProxiesVNC(t *testing.T) {
	f, m := machinesFixture(t)
	conn, _, err := dialView(t, f, "agentbox-machine-app-1", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {f.http.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "RFB 003.008\n" {
		t.Fatalf("greeting: %v %q %v", typ, data, err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{3, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, data, err := conn.Read(ctx); err != nil || string(data) != "\x03\x00\x00\x00" {
		t.Fatalf("echo: %q %v", data, err)
	}
	vnc := <-m.dialed
	_ = conn.Close(websocket.StatusNormalClosure, "")
	_ = vnc.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := vnc.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("the VNC connection outlived the page's: %v", err)
	}
}

// TestViewRefusesOtherOrigins: any page the user visits could open a
// WebSocket to 127.0.0.1, and its Origin says so.
func TestViewRefusesOtherOrigins(t *testing.T) {
	f, m := machinesFixture(t)
	_, resp, err := dialView(t, f, "agentbox-machine-app-1", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin: %v %v", resp, err)
	}
	// A rebinding page's own origin and Host agree, but the Host isn't loopback.
	_, resp, err = dialView(t, f, "agentbox-machine-app-1", &websocket.DialOptions{
		Host: "evil.example", HTTPHeader: http.Header{"Origin": {"http://evil.example"}},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign host: %v %v", resp, err)
	}
	if len(m.dialed) != 0 {
		t.Error("dialed the machine for a refused page")
	}
}

func TestViewOfStoppedOrUnknownMachine(t *testing.T) {
	f, _ := machinesFixture(t)
	if resp := f.do("GET", "/api/machines/agentbox-machine-api-2/view"); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("a stopped machine: %d", resp.StatusCode)
	}
	if resp := f.do("GET", "/api/machines/nope/view"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown machine: %d", resp.StatusCode)
	}
}

func TestViewClosesWithTheDialError(t *testing.T) {
	f, m := machinesFixture(t)
	m.failVNC = errors.New("connect ECONNREFUSED 127.0.0.1:5900")
	conn, _, err := dialView(t, f, "agentbox-machine-app-1", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {f.http.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.Read(context.Background())
	if websocket.CloseStatus(err) != websocket.StatusInternalError || !strings.Contains(err.Error(), "ECONNREFUSED") {
		t.Errorf("close: %v", err)
	}
}

func TestRunningFile(t *testing.T) {
	f, _ := machinesFixture(t)
	path := filepath.Join(t.TempDir(), "machines", "serve.json")
	ctx := context.Background()
	if _, ok := Running(ctx, path); ok {
		t.Fatal("running before it was written")
	}
	remove, err := WriteRunning(path, f.http.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if url, ok := Running(ctx, path); !ok || url != f.http.URL+"/" {
		t.Errorf("Running = %q, %v", url, ok)
	}
	f.http.Close()
	if _, ok := Running(ctx, path); ok {
		t.Error("a serve that stopped answering is running")
	}
	remove()
	if _, err := readRunning(path); err == nil {
		t.Error("not removed")
	}
}
