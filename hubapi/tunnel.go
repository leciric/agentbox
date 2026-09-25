package hubapi

import (
	"io"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

// The tunnel is one WebSocket from the environment to the hub, carrying yamux
// streams. The hub opens a stream for every request it proxies, and speaks
// plain HTTP/1.1 on it, so the environment serves the tunnel with the same
// handler as its unix socket: requests, the event stream, WebSockets, media.
//
// The environment dials PathConnect with its environment token and is the yamux
// server on the connection; the hub accepts and is the yamux client. Both sides
// configure the session with TunnelConfig, so their windows and keepalives
// agree.

// TunnelConfig is how both ends set up the yamux session over the WebSocket.
func TunnelConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 15 * time.Second
	c.ConnectionWriteTimeout = 30 * time.Second
	c.StreamOpenTimeout = 30 * time.Second
	c.MaxStreamWindowSize = 8 << 20 // browser and Android views are bulky
	c.LogOutput = io.Discard
	return c
}

// WebSocketURL turns a hub's http(s):// address into the ws(s):// one its
// tunnel is dialled on.
func WebSocketURL(base string) string {
	base = strings.TrimRight(base, "/")
	if rest, ok := strings.CutPrefix(base, "https://"); ok {
		return "wss://" + rest
	}
	if rest, ok := strings.CutPrefix(base, "http://"); ok {
		return "ws://" + rest
	}
	return base
}
