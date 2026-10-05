package agent

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

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
