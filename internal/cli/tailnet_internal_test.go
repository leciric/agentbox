package cli

import "testing"

// TestServeURL checks the address a client opens, as tailscale spells the
// handler: the default port isn't written, and the root path has no trailing
// slash.
func TestServeURL(t *testing.T) {
	for _, c := range []struct {
		host string
		port int
		path string
		want string
	}{
		{"machine.example.ts.net", 443, "/", "https://machine.example.ts.net"},
		{"machine.example.ts.net", 443, "/agentbox", "https://machine.example.ts.net/agentbox"},
		{"machine.example.ts.net", 8443, "/agentbox", "https://machine.example.ts.net:8443/agentbox"},
		{"machine.example.ts.net", 8443, "/", "https://machine.example.ts.net:8443"},
	} {
		if got := serveURL(c.host, c.port, c.path); got != c.want {
			t.Errorf("serveURL(%q, %d, %q) = %q, want %q", c.host, c.port, c.path, got, c.want)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	if host, port := splitHostPort("machine.example.ts.net:8443"); host != "machine.example.ts.net" || port != 8443 {
		t.Errorf("splitHostPort() = %q, %d", host, port)
	}
	if host, port := splitHostPort("machine.example.ts.net"); host != "machine.example.ts.net" || port != defaultTailnetServePort {
		t.Errorf("splitHostPort() without a port = %q, %d, want the default", host, port)
	}
}

func TestSameProxy(t *testing.T) {
	for _, c := range []struct {
		proxy, addr string
		want        bool
	}{
		{"http://127.0.0.1:7781", "127.0.0.1:7781", true},
		{"http://127.0.0.1:7781/", "127.0.0.1:7781", true},
		{"http://127.0.0.1:7781", "127.0.0.1:7782", false},
		{"http://127.0.0.1:8787", "127.0.0.1:7781", false},
	} {
		if got := sameProxy(c.proxy, c.addr); got != c.want {
			t.Errorf("sameProxy(%q, %q) = %v, want %v", c.proxy, c.addr, got, c.want)
		}
	}
}
