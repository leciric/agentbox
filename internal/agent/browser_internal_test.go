package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/image"
	"agentbox/internal/incus"
	mach "agentbox/internal/machine"
	"agentbox/internal/paths"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// TestDisplayUpReadsTheVNCGreeting is what lets the Desktop tab show a desktop
// with no browser on it: the proxy device accepts the connection whether or
// not anything listens inside the agent, so only the RFB greeting says the
// display is really there.
func TestDisplayUpReadsTheVNCGreeting(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		serve func(net.Conn)
		want  bool
	}{
		"a VNC server greets":            {func(c net.Conn) { _, _ = c.Write([]byte("RFB 003.008\n")) }, true},
		"something else answers":         {func(c net.Conn) { _, _ = c.Write([]byte("HTTP/1.1 200 OK\r\n")) }, false},
		"the proxy accepts, then closes": {func(c net.Conn) {}, false},
	}
	for name, c := range cases {
		socket := filepath.Join(t.TempDir(), "vnc")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			c.serve(conn)
			_ = conn.Close()
		}()
		m := &Manager{BrowserSocket: func(string, string) string { return socket }}
		if got := m.displayUp(context.Background(), state.Agent{Instance: "ab-p-agent-01"}); got != c.want {
			t.Errorf("%s: displayUp = %v, want %v", name, got, c.want)
		}
		_ = listener.Close()
	}

	m := &Manager{BrowserSocket: func(string, string) string { return filepath.Join(t.TempDir(), "nothing") }}
	if m.displayUp(context.Background(), state.Agent{Instance: "ab-p-agent-01"}) {
		t.Error("no socket at all: displayUp = true, want false")
	}
}

// TestBrowserScriptMarksACrashedProfileClean is what keeps Chromium from
// offering to restore pages after it was killed instead of closed.
func TestBrowserScriptMarksACrashedProfileClean(t *testing.T) {
	t.Parallel()
	profile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o755); err != nil {
		t.Fatal(err)
	}
	prefs := filepath.Join(profile, "Default", "Preferences")
	if err := os.WriteFile(prefs, []byte(`{"profile":{"exit_type":"Crashed","exited_cleanly":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?s)\nmark_clean_exit\(\) \{.*?\n\}\n`).FindString(string(BrowserScript()))
	if fn == "" {
		t.Fatal("browser.sh has no mark_clean_exit")
	}
	cmd := exec.Command("sh", "-c", fn+"mark_clean_exit")
	cmd.Env = append(os.Environ(), "profile="+profile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got, _ := os.ReadFile(prefs)
	if string(got) != `{"profile":{"exit_type":"Normal","exited_cleanly":true}}` {
		t.Errorf("preferences = %s", got)
	}
}

// The desktop's idle VNC client, which keeps Xvnc's frame clock at 60 Hz:
// it shakes hands as a shared client with no security, then stays connected
// in one process, so that killing it closes the connection.
func TestBrowserScriptHoldsVNC(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/proc/self/comm"); err != nil {
		t.Skip("no /proc")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	m := regexp.MustCompile(`(?s)\nhold_vnc='([^']*)'\n`).FindStringSubmatch(string(BrowserScript()))
	if m == nil {
		t.Fatal("browser.sh has no hold_vnc")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", m[1], "agentbox-vnc-clock", port)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()

	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// The server speaks first; the client answers each step with one write
	// already, which is how Xvnc reads it too.
	if _, err := conn.Write([]byte("RFB 003.008\n\x01\x01")); err != nil {
		t.Fatal(err)
	}
	want := "RFB 003.008\n\x01\x01"
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != want {
		t.Fatalf("handshake = %q, %v; want %q", got, err, want)
	}
	// What Xvnc sends (a bell, the clipboard) is read and dropped, NULs too.
	if _, err := conn.Write([]byte("\x02\x00\x00\x00\x00\x00\x00\x00")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("the client left: %v", err)
	default:
	}
	// The bash holds the connection itself, with no child to outlive it.
	children, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "task", strconv.Itoa(cmd.Process.Pid), "children"))
	if c := strings.TrimSpace(string(children)); c != "" {
		t.Errorf("the client has children: %s", c)
	}
	// It ends when the server goes.
	_ = conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("the client outlived the server")
	}
}

// TestBrowserScriptLaunchesTheDockFromItsOwnPath is what makes the dock's
// Browser button start the copy of browser.sh that is running: the script is
// /usr/local/bin/agentbox-browser in an Incus agent and under the agent's home
// in a pod (browser.go), so the launcher cannot name either one.
func TestBrowserScriptLaunchesTheDockFromItsOwnPath(t *testing.T) {
	t.Parallel()
	script := string(BrowserScript())
	if !strings.Contains(script, `self=$(readlink -f "$0"`) {
		t.Error("browser.sh doesn't resolve the path it was started as")
	}
	line := regexp.MustCompile(`(?m)^\s*dock_entry browser Browser (.*)$`).FindStringSubmatch(script)
	if line == nil {
		t.Fatal("browser.sh has no browser dock entry")
	}
	if strings.HasPrefix(line[1], "/") {
		t.Errorf("the dock's browser launcher is the fixed path %s, want the script's own", line[1])
	}
}

// The browser's half of the machine seam (agent.go): a cluster agent's machine
// is a pod, whose desktop is the same one an Incus agent has, reached and
// installed differently. Its two ports are dialled from inside the pod, and its
// copies of browser.sh, the wallpaper and the palette live under the agent's
// home, the only place outside the workspace and /run/agentbox its agentd lets
// a write reach. machinepaths_test.go is the rest of the seam.

// podAgent is an agent whose machine is a pod in the k3d cluster.
func podAgent() state.Agent {
	return state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01",
		Cluster: "k3d", Workspace: "/workspace",
	}
}

// browserPod is a pod's machine as the browser's tests need it: the cluster
// fixture's stub, with what it was asked to run, the user it ran as, and the
// ports it was dialled for recorded. Dial answers through dial; a nil one
// refuses, as a port with nothing behind it does.
type browserPod struct {
	*podMachineStub
	// output is what UserExec writes to stdout.
	output string
	dial   func(network, addr string) (net.Conn, error)

	execs []string
	user  string
	dials []string
}

func (p *browserPod) Exec(_ context.Context, command ...string) (string, error) {
	p.execs = append(p.execs, strings.Join(command, " "))
	return "", nil
}

func (p *browserPod) UserExec(_ context.Context, user, command string, _ io.Reader, stdout, _ io.Writer) error {
	p.user = user
	p.userExecs = append(p.userExecs, command)
	_, err := io.WriteString(stdout, p.output)
	return err
}

func (p *browserPod) Dial(_ context.Context, network, addr string) (net.Conn, error) {
	p.dials = append(p.dials, network+" "+addr)
	if p.dial == nil {
		return nil, fmt.Errorf("nothing listens on %s of a fake pod", addr)
	}
	return p.dial(network, addr)
}

// podCluster is the cluster browserPod is the only machine of.
type podCluster struct{ m *browserPod }

func (r podCluster) Kind() mach.Kind             { return mach.KindKubernetes }
func (r podCluster) Machine(string) mach.Machine { return r.m }

// podManager is a Manager whose cluster agents run on pod, with an Incus that
// records every call it gets: the browser paths may not make one. incusCalls
// returns what it recorded.
func podManager(t *testing.T, pod *browserPod) (m *Manager, incusCalls func() string) {
	t.Helper()
	calls := filepath.Join(t.TempDir(), "incus-calls")
	bin := filepath.Join(t.TempDir(), "incus")
	testutil.WriteExecutable(t, bin, []byte("#!/bin/sh\necho \"$*\" >>"+calls+"\n"))
	socket := filepath.Join(t.TempDir(), "browser")
	return &Manager{
		User:          image.User{Name: "dev", UID: 1000, GID: 1000},
		Kubernetes:    podCluster{m: pod},
		Incus:         incus.Client{Bin: bin},
		BrowserSocket: func(string, string) string { return socket },
		Paths:         paths.Paths{Config: t.TempDir(), Data: t.TempDir()},
		Log:           io.Discard,
	}, func() string {
		out, err := os.ReadFile(calls)
		if err != nil {
			return ""
		}
		return string(out)
	}
}

// TestPrepareBrowserInAPodWritesUnderItsHome installs the desktop in a pod: its
// browser.sh, wallpaper and palette land under /home/agent, where its agentd
// accepts a write, and no Incus device is made for a machine that has none.
func TestPrepareBrowserInAPodWritesUnderItsHome(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	m, incusCalls := podManager(t, pod)
	if err := m.prepareBrowser(context.Background(), podAgent()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/home/agent/.local/bin/agentbox-browser",
		"/home/agent/.local/share/agentbox/wallpaper.png",
		"/home/agent/.local/share/agentbox/theme.conf",
	} {
		if !slices.Contains(pod.writes, want) {
			t.Errorf("prepareBrowser() didn't write %s, wrote %v", want, pod.writes)
		}
	}
	if len(pod.writes) != 3 {
		t.Errorf("prepareBrowser() wrote %v, want the script, the wallpaper and the palette", pod.writes)
	}
	for _, wrote := range pod.writes {
		if !strings.HasPrefix(wrote, "/home/agent/") {
			t.Errorf("prepareBrowser() wrote %s, outside the pod's home", wrote)
		}
	}
	// The checks for what is already there name the same files.
	for _, want := range []string{
		"sha256sum /home/agent/.local/share/agentbox/wallpaper.png",
		"sha256sum /home/agent/.local/share/agentbox/theme.conf",
	} {
		if !strings.Contains(strings.Join(pod.execs, "\n"), want) {
			t.Errorf("prepareBrowser() didn't check %s in the pod, ran %v", want, pod.execs)
		}
	}
	if got := incusCalls(); got != "" {
		t.Errorf("prepareBrowser() made an Incus call for a pod:\n%s", got)
	}
}

// TestRunBrowserScriptInAPodNamesItsShareAndHome runs the desktop's own action
// in a pod, as its agent user, with the desktop share browser.sh has no default
// for there.
func TestRunBrowserScriptInAPodNamesItsShareAndHome(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	m, _ := podManager(t, pod)
	if err := m.runBrowserScript(context.Background(), podAgent(), "start"); err != nil {
		t.Fatal(err)
	}
	if pod.user != "agent" {
		t.Errorf("the browser script ran as %q, want the pod's own agent user", pod.user)
	}
	want := "AGENTBOX_SHARE=/home/agent/.local/share/agentbox /home/agent/.local/bin/agentbox-browser start"
	if len(pod.userExecs) != 1 || pod.userExecs[0] != want {
		t.Errorf("runBrowserScript() ran %q, want %q", pod.userExecs, want)
	}
}

// TestRunBrowserScriptOnIncusKeepsItsPaths checks the other half: an Incus agent
// still runs /usr/local/bin/agentbox-browser, as the daemon's user, and is not
// told where its share is.
func TestRunBrowserScriptOnIncusKeepsItsPaths(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	bin := filepath.Join(t.TempDir(), "incus")
	testutil.WriteExecutable(t, bin, []byte("#!/bin/sh\necho \"$*\" >>"+calls+"\n"))
	m := &Manager{
		User:  image.User{Name: "dev", UID: 1000, GID: 1000},
		Incus: incus.Client{Bin: bin},
	}
	a := state.Agent{Instance: "ab-hello-stack-agent-01"}
	if err := m.runBrowserScript(context.Background(), a, "theme"); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "/usr/local/bin/agentbox-browser theme") {
		t.Errorf("runBrowserScript() on Incus ran:\n%s", out)
	}
	if strings.Contains(string(out), "AGENTBOX_SHARE") {
		t.Errorf("runBrowserScript() told an Incus agent where its share is:\n%s", out)
	}
	if !strings.Contains(string(out), "runuser") || !strings.Contains(string(out), "dev") {
		t.Errorf("runBrowserScript() didn't run as the daemon's user:\n%s", out)
	}
}

// TestDialVNCOpensThePodsOwnPort is the display's half: a pod has no unix
// socket on this host, so its VNC server is dialled from inside the pod, on
// the loopback address Chromium and Xvnc listen on there.
func TestDialVNCOpensThePodsOwnPort(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	pod.dial = func(_, _ string) (net.Conn, error) {
		server, client := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	m, _ := podManager(t, pod)
	conn, err := m.dialVNC(context.Background(), podAgent())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if want := []string{"tcp 127.0.0.1:5900"}; !slices.Equal(pod.dials, want) {
		t.Errorf("dialVNC() dialled %v, want %v", pod.dials, want)
	}
}

// serveDevTools answers on conn, one side of the pipe a fake pod's Dial handed
// the DevTools client, with what Chromium answers a status request.
func serveDevTools(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	path := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if line = strings.TrimRight(line, "\r\n"); line == "" {
			break
		}
		if rest, ok := strings.CutPrefix(line, "GET "); ok {
			path, _, _ = strings.Cut(rest, " ")
		}
	}
	body := "{}"
	switch path {
	case "/json/version":
		body = `{"Browser":"Chrome/141"}`
	case "/json/list":
		body = `[{"ID":"PAGE1","Type":"page","URL":"https://example.test/","Title":"Example","WebSocketDebuggerURL":"ws://127.0.0.1:9222/devtools/page/PAGE1"}]`
	}
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}

// TestDevtoolsInAPodDialsItsOwnDebuggingPort is the browser's half of the same
// seam: the DevTools client reaches 9222 from inside the pod, not through an
// Incus proxy socket.
func TestDevtoolsInAPodDialsItsOwnDebuggingPort(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	pod.dial = func(_, _ string) (net.Conn, error) {
		server, client := net.Pipe()
		go serveDevTools(server)
		return client, nil
	}
	m, _ := podManager(t, pod)
	var version struct{ Browser string }
	if err := m.devtoolsRequest(context.Background(), podAgent(), http.MethodGet, "/json/version", &version); err != nil {
		t.Fatal(err)
	}
	if version.Browser != "Chrome/141" {
		t.Errorf("the DevTools version = %q, want what the pod's Chromium answered", version.Browser)
	}
	if want := []string{"tcp 127.0.0.1:9222"}; !slices.Equal(pod.dials, want) {
		t.Errorf("devtools() dialled %v, want %v", pod.dials, want)
	}
}

// TestBrowserStatusOfAStoppedPodAsksNothing checks that a pod that is stopped
// has no desktop and no browser, and that asking costs it no connection and no
// Incus call: its machine, not an instance of the same name, is what answers.
func TestBrowserStatusOfAStoppedPodAsksNothing(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01", state: "Stopped"}}
	m, incusCalls := podManager(t, pod)
	status, err := m.BrowserStatus(context.Background(), podAgent())
	if err != nil {
		t.Fatal(err)
	}
	if status.Display || status.Running || status.Version != "" || len(status.Pages) != 0 {
		t.Errorf("BrowserStatus() of a stopped pod = %+v, want nothing running", status)
	}
	if len(pod.dials) != 0 {
		t.Errorf("BrowserStatus() dialled a stopped pod: %v", pod.dials)
	}
	if got := incusCalls(); got != "" {
		t.Errorf("BrowserStatus() made an Incus call for a pod:\n%s", got)
	}
}

// TestBrowserStatusOfARunningPodAsksTheBrowser checks that a running pod needs
// no devices to answer: the DevTools port answering is the desktop being up.
func TestBrowserStatusOfARunningPodAsksTheBrowser(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	pod.dial = func(_, _ string) (net.Conn, error) {
		server, client := net.Pipe()
		go serveDevTools(server)
		return client, nil
	}
	m, _ := podManager(t, pod)
	status, err := m.BrowserStatus(context.Background(), podAgent())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Display || !status.Running || status.Version != "Chrome/141" {
		t.Errorf("BrowserStatus() = %+v, want the desktop up with its browser", status)
	}
	if len(status.Pages) != 1 || status.Pages[0].ID != "PAGE1" || status.Pages[0].URL != "https://example.test/" {
		t.Errorf("BrowserStatus() pages = %+v, want the one the pod's Chromium has open", status.Pages)
	}
	// The browser answered from the display, so the VNC server isn't asked.
	for _, dialled := range pod.dials {
		if dialled != "tcp 127.0.0.1:9222" {
			t.Errorf("BrowserStatus() dialled %v, want only the DevTools port", pod.dials)
		}
	}
}

// TestBrowserStatusOfAPodWithNoBrowserFindsItsDisplay is the case the Desktop
// tab shows: Chromium is gone and the display it was a window on is not, which
// only the VNC server's greeting can say.
func TestBrowserStatusOfAPodWithNoBrowserFindsItsDisplay(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	pod.dial = func(_, addr string) (net.Conn, error) {
		server, client := net.Pipe()
		if addr != "127.0.0.1:5900" {
			_ = server.Close()
			return nil, fmt.Errorf("nothing listens on %s", addr)
		}
		go func() {
			defer func() { _ = server.Close() }()
			_, _ = server.Write([]byte("RFB 003.008\n"))
		}()
		return client, nil
	}
	m, _ := podManager(t, pod)
	status, err := m.BrowserStatus(context.Background(), podAgent())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Display || status.Running {
		t.Errorf("BrowserStatus() = %+v, want the display up without its browser", status)
	}
	want := []string{"tcp 127.0.0.1:9222", "tcp 127.0.0.1:5900"}
	if !slices.Equal(pod.dials, want) {
		t.Errorf("BrowserStatus() dialled %v, want %v", pod.dials, want)
	}
}

// TestEnsureRecordingStageIsSkippedInAPod is why a pod's recording is copied
// out rather than renamed: a pod has no Incus device to mount the host's stage
// with, so the stage is never asked for, and none is made.
func TestEnsureRecordingStageIsSkippedInAPod(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}}
	m, incusCalls := podManager(t, pod)
	staged, err := m.ensureRecordingStage(context.Background(), podAgent())
	if err == nil && staged {
		t.Error("a pod's recording is staged in the host's media, want it written in the pod and copied out")
	}
	if err != nil {
		t.Errorf("ensureRecordingStage() = %v, want it skipped without an error", err)
	}
	if got := incusCalls(); got != "" {
		t.Errorf("ensureRecordingStage() made an Incus call for a pod:\n%s", got)
	}
	if _, err := os.Stat(m.recordingStage(podAgent())); !os.IsNotExist(err) {
		t.Errorf("the host's recording stage was made for a pod: %v", err)
	}
}

// TestAgentShellInAPodRunsAsItsOwnUser is the user every one of these paths has
// to name: agentd refuses an exec that names anyone but the pod's own user, so
// a pod's home and its copy of the desktop are the agent's, never this host's.
func TestAgentShellInAPodRunsAsItsOwnUser(t *testing.T) {
	pod := &browserPod{podMachineStub: &podMachineStub{ref: "hello-stack/agent-01"}, output: "hi\n"}
	m, _ := podManager(t, pod)
	out, err := m.agentShell(context.Background(), podAgent(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi\n" {
		t.Errorf("agentShell() = %q, want what the pod printed", out)
	}
	if pod.user != "agent" {
		t.Errorf("agentShell() ran as %q, want the pod's own agent user", pod.user)
	}
}
