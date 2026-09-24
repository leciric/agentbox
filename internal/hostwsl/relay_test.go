package hostwsl

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
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
)

var fakeDir string

func TestMain(m *testing.M) {
	code := m.Run()
	if fakeDir != "" {
		os.RemoveAll(fakeDir)
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
	t.Cleanup(func() { os.RemoveAll(sockDir) })
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
	os.WriteFile(tarball, []byte("not really"), 0o644)
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
	os.WriteFile(d.Binary, []byte("a new version"), 0o755)
	if got, err := d.EnsureBinary(ctx); err != nil || !got {
		t.Fatalf("after an upgrade: %v, %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(d.fakeEnv("FAKEWSL_ROOT"), "usr", "local", "bin", "agentbox"))
	if string(b) != "a new version" {
		t.Fatalf("the distro has %q", b)
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
	go io.Copy(io.Discard, outR)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return dialPrivate(path) },
	}}
	get := func() (*http.Response, string) {
		t.Helper()
		resp, err := client.Get("http://agentbox/v1/version")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	// No daemon yet: the relay answers for it, so the app starts one.
	resp, body := get()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get(relayHeader) != relayNotListened {
		t.Fatalf("with no daemon: %s %q", resp.Status, body)
	}

	if err := d.StartDaemon(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodPost, "http://agentbox/v1/shutdown", nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	})
	if resp, body := get(); resp.StatusCode != http.StatusOK || body != `{"version":"fake"}` {
		t.Fatalf("with the daemon: %s %q", resp.Status, body)
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
			defer resp.Body.Close()
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
	defer conn.Close()
	var key [16]byte
	rand.Read(key[:])
	fmt.Fprintf(conn, "GET /v1/echo HTTP/1.1\r\nHost: agentbox\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", base64.StdEncoding.EncodeToString(key[:]))
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
		conn.Write(frame)
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		h := make([]byte, 2)
		if _, err := io.ReadFull(br, h); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, h[1]&0x7f)
		io.ReadFull(br, got)
		if string(got) != msg {
			t.Fatalf("websocket echoed %q, want %q", got, msg)
		}
	}

	// The relay ends when the app closes its stdin.
	inW.Close()
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
	go (&Relay{Distro: d}).Serve(ctx, ln)
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return dialPrivate(path) },
	}}
	resp, err := client.Get("http://agentbox/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "agentbox wsl init") {
		t.Fatalf("%s %s", resp.Status, b)
	}
}
