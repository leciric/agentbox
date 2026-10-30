package agent

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
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
