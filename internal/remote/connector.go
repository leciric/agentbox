// Package remote is this machine's side of a hub: it keeps the daemon
// connected to one as an environment, and serves the daemon's API back through
// the tunnel.
//
// The hub itself — accounts, environments, and the proxy that reaches them — is
// a separate program. All this package knows about it is agentbox/hubapi.
package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"agentbox/hubapi"
)

// ConnectorStatus is how an environment's connection to its hub is doing.
type ConnectorStatus struct {
	Connected bool
	Since     time.Time // when it connected, or started failing
	LastError string
}

// Connector keeps an environment connected to a hub: it dials the hub's tunnel
// endpoint with the environment's token, serves Handler to the hub over the
// connection, and reconnects with backoff when it drops.
type Connector struct {
	Hub      string // the hub's URL, like https://hub.example.com
	Token    string // the environment's token
	Handler  http.Handler
	Version  string
	Hostname string
	Log      io.Writer

	mu     sync.Mutex
	status ConnectorStatus
}

func (c *Connector) Status() ConnectorStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Connector) setStatus(connected bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if connected != c.status.Connected || c.status.Since.IsZero() {
		c.status.Since = time.Now()
	}
	c.status.Connected = connected
	c.status.LastError = ""
	if err != nil {
		c.status.LastError = err.Error()
	}
}

func (c *Connector) logf(format string, args ...any) {
	if c.Log != nil {
		fmt.Fprintf(c.Log, time.Now().Format(time.DateTime)+" "+format+"\n", args...)
	}
}

// Run connects until ctx ends.
func (c *Connector) Run(ctx context.Context) {
	backoff := time.Second
	for {
		started := time.Now()
		err := c.connect(ctx)
		if ctx.Err() != nil {
			c.setStatus(false, nil)
			return
		}
		c.setStatus(false, err)
		c.logf("hub %s: %v", c.Hub, err)
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		select {
		case <-ctx.Done():
			c.setStatus(false, nil)
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (c *Connector) connect(ctx context.Context) error {
	conn, resp, err := websocket.Dial(ctx, hubapi.WebSocketURL(c.Hub)+hubapi.PathConnect, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":       {"Bearer " + c.Token},
			hubapi.HeaderVersion:  {c.Version},
			hubapi.HeaderHostname: {c.Hostname},
		},
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errors.New("the hub doesn't know this environment's token: it was deleted, or mistyped")
		}
		return fmt.Errorf("connecting: %w", err)
	}
	conn.SetReadLimit(-1)
	session, err := yamux.Server(websocket.NetConn(ctx, conn, websocket.MessageBinary), hubapi.TunnelConfig())
	if err != nil {
		conn.CloseNow()
		return err
	}
	defer session.Close()
	c.setStatus(true, nil)
	c.logf("connected to the hub %s", c.Hub)
	srv := &http.Server{Handler: c.Handler, ReadHeaderTimeout: 30 * time.Second}
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	srv.Serve(session)
	return errors.New("the connection to the hub closed")
}
