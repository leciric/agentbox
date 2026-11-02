package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"agentbox/hubapi"
)

// testConnector is an environment, for the hub's own tests: it dials the
// tunnel and serves a handler back through it.
//
// The real one is AgentBox's, in agentbox/internal/remote, which this
// repository doesn't import — the two halves meet at hubapi and nowhere else.
// This stands in for it, and deliberately does only what the protocol says:
// dial PathConnect with a bearer environment token, announce the version and
// hostname, then be the yamux server on the connection. It has none of the
// real connector's reconnect backoff.
type testConnector struct {
	Hub      string
	Token    string
	Handler  http.Handler
	Version  string
	Hostname string

	mu        sync.Mutex
	connected bool
	lastError string
}

type connectorStatus struct {
	Connected bool
	LastError string
}

func (c *testConnector) Status() connectorStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return connectorStatus{Connected: c.connected, LastError: c.lastError}
}

func (c *testConnector) set(connected bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = connected
	c.lastError = ""
	if err != nil {
		c.lastError = err.Error()
	}
}

// Run connects until ctx ends, trying again after the connection drops. The
// real connector backs off; this one retries quickly, so tests don't wait.
func (c *testConnector) Run(ctx context.Context) {
	for ctx.Err() == nil {
		c.set(false, c.connect(ctx))
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	c.set(false, nil)
}

func (c *testConnector) connect(ctx context.Context) error {
	conn, resp, err := websocket.Dial(ctx, hubapi.WebSocketURL(c.Hub)+hubapi.PathConnect, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":       {"Bearer " + c.Token},
			hubapi.HeaderVersion:  {c.Version},
			hubapi.HeaderHostname: {c.Hostname},
		},
	})
	if err != nil {
		if resp != nil {
			// The status is what the hub said; the real connector turns
			// 401 into a sentence about the environment's token.
			return fmt.Errorf("connecting: status %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("connecting: %w", err)
	}
	conn.SetReadLimit(-1)
	session, err := yamux.Server(websocket.NetConn(ctx, conn, websocket.MessageBinary), hubapi.TunnelConfig())
	if err != nil {
		_ = conn.CloseNow()
		return err
	}
	defer func() { _ = session.Close() }()
	c.set(true, nil)
	srv := &http.Server{Handler: c.Handler, ReadHeaderTimeout: 30 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()
	_ = srv.Serve(session)
	return errors.New("the connection to the hub closed")
}
