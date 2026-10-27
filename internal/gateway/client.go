package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Client is a pod's side of the gateway: it connects out to the master, keeps
// the connection open, and hands each stream the master opens to Handle.
//
// It reconnects on its own, with a growing wait between attempts, so a master
// that restarts or a network that blinks costs a pod nothing but a pause.
type Client struct {
	// URL is the master as this pod reaches it, like http://host.k3d.internal:7782
	// or https://monster.example.ts.net. Path is appended to it.
	URL   string
	Token string
	// Handle serves one stream: the operation's header, then its frames.
	Handle func(ctx context.Context, h Header, s *Stream) error
	// Log is where a connection's trouble is recorded; nil discards it.
	Log func(format string, args ...any)
}

func (c *Client) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// Run connects and serves until ctx is cancelled, reconnecting as it has to.
// It returns ctx's error, never a connection's: a pod whose master is away
// waits for it.
func (c *Client) Run(ctx context.Context) error {
	wait := time.Second
	for {
		started := time.Now()
		err := c.serve(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			c.logf("gateway: %v", err)
		}
		// A connection that lasted a while starts over at the first wait, so
		// a master restarted once a day reconnects at once rather than slowly.
		if time.Since(started) > 30*time.Second {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
	}
}

// Endpoint is the URL the client connects to.
func (c *Client) Endpoint() (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(c.URL), "/")
	if base == "" {
		return "", errors.New("no master URL: the pod has nothing to connect to")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return "", fmt.Errorf("the master URL %q is neither http nor https", base)
	}
	return base + Path, nil
}

// serve is one connection, from the WebSocket to its last stream.
func (c *Client) serve(ctx context.Context) error {
	endpoint, err := c.Endpoint()
	if err != nil {
		return err
	}
	if c.Token == "" {
		return errors.New("no token: the master can't tell which agent this is")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, endpoint, &websocket.DialOptions{
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + c.Token}},
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", endpoint, err)
	}
	defer func() { _ = conn.CloseNow() }()
	// The connection outlives the dial's context, which is cancelled on
	// return; a session's life is the Run context's.
	mux, err := yamux.Client(websocket.NetConn(ctx, conn, websocket.MessageBinary), config())
	if err != nil {
		return fmt.Errorf("multiplexing the connection: %w", err)
	}
	defer func() { _ = mux.Close() }()
	c.logf("gateway: connected to %s", c.URL)
	slots := make(chan struct{}, MaxStreams)
	for {
		stream, err := mux.Accept()
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			// More streams at once than the limit: the master is told no
			// rather than the pod being allowed to exhaust itself.
			_ = stream.Close()
			continue
		}
		go func() {
			defer func() { <-slots }()
			defer func() { _ = stream.Close() }()
			st := NewStream(stream)
			h, err := st.ReadHeader()
			if err != nil {
				c.logf("gateway: reading a stream's header: %v", err)
				return
			}
			streamCtx := ctx
			if h.TimeoutMs > 0 {
				var cancel context.CancelFunc
				streamCtx, cancel = context.WithTimeout(ctx, time.Duration(h.TimeoutMs)*time.Millisecond)
				defer cancel()
			}
			if c.Handle == nil {
				_ = st.Write(FrameStderr, []byte("this agentd serves no operations\n"))
				_ = st.Exit(1)
				return
			}
			if err := c.Handle(streamCtx, h, st); err != nil {
				c.logf("gateway: %s: %v", h.Op, err)
			}
		}()
	}
}
