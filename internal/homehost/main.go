package homehost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/paths"
)

const usage = `usage: agentbox home-host [--with-app]
       agentbox home-host mcp

Runs the Home chat's AI tool on this computer for the AgentBox daemon, which
reaches it over its own socket. The desktop app starts it, with --with-app:
it then ends when the app does. "mcp" is the MCP server the tool starts, which
carries AgentBox's tools from the daemon.`

// Main is `agentbox home-host`, on the user's computer.
func Main(args []string, version string) int {
	socket := os.Getenv(SocketEnv)
	if socket == "" {
		p, err := paths.Default()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		socket = p.Socket()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch {
	case len(args) == 1 && args[0] == "mcp":
		if err := carryMCP(ctx, socket); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	case len(args) == 0, len(args) == 1 && args[0] == "--with-app":
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	if len(args) == 1 {
		// The app holds our stdin: it closing is the app gone.
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			cancel()
		}()
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	WidenPath()
	h := &Host{
		Version: version, Self: self, Socket: socket,
		Dir:   filepath.Join(p.Data, "home-chat"),
		Tools: filepath.Join(p.Data, "home-tools"),
	}
	logger := log.New(os.Stderr, "home-host: ", log.LstdFlags)
	run(ctx, socket, h, logger)
	return 0
}

// run keeps the helper connected to the daemon until ctx ends, dialling again
// whenever the daemon restarts or the VM starts.
func run(ctx context.Context, socket string, h *Host, logger *log.Logger) {
	wait := time.Second
	said := ""
	for ctx.Err() == nil {
		conn, err := Dial(ctx, socket, Path)
		if err != nil {
			if msg := err.Error(); msg != said {
				logger.Printf("waiting for the daemon: %v", err)
				said = msg
			}
			select {
			case <-ctx.Done():
			case <-time.After(wait):
			}
			wait = min(wait*2, 10*time.Second)
			continue
		}
		logger.Printf("connected: the Home chat runs here")
		wait, said = time.Second, ""
		_ = h.Serve(ctx, conn)
		logger.Printf("the daemon went")
	}
}

// Dial opens a WebSocket to the daemon at path, as a stream.
func Dial(ctx context.Context, socket, path string) (net.Conn, error) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dialSocket(ctx, socket) },
	}}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, "ws://agentbox"+path, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("the daemon is older than this app, and can't run the Home chat here")
		}
		return nil, err
	}
	return websocket.NetConn(ctx, ws, websocket.MessageBinary), nil
}

// carryMCP carries this process's stdio to `agentbox mcp` in the daemon's
// machine, as the Home chat's.
func carryMCP(ctx context.Context, socket string) error {
	conn, err := Dial(ctx, socket, MCPPath+"?session="+url.QueryEscape(os.Getenv(SessionEnv)))
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
		close(done)
	}()
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		_ = conn.Close()
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	_ = conn.Close()
	return nil
}
