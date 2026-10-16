package hostwsl

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/hostos"
)

var fakeDir string

func TestMain(m *testing.M) {
	code := m.Run()
	if fakeDir != "" {
		_ = os.RemoveAll(fakeDir)
	}
	os.Exit(code)
}

// fakeWSL builds fakewsl once per test binary, or takes one already built from
// FAKEWSL_BIN: for a test binary that runs where there's no go, under wine.
var fakeWSL = sync.OnceValues(func() (string, error) {
	if exe := os.Getenv("FAKEWSL_BIN"); exe != "" {
		return exe, nil
	}
	dir, err := os.MkdirTemp("", "fakewsl-bin-")
	if err != nil {
		return "", err
	}
	fakeDir = dir
	exe := filepath.Join(dir, "wsl")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", exe, "./fakewsl").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building fakewsl: %v\n%s", err, out)
	}
	return exe, nil
})

// fakeDistro is a Distro on fakewsl, with no distro made yet.
func fakeDistro(t *testing.T) *Distro {
	t.Helper()
	wsl, err := fakeWSL()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// A unix socket's path has to be short: not under t.TempDir on every OS.
	sockDir, err := os.MkdirTemp("", "fw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	binary := filepath.Join(root, "agentbox-linux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho the Linux agentbox\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Distro{
		WSL:    wsl,
		Name:   DefaultName,
		User:   "ana",
		Dir:    filepath.Join(root, "disk"),
		Binary: binary,
		Log:    io.Discard,
		Env:    []string{"FAKEWSL_ROOT=" + filepath.Join(root, "wsl"), "FAKEWSL_SOCKET=" + filepath.Join(sockDir, "agentbox.sock")},
	}
}

func (d *Distro) fakeEnv(name string) string {
	for _, kv := range d.Env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v
		}
	}
	return ""
}

// imported makes the fake distro through Import, from a file.
func imported(t *testing.T, d *Distro) {
	t.Helper()
	tarball := filepath.Join(t.TempDir(), "rootfs.tar.gz")
	_ = os.WriteFile(tarball, []byte("not really"), 0o644)
	t.Setenv("AGENTBOX_WSL_ROOTFS", tarball)
	if err := d.Import(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStatus(t *testing.T) {
	d := fakeDistro(t)
	ctx := context.Background()
	st := d.Status(ctx)
	if st.WSL != "2.4.13.0" || st.Exists || st.Problem != ErrNotCreated.Error() {
		t.Fatalf("before import: %+v", st)
	}
	imported(t, d)
	// wsl.exe without WSL_UTF8 speaks UTF-16.
	d.Env = append(d.Env, "FAKEWSL_UTF16=1")
	st = d.Status(ctx)
	if st.Problem != "" || !st.Exists || st.Version != 2 || st.WSL != "2.4.13.0" {
		t.Fatalf("after import: %+v", st)
	}
	if err := d.Unregister(ctx); err != nil {
		t.Fatal(err)
	}
	if st := d.Status(ctx); st.Exists {
		t.Fatalf("after delete: %+v", st)
	}

	d.WSL = ""
	if st := d.Status(ctx); st.Problem != errNoWSL.Error() {
		t.Fatalf("without WSL: %+v", st)
	}
}

// TestNoDistros: WSL with no distros at all, in English and in another
// language, is a distro to set up, not an error.
func TestNoDistros(t *testing.T) {
	for _, lang := range []string{"en-US", "pt-BR"} {
		d := fakeDistro(t)
		d.Env = append(d.Env, "FAKEWSL_LANG="+lang)
		if st := d.Status(context.Background()); st.Problem != ErrNotCreated.Error() {
			t.Errorf("%s: %+v", lang, st)
		}
		if code, err := d.Forward(context.Background(), []string{"daemon", "start"}); code != 1 || !errors.Is(err, ErrNotCreated) {
			t.Errorf("%s: Forward = %d, %v", lang, code, err)
		}
	}
}

func TestEnsureBinary(t *testing.T) {
	d := fakeDistro(t)
	ctx := context.Background()
	if _, err := d.EnsureBinary(ctx); err == nil {
		t.Fatal("installed into a distro that doesn't exist")
	}
	imported(t, d)
	for i, want := range []bool{true, false} {
		got, err := d.EnsureBinary(ctx)
		if err != nil || got != want {
			t.Fatalf("EnsureBinary #%d = %v, %v; want %v", i+1, got, err, want)
		}
	}
	_ = os.WriteFile(d.Binary, []byte("a new version"), 0o755)
	if got, err := d.EnsureBinary(ctx); err != nil || !got {
		t.Fatalf("after an upgrade: %v, %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(d.fakeEnv("FAKEWSL_ROOT"), "usr", "local", "bin", "agentbox"))
	if string(b) != "a new version" {
		t.Fatalf("the distro has %q", b)
	}
}

// TestEnsureBinaryThroughStdin: a distro that can't see Windows' copy (no
// automount) gets it through wsl.exe's stdin instead.
func TestEnsureBinaryThroughStdin(t *testing.T) {
	d := fakeDistro(t)
	imported(t, d)
	d.Env = append(d.Env, "FAKEWSL_NO_MOUNT=1")
	var log strings.Builder
	d.Log = &log
	if got, err := d.EnsureBinary(context.Background()); err != nil || !got {
		t.Fatalf("EnsureBinary = %v, %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(d.fakeEnv("FAKEWSL_ROOT"), "usr", "local", "bin", "agentbox"))
	want, _ := os.ReadFile(d.Binary)
	if string(b) != string(want) || !strings.Contains(log.String(), "Copying agentbox into AgentBox") {
		t.Fatalf("the distro has %q; said %q", b, log.String())
	}
}

func TestForwardExitCode(t *testing.T) {
	d := fakeDistro(t)
	imported(t, d)
	// The distro's agentbox is fakewsl itself, which fails what it can't fake.
	d.Env = append(d.Env, "FAKEWSL_AGENTBOX="+d.WSL)
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"--version"}, 0},
		{[]string{"--help", "nonsense"}, 1},
	} {
		code, err := d.Forward(context.Background(), tc.args)
		if err != nil || code != tc.want {
			t.Errorf("Forward(%q) = %d, %v; want %d", tc.args, code, err, tc.want)
		}
	}
}

// TestForwardDaemonStart: the front end starts the daemon itself, and says
// so, rather than run the distro's `agentbox daemon start` after it.
func TestForwardDaemonStart(t *testing.T) {
	d := fakeDistro(t)
	imported(t, d)
	// The distro's agentbox is fakewsl itself, which can't fake `daemon
	// start`: running it would fail.
	d.Env = append(d.Env, "FAKEWSL_AGENTBOX="+d.WSL)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = d.exec(ctx, false, nil, "agentbox", "daemon", "stop") })
	var log strings.Builder
	d.Log = &log
	code, err := d.Forward(ctx, []string{"daemon", "start"})
	if err != nil || code != 0 {
		t.Fatalf("Forward(daemon start) = %d, %v", code, err)
	}
	// What it did, as the app shows it while it waits.
	for _, step := range []string{"Starting AgentBox, AgentBox's WSL distro…", "Installed agentbox in AgentBox (", "Starting the AgentBox daemon in AgentBox…", "Started the AgentBox daemon in AgentBox ("} {
		if !strings.Contains(log.String(), step) {
			t.Errorf("didn't say %q: %q", step, log.String())
		}
	}
	if !d.daemonAnswers(ctx, 0) {
		t.Fatal("no daemon answers after daemon start")
	}
}

// TestDaemonExits: a daemon that exits as it starts is reported then, not
// waited for.
func TestDaemonExits(t *testing.T) {
	d := fakeDistro(t)
	imported(t, d)
	d.Env = append(d.Env, "FAKEWSL_DAEMON_EXIT=1")
	start := time.Now()
	err := d.StartDaemon(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exited as it started") {
		t.Fatalf("StartDaemon = %v", err)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("reported after %s", took)
	}
}

// TestInstallScript runs EnsureBinary's script with a real sh, where there's
// one: current, installed from where the distro sees Windows' copy, and
// "stdin" when it can't read it.
func TestInstallScript(t *testing.T) {
	for _, tool := range []string{"sh", "flock", "sha256sum", "cut"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	if err := os.MkdirAll("/run/lock", 0o755); err != nil {
		t.Skip("no /run/lock here")
	}
	if f, err := os.OpenFile("/run/lock/agentbox-install", os.O_CREATE|os.O_WRONLY, 0o666); err != nil {
		t.Skip("can't take /run/lock/agentbox-install here")
	} else {
		_ = f.Close()
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "agentbox-linux"), filepath.Join(dir, "agentbox")
	_ = os.WriteFile(src, []byte("new"), 0o644)
	want, _ := digest(strings.NewReader("new"))
	run := func(src string) string {
		out, err := exec.Command("sh", "-c", installScript, "sh", want, dst, src).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(filepath.Join(dir, "missing")); got != "stdin" {
		t.Fatalf("with no copy to read: %q", got)
	}
	if got := run(src); got != "installed" {
		t.Fatalf("first: %q", got)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("installed %q", b)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
		t.Fatalf("installed with mode %v", fi.Mode())
	}
	if got := run(src); got != "current" {
		t.Fatalf("again: %q", got)
	}
}

// TestStuckWSL: a wsl.exe that never answers is given up on, and the error
// says so, rather than `agentbox daemon start` hanging with nothing said
// until the app gives up on it, which is all its banner could show.
func TestStuckWSL(t *testing.T) {
	for _, step := range []string{"--list", "--distribution"} {
		t.Run(step, func(t *testing.T) {
			d := fakeDistro(t)
			imported(t, d)
			d.Env = append(d.Env, "FAKEWSL_HANG="+step)
			defer func(l, e time.Duration) { listTimeout, execTimeout = l, e }(listTimeout, execTimeout)
			listTimeout, execTimeout = time.Second, time.Second
			start := time.Now()
			code, err := d.Forward(context.Background(), []string{"daemon", "start"})
			if code == 0 || err == nil || !strings.Contains(err.Error(), "didn't answer") || !strings.Contains(err.Error(), "wsl --shutdown") {
				t.Fatalf("Forward = %d, %v", code, err)
			}
			if took := time.Since(start); took > 20*time.Second {
				t.Fatalf("gave up after %s", took)
			}
		})
	}
}

func TestSilentExit(t *testing.T) {
	quiet, loud, out := &tail{}, &tail{}, &tail{}
	_, _ = loud.Write([]byte("error: no such project\n"))
	_, _ = out.Write([]byte("Le nom de la distribution n'existe pas.\r\nWsl/Service/WSL_E_DISTRO_NOT_FOUND\r\n"))
	for _, tc := range []struct {
		code      int
		said, out *tail
		want      string
	}{
		{1, loud, nil, ""},
		{1, nil, nil, ""}, // a console: it saw whatever was said
		{1, quiet, nil, "agentbox daemon stop exited with status 1 in AgentBox"},
		{3, quiet, out, "agentbox daemon stop exited with status 3 in AgentBox: Wsl/Service/WSL_E_DISTRO_NOT_FOUND"},
		{-1, loud, out, "wsl.exe couldn't run agentbox daemon stop in AgentBox (error 0xffffffff): Wsl/Service/WSL_E_DISTRO_NOT_FOUND"},
		{0x8007019e, nil, nil, "wsl.exe couldn't run agentbox daemon stop in AgentBox (error 0x8007019e)"},
	} {
		err := silentExit([]string{"daemon", "stop"}, "AgentBox", tc.code, tc.said, tc.out)
		if got := fmt.Sprint(err); (err == nil) != (tc.want == "") || (err != nil && got != tc.want) {
			t.Errorf("silentExit(%d) = %v, want %q", tc.code, err, tc.want)
		}
	}
}

// TestRelay runs the app's path to the daemon end to end: a private listener,
// yamux over fakewsl's stdio, the bridge, and a daemon on a unix socket.
func TestRelay(t *testing.T) {
	d := fakeDistro(t)
	imported(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in, inW := io.Pipe()
	outR, out := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- RunRelay(ctx, d, in, out) }()
	path, err := ReadSocketLine(outR)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return dialPrivate(path) },
	}}
	get := func() (*http.Response, string) {
		t.Helper()
		resp, err := client.Get("http://agentbox/v1/version")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	// No daemon yet: the relay answers for it, so the app starts one.
	resp, body := get()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get(relayHeader) != relayNotListened {
		t.Fatalf("with no daemon: %s %q", resp.Status, body)
	}

	// As `daemon start` does, through Forward: a stopped distro's relay
	// doesn't install the distro's agentbox.
	if _, err := d.EnsureBinary(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.StartDaemon(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodPost, "http://agentbox/v1/shutdown", nil)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	})
	if resp, body := get(); resp.StatusCode != http.StatusOK || body != `{"version":"fake"}` {
		t.Fatalf("with the daemon: %s %q", resp.Status, body)
	}
	// The install ID is kept beside the distro's directory, which a new
	// distro replaces; the Windows home is where the user's skills are. The
	// daemon gets both at their paths in WSL (C:\x is /mnt/c/x there).
	env, _ := os.ReadFile(filepath.Join(d.fakeEnv("FAKEWSL_ROOT"), "daemon-env"))
	home, _ := os.UserHomeDir()
	file, _ := LinuxPath(filepath.Join(filepath.Dir(d.Dir), "install-id"), d.Name)
	home, _ = LinuxPath(home, d.Name)
	if runtime.GOOS == "windows" && (!strings.HasPrefix(file, "/mnt/") || !strings.HasPrefix(home, "/mnt/")) {
		t.Fatalf("LinuxPath gave %q and %q, not paths under /mnt", file, home)
	}
	if want := hostos.InstallIDFileEnv + "=" + file + "\n" + hostos.WindowsHomeEnv + "=" + home; string(env) != want {
		t.Errorf("the daemon was started with %q, want %q", env, want)
	}

	// Many at once, over the one wsl.exe, each with a body bigger than a
	// yamux window.
	big := strings.Repeat("abcdefgh", 1<<17)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Post("http://agentbox/v1/echo", "text/plain", strings.NewReader(big))
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			if string(b) != strings.ToUpper(big) {
				t.Errorf("echo of %d bytes came back as %d", len(big), len(b))
			}
		}()
	}
	wg.Wait()

	// A websocket: an upgraded connection that stays open both ways.
	conn, err := dialPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var key [16]byte
	rand.Read(key[:])
	_, _ = fmt.Fprintf(conn, "GET /v1/echo HTTP/1.1\r\nHost: agentbox\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", base64.StdEncoding.EncodeToString(key[:]))
	br := bufio.NewReader(conn)
	status, _ := br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("websocket upgrade: %q", status)
	}
	for line, _ := br.ReadString('\n'); line != "\r\n" && line != ""; line, _ = br.ReadString('\n') {
	}
	for _, msg := range []string{"hello", "from windows"} {
		// A masked text frame, as a client sends.
		frame := []byte{0x81, 0x80 | byte(len(msg)), 1, 2, 3, 4}
		for i := range len(msg) {
			frame = append(frame, msg[i]^byte(i%4+1))
		}
		_, _ = conn.Write(frame)
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		h := make([]byte, 2)
		if _, err := io.ReadFull(br, h); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, h[1]&0x7f)
		_, _ = io.ReadFull(br, got)
		if string(got) != msg {
			t.Fatalf("websocket echoed %q, want %q", got, msg)
		}
	}

	// The relay ends when the app closes its stdin.
	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the relay didn't end when its stdin closed")
	}
}

func TestRelayWithoutDistro(t *testing.T) {
	d := fakeDistro(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, path, err := listenPrivate()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = (&Relay{Distro: d}).Serve(ctx, ln) }()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return dialPrivate(path) },
	}}
	resp, err := client.Get("http://agentbox/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "agentbox wsl init") {
		t.Fatalf("%s %s", resp.Status, b)
	}
}
