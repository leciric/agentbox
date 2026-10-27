package machine_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/machine"
)

const podToken = "0123456789abcdef0123456789abcdef"

var podRef = gateway.Ref{Project: "pawly", Agent: "agent-01"}

// testPod is the pod's side of every case: it records what the machine sent
// and answers with output the test chose.
type testPod struct {
	mu      sync.Mutex
	files   map[string][]byte
	headers []gateway.Header
	typed   []byte
	resizes [][2]uint16
	archive []byte
}

func (p *testPod) handle(ctx context.Context, h gateway.Header, s *gateway.Stream) error {
	p.mu.Lock()
	p.headers = append(p.headers, h)
	p.mu.Unlock()
	switch h.Op {
	case gateway.OpExec:
		stdin, err := io.ReadAll(gateway.NewFrameReader(s, gateway.FrameStdin))
		if err != nil {
			return err
		}
		command := strings.Join(h.Argv, " ")
		switch {
		case command == "cat":
			if len(stdin) > 0 {
				if err := s.Write(gateway.FrameStdout, stdin); err != nil {
					return err
				}
			}
			return s.Exit(0)
		case command == "fail":
			if err := s.Write(gateway.FrameStdout, []byte("boom")); err != nil {
				return err
			}
			if err := s.Write(gateway.FrameStderr, []byte("why")); err != nil {
				return err
			}
			return s.Exit(3)
		case len(h.Argv) > 0 && strings.HasPrefix(h.Argv[len(h.Argv)-1], "tar -C "):
			p.mu.Lock()
			archive := p.archive
			p.mu.Unlock()
			if len(archive) > 0 {
				if err := s.Write(gateway.FrameStdout, archive); err != nil {
					return err
				}
			}
			return s.Exit(0)
		default:
			if err := s.Write(gateway.FrameStdout, []byte("hello\n")); err != nil {
				return err
			}
			return s.Exit(0)
		}
	case gateway.OpWrite:
		body, err := io.ReadAll(gateway.NewFrameReader(s, gateway.FrameData))
		if err != nil {
			return err
		}
		p.mu.Lock()
		p.files[h.Path] = body
		p.mu.Unlock()
		return s.Exit(0)
	case gateway.OpRead:
		p.mu.Lock()
		body, ok := p.files[h.Path]
		p.mu.Unlock()
		if !ok {
			_ = s.Write(gateway.FrameStderr, []byte("no such file"))
			return s.Exit(1)
		}
		if len(body) > 0 {
			if err := s.Write(gateway.FrameData, body); err != nil {
				return err
			}
		}
		return s.Exit(0)
	case gateway.OpAttach:
		if err := s.Write(gateway.FrameStdout, []byte("welcome")); err != nil {
			return err
		}
		for {
			kind, payload, err := s.Read()
			if err != nil {
				return nil
			}
			switch kind {
			case gateway.FrameStdin:
				p.mu.Lock()
				p.typed = append(p.typed, payload...)
				p.mu.Unlock()
				if err := s.Write(gateway.FrameStdout, payload); err != nil {
					return err
				}
			case gateway.FrameResize:
				if len(payload) < 4 {
					return fmt.Errorf("a resize frame of %d bytes", len(payload))
				}
				cols := binary.BigEndian.Uint16(payload[0:2])
				rows := binary.BigEndian.Uint16(payload[2:4])
				p.mu.Lock()
				p.resizes = append(p.resizes, [2]uint16{cols, rows})
				p.mu.Unlock()
				if err := s.Write(gateway.FrameStdout, []byte(fmt.Sprintf("resized %dx%d", cols, rows))); err != nil {
					return err
				}
				return s.Exit(0)
			}
		}
	case gateway.OpDial:
		for {
			kind, payload, err := s.Read()
			if err != nil {
				return nil
			}
			if kind != gateway.FrameData {
				continue
			}
			if err := s.Write(gateway.FrameData, payload); err != nil {
				return nil
			}
		}
	}
	return fmt.Errorf("unexpected op %q", h.Op)
}

func (p *testPod) last(op string) gateway.Header {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.headers) - 1; i >= 0; i-- {
		if p.headers[i].Op == op {
			return p.headers[i]
		}
	}
	return gateway.Header{}
}

func (p *testPod) file(path string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.files[path]
}

func (p *testPod) setFile(path string, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[path] = body
}

func (p *testPod) setArchive(archive []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.archive = archive
}

// newKubernetes starts a real gateway server with a real pod client behind it,
// which is the transport a pod's machine talks over.
func newKubernetes(t *testing.T, pods machine.Pods) (*machine.Kubernetes, *testPod) {
	t.Helper()
	pod := &testPod{files: map[string][]byte{}}
	server := gateway.NewServer(func(ctx context.Context, token string) (gateway.Ref, error) {
		return podRef, nil
	})
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := &gateway.Client{
		URL:    httpServer.URL,
		Token:  podToken,
		Handle: pod.handle,
		Log:    t.Logf,
	}
	go func() { _ = client.Run(ctx) }()
	if err := server.WaitFor(ctx, podRef, 10*time.Second); err != nil {
		t.Fatalf("the pod never connected: %v", err)
	}
	return machine.NewKubernetes(server, pods), pod
}

// fakePods is the daemon's side of a pod: the state it last saw, and what was
// asked of it.
type fakePods struct {
	mu        sync.Mutex
	state     string
	started   int
	stopped   int
	destroyed int
}

func (p *fakePods) Start(ctx context.Context, ref gateway.Ref) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started++
	p.state = "Running"
	return nil
}

func (p *fakePods) Stop(ctx context.Context, ref gateway.Ref) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped++
	p.state = "Stopped"
	return nil
}

func (p *fakePods) Destroy(ctx context.Context, ref gateway.Ref) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.destroyed++
	p.state = "Stopped"
	return nil
}

func (p *fakePods) Info(ctx context.Context, ref gateway.Ref) (machine.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.state
	if state == "" {
		state = "Running"
	}
	return machine.Info{Ref: ref.String(), State: state}, nil
}

func (p *fakePods) setState(state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = state
}

func podContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestKubernetesExec(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	ctx := podContext(t)

	out, err := m.Exec(ctx, "say", "hello")
	if err != nil {
		t.Fatalf("Exec(say hello) = %v", err)
	}
	if out != "hello\n" {
		t.Errorf("Exec(say hello) printed %q, want %q", out, "hello\n")
	}
	if got := strings.Join(pod.last(gateway.OpExec).Argv, " "); got != "say hello" {
		t.Errorf("the pod ran %q, want %q", got, "say hello")
	}

	out, err = m.Exec(ctx, "fail")
	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) {
		t.Fatalf("Exec(fail) error = %v, want an error carrying an exit code", err)
	}
	if exit.ExitCode() != 3 {
		t.Errorf("Exec(fail) exit code = %d, want 3", exit.ExitCode())
	}
	if !strings.Contains(out, "boom") || !strings.Contains(out, "why") {
		t.Errorf("Exec(fail) returned %q, want both streams", out)
	}
}

// TestKubernetesExecInput proves the half-close: the pod's command reads its
// stdin until EOF, so the call only ends if the machine sent CloseWrite.
func TestKubernetesExecInput(t *testing.T) {
	rt, _ := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())

	out, err := m.ExecInput(podContext(t), strings.NewReader("ping"), "cat")
	if err != nil {
		t.Fatalf("ExecInput(cat) = %v", err)
	}
	if out != "ping" {
		t.Errorf("ExecInput(cat) printed %q, want %q", out, "ping")
	}
}

func TestKubernetesUserExec(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())

	var stdout, stderr bytes.Buffer
	err := m.UserExec(podContext(t), "agent", "echo hello", nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("UserExec(echo hello) = %v", err)
	}
	if stdout.String() != "hello\n" {
		t.Errorf("UserExec stdout = %q, want %q", stdout.String(), "hello\n")
	}
	h := pod.last(gateway.OpExec)
	want := []string{"sh", "-lc", "echo hello"}
	if strings.Join(h.Argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("the pod ran %q, want %q", h.Argv, want)
	}
	if h.User != "agent" {
		t.Errorf("the command ran as %q, want agent", h.User)
	}
}

func TestKubernetesWriteFile(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	content := []byte("hello file")

	if err := m.WriteFile(podContext(t), "/workspace/a.txt", content, 1000, 1000, 0o640); err != nil {
		t.Fatalf("WriteFile = %v", err)
	}
	h := pod.last(gateway.OpWrite)
	if h.Path != "/workspace/a.txt" || h.UID != 1000 || h.GID != 1000 || h.Mode != 0o640 {
		t.Errorf("the write header = %+v, want the path, 1000:1000 and 0640", h)
	}
	if got := pod.file("/workspace/a.txt"); !bytes.Equal(got, content) {
		t.Errorf("the pod holds %q, want %q", got, content)
	}
}

func TestKubernetesPullFile(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	ctx := podContext(t)
	dir := t.TempDir()
	pod.setFile("/workspace/a.txt", []byte("copied out"))

	target := filepath.Join(dir, "a.txt")
	if err := m.PullFile(ctx, "/workspace/a.txt", target); err != nil {
		t.Fatalf("PullFile = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "copied out" {
		t.Errorf("PullFile wrote %q, want %q", got, "copied out")
	}

	missing := filepath.Join(dir, "missing.txt")
	err = m.PullFile(ctx, "/workspace/missing.txt", missing)
	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Errorf("PullFile of a missing file = %v, want exit status 1", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed pull left %s behind", missing)
	}
}

func TestKubernetesAttach(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	ctx := podContext(t)

	term, err := m.Attach(ctx, machine.AttachOptions{
		Argv: []string{"tmux", "new", "-A", "-s", "agent"},
		Cols: 80,
		Rows: 24,
	})
	if err != nil {
		t.Fatalf("Attach = %v", err)
	}
	defer func() { _ = term.Close() }()

	read := func(n int) string {
		t.Helper()
		buf := make([]byte, n)
		if _, err := io.ReadFull(term, buf); err != nil {
			t.Fatalf("reading the terminal: %v", err)
		}
		return string(buf)
	}
	if got := read(len("welcome")); got != "welcome" {
		t.Errorf("the terminal printed %q, want welcome", got)
	}
	if _, err := term.Write([]byte("hi")); err != nil {
		t.Fatalf("writing to the terminal: %v", err)
	}
	if got := read(2); got != "hi" {
		t.Errorf("the terminal echoed %q, want hi", got)
	}
	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("Resize = %v", err)
	}
	const resized = "resized 120x40"
	if got := read(len(resized)); got != resized {
		t.Errorf("the pod resized to %q, want %q", got, resized)
	}
	if _, err := term.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("Read after the exit frame = %v, want io.EOF", err)
	}
	if err := term.Close(); err != nil {
		t.Errorf("Close of a terminal that ended with 0 = %v", err)
	}

	h := pod.last(gateway.OpAttach)
	if h.Cols != 80 || h.Rows != 24 {
		t.Errorf("the terminal opened %dx%d, want 80x24", h.Cols, h.Rows)
	}
	if strings.Join(h.Argv, " ") != "tmux new -A -s agent" {
		t.Errorf("the terminal ran %q", h.Argv)
	}
	pod.mu.Lock()
	typed, resizes := string(pod.typed), pod.resizes
	pod.mu.Unlock()
	if typed != "hi" {
		t.Errorf("the pod received %q, want hi", typed)
	}
	if len(resizes) != 1 || resizes[0] != [2]uint16{120, 40} {
		t.Errorf("the pod saw resizes %v, want one of 120x40", resizes)
	}
}

func TestKubernetesDial(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())

	conn, err := m.Dial(podContext(t), "tcp", "127.0.0.1:5900")
	if err != nil {
		t.Fatalf("Dial = %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("writing to the dialed port: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("reading from the dialed port: %v", err)
	}
	if string(buf) != "ping" {
		t.Errorf("the port echoed %q, want ping", buf)
	}
	if addr := conn.RemoteAddr(); !strings.Contains(addr.String(), podRef.String()) || !strings.Contains(addr.String(), "127.0.0.1:5900") {
		t.Errorf("RemoteAddr() = %q, want the pod and its address", addr)
	}
	// Deadlines are accepted and do nothing: a stream carries none.
	if err := conn.SetDeadline(time.Now()); err != nil {
		t.Errorf("SetDeadline = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		t.Errorf("SetReadDeadline = %v", err)
	}
	if err := conn.SetWriteDeadline(time.Now()); err != nil {
		t.Errorf("SetWriteDeadline = %v", err)
	}
	h := pod.last(gateway.OpDial)
	if h.Network != "tcp" || h.Addr != "127.0.0.1:5900" {
		t.Errorf("the pod dialed %s %s, want tcp 127.0.0.1:5900", h.Network, h.Addr)
	}
}

func TestKubernetesWaitReadyWaitsForTheSession(t *testing.T) {
	pod := &testPod{files: map[string][]byte{}}
	server := gateway.NewServer(func(ctx context.Context, token string) (gateway.Ref, error) {
		return podRef, nil
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	rt := machine.NewKubernetes(server, &fakePods{state: "Running"})
	m := rt.Machine(podRef.String())
	ctx := podContext(t)

	type answer struct {
		info machine.Info
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		info, err := m.WaitReady(ctx, 5*time.Second)
		done <- answer{info, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("WaitReady returned before the pod connected: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}

	clientCtx, stopClient := context.WithCancel(context.Background())
	defer stopClient()
	client := &gateway.Client{URL: httpServer.URL, Token: podToken, Handle: pod.handle, Log: t.Logf}
	go func() { _ = client.Run(clientCtx) }()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("WaitReady = %v", got.err)
		}
		if got.info.State != "Running" {
			t.Errorf("WaitReady read state %q, want Running", got.info.State)
		}
	case <-ctx.Done():
		t.Fatal("WaitReady never returned after the pod connected")
	}
}

func TestKubernetesInfoReadsThePodState(t *testing.T) {
	pods := &fakePods{state: "Stopped"}
	rt, _ := newKubernetes(t, pods)
	m := rt.Machine(podRef.String())
	ctx := podContext(t)

	info, err := m.Info(ctx)
	if err != nil {
		t.Fatalf("Info = %v", err)
	}
	if info.State != "Stopped" || info.Running() || info.Paused() {
		t.Errorf("Info = %+v, want a stopped machine", info)
	}
	if info.Ref != podRef.String() {
		t.Errorf("Info.Ref = %q, want %q", info.Ref, podRef.String())
	}

	pods.setState("Running")
	info, err = m.Info(ctx)
	if err != nil {
		t.Fatalf("Info after start = %v", err)
	}
	if !info.Running() {
		t.Errorf("Info = %+v, want a running machine", info)
	}
}

func TestKubernetesPauseIsNotSupported(t *testing.T) {
	rt, _ := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	ctx := podContext(t)

	calls := []struct {
		name string
		call func() error
	}{
		{"Pause", func() error { return m.Pause(ctx) }},
		{"Resume", func() error { return m.Resume(ctx) }},
		{"Snapshot", func() error { return m.Snapshot(ctx, "s") }},
		{"SnapshotList", func() error { _, err := m.SnapshotList(ctx); return err }},
		{"RestoreSnapshot", func() error { return m.RestoreSnapshot(ctx, "s") }},
		{"DeleteSnapshot", func() error { return m.DeleteSnapshot(ctx, "s") }},
		{"HasSnapshot", func() error { _, err := m.HasSnapshot(ctx, "s"); return err }},
		{"Usage", func() error { _, err := m.Usage(ctx); return err }},
	}
	for _, c := range calls {
		err := c.call()
		if !errors.Is(err, machine.ErrNotSupported) {
			t.Errorf("%s = %v, want ErrNotSupported", c.name, err)
		}
	}
	if err := m.Pause(ctx); !strings.Contains(err.Error(), "stopped, not frozen") {
		t.Errorf("Pause = %v, want it to say a pod is stopped, not frozen", err)
	}
}

func TestKubernetesABadRefFailsEveryMethod(t *testing.T) {
	// No session and no pods: every method must fail on the ref before it
	// touches either, and none may panic.
	rt := machine.NewKubernetes(nil, &fakePods{})
	m := rt.Machine("not-a-ref")
	if m.Ref() != "not-a-ref" {
		t.Errorf("Ref() = %q, want not-a-ref", m.Ref())
	}
	if m.Kind() != machine.KindKubernetes {
		t.Errorf("Kind() = %q, want kubernetes", m.Kind())
	}
	ctx := context.Background()
	calls := []struct {
		name string
		call func() error
	}{
		{"Info", func() error { _, err := m.Info(ctx); return err }},
		{"WaitReady", func() error { _, err := m.WaitReady(ctx, time.Second); return err }},
		{"Start", func() error { return m.Start(ctx) }},
		{"Stop", func() error { return m.Stop(ctx) }},
		{"StopWithin", func() error { return m.StopWithin(ctx, time.Second) }},
		{"ForceStop", func() error { return m.ForceStop(ctx) }},
		{"Pause", func() error { return m.Pause(ctx) }},
		{"Resume", func() error { return m.Resume(ctx) }},
		{"Destroy", func() error { return m.Destroy(ctx) }},
		{"Exec", func() error { _, err := m.Exec(ctx, "true"); return err }},
		{"ExecInput", func() error { _, err := m.ExecInput(ctx, strings.NewReader("x"), "cat"); return err }},
		{"UserExec", func() error { return m.UserExec(ctx, "agent", "true", nil, nil, nil) }},
		{"Attach", func() error {
			term, err := m.Attach(ctx, machine.AttachOptions{Argv: []string{"sh"}})
			if err == nil {
				_ = term.Close()
			}
			return err
		}},
		{"WriteFile", func() error { return m.WriteFile(ctx, "/workspace/a", nil, 0, 0, 0o644) }},
		{"PullFile", func() error { return m.PullFile(ctx, "/workspace/a", "target") }},
		{"PullDir", func() error { return m.PullDir(ctx, "/workspace", t.TempDir()) }},
		{"PushFile", func() error { return m.PushFile(ctx, "local", "/workspace/a", 0o644) }},
		{"Snapshot", func() error { return m.Snapshot(ctx, "s") }},
		{"SnapshotList", func() error { _, err := m.SnapshotList(ctx); return err }},
		{"RestoreSnapshot", func() error { return m.RestoreSnapshot(ctx, "s") }},
		{"DeleteSnapshot", func() error { return m.DeleteSnapshot(ctx, "s") }},
		{"HasSnapshot", func() error { _, err := m.HasSnapshot(ctx, "s"); return err }},
		{"Usage", func() error { _, err := m.Usage(ctx); return err }},
		{"Dial", func() error {
			conn, err := m.Dial(ctx, "tcp", "127.0.0.1:5900")
			if err == nil {
				_ = conn.Close()
			}
			return err
		}},
	}
	for _, c := range calls {
		err := c.call()
		if err == nil {
			t.Errorf("%s on a bad ref = nil, want an error naming it", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "not-a-ref") {
			t.Errorf("%s on a bad ref = %v, want it to name the ref", c.name, err)
		}
	}
}

func TestKubernetesPullDir(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	dir := t.TempDir()
	pod.setArchive(tarArchive(t, map[string]string{"./index.html": "<html>"}))

	if err := m.PullDir(podContext(t), "/workspace/report", dir); err != nil {
		t.Fatalf("PullDir = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>" {
		t.Errorf("the pull wrote %q, want %q", got, "<html>")
	}
	if command := pod.last(gateway.OpExec).Argv; !strings.Contains(strings.Join(command, " "), "tar -C '/workspace/report' -cf - .") {
		t.Errorf("the pod ran %q, want the tar command for the directory", command)
	}
}

func TestKubernetesPullDirRefusesAnEscape(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	pod.setArchive(tarArchive(t, map[string]string{"../escape.txt": "no"}))

	err := m.PullDir(podContext(t), "/workspace/report", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("PullDir of an escaping entry = %v, want an error saying it would land outside", err)
	}
}

func TestKubernetesPullDirRefusesASymlink(t *testing.T) {
	rt, pod := newKubernetes(t, &fakePods{})
	m := rt.Machine(podRef.String())
	pod.setArchive(symlinkArchive(t, "./link", "/etc/passwd"))

	err := m.PullDir(podContext(t), "/workspace/report", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("PullDir of a symlink = %v, want an error naming the symlink", err)
	}
}

func tarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func symlinkArchive(t *testing.T, name, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: name, Linkname: target, Mode: 0o777, Typeflag: tar.TypeSymlink}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
