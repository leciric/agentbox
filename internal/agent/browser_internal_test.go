package agent

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"agentbox/internal/state"
)

// TestDisplayUpReadsTheVNCGreeting is what lets the Desktop tab show a desktop
// with no browser on it: the proxy device accepts the connection whether or
// not anything listens inside the agent, so only the RFB greeting says the
// display is really there.
func TestDisplayUpReadsTheVNCGreeting(t *testing.T) {
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
