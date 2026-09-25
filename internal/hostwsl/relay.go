package hostwsl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// The relay carries the app's connections to the daemon's unix socket, which
// is in the distro, where Windows can't open it: WSL2 carries files and TCP
// between Windows and Linux, but not AF_UNIX.
//
//	app ── named pipe ──▶ agentbox.exe relay ══ yamux over one wsl.exe's stdio ══▶ agentbox wsl-bridge ──▶ agentbox.sock
//
// The pipe has a random name and only its user may open it, which keeps the
// socket's promise of answering only its own user (mode 0600): the user who can
// open the pipe is the user wsl.exe runs as, and the bridge connects to the
// socket as that Linux user. There is no token to leak and no TCP port for
// another user of the machine to reach.
//
// Each stream starts with one byte from the bridge: whether it reached the
// socket. When it didn't, the relay answers the connection itself with a 503
// the app recognises by its X-AgentBox-Relay header, and the app starts the
// daemon, as it does after ECONNREFUSED from the socket on Linux.

const (
	bridgeOK         byte = 0
	bridgeNoDaemon   byte = 1
	relayHeader           = "X-AgentBox-Relay"
	relayNotListened      = "not-listening"
)

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	// Media and screen streams are large; the default window of 256 KiB
	// round-trips too often through wsl.exe's pipes.
	cfg.MaxStreamWindowSize = 4 << 20
	return cfg
}

// Relay serves the app's connections, carrying each to the distro's daemon.
type Relay struct {
	Distro *Distro

	mu      sync.Mutex
	session *yamux.Session
	stderr  *tail
}

// Serve accepts connections on ln until ctx ends.
func (r *Relay) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go r.handle(ctx, c)
	}
}

func (r *Relay) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	stream, err := r.open(ctx)
	if err != nil {
		refuse(c, err.Error())
		return
	}
	defer stream.Close()
	status := make([]byte, 1)
	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(stream, status); err != nil {
		refuse(c, fmt.Sprintf("the relay to %s didn't answer: %v", r.Distro.Name, err))
		return
	}
	stream.SetReadDeadline(time.Time{})
	if status[0] != bridgeOK {
		refuse(c, "the AgentBox daemon isn't running in "+r.Distro.Name)
		return
	}
	pipe(c, stream)
}

// open opens a stream to the bridge, starting wsl.exe when there's no session
// or the last one ended: the distro was shut down, or wsl.exe was killed.
func (r *Relay) open(ctx context.Context) (net.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == nil || r.session.IsClosed() {
		s, err := r.start(ctx)
		if err != nil {
			return nil, err
		}
		r.session = s
	}
	stream, err := r.session.Open()
	if err != nil {
		r.session.Close()
		return nil, fmt.Errorf("the relay to %s failed: %v%s", r.Distro.Name, err, r.stderr.suffix())
	}
	return stream, nil
}

func (r *Relay) start(ctx context.Context) (*yamux.Session, error) {
	if err := r.Distro.ready(ctx); err != nil {
		return nil, err
	}
	// The bridge is the distro's agentbox, which has to be this one's.
	if _, err := r.Distro.EnsureBinary(ctx); err != nil {
		return nil, err
	}
	cmd := r.Distro.command(context.Background(), r.Distro.execArgs(false, "~", "agentbox", "wsl-bridge")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	r.stderr = &tail{}
	cmd.Stderr = r.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the relay into %s: %w", r.Distro.Name, err)
	}
	s, err := yamux.Client(&stdio{Reader: stdout, WriteCloser: stdin, cmd: cmd}, yamuxConfig())
	if err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	go func() {
		cmd.Wait()
		s.Close()
	}()
	return s, nil
}

// refuse answers a connection the relay can't carry, with what the daemon
// would have said had it been unreachable on Linux: an error in JSON, marked
// so the app knows no daemon saw the request.
func refuse(c net.Conn, msg string) {
	body := fmt.Sprintf(`{"error":%q}`, msg)
	// Read what the client sends while answering, so it isn't cut off
	// mid-request, and give it a moment to read the answer before closing.
	c.SetDeadline(time.Now().Add(5 * time.Second))
	go io.Copy(io.Discard, c)
	fmt.Fprintf(c, "HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/json\r\n%s: %s\r\nConnection: close\r\nContent-Length: %d\r\n\r\n%s",
		relayHeader, relayNotListened, len(body), body)
	time.Sleep(50 * time.Millisecond)
}

// pipe copies both ways until both are done.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		// Half-close where the connection can; otherwise close it, which
		// ends the other copy too.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			if cw.CloseWrite() == nil {
				return
			}
		}
		dst.Close()
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// stdio is a child process's stdout and stdin, as one connection.
type stdio struct {
	io.Reader
	io.WriteCloser
	cmd *exec.Cmd
}

func (s *stdio) Close() error {
	err := s.WriteCloser.Close()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	return err
}

// tail keeps the end of what wsl.exe says on stderr, for the error when the
// relay fails: "There is no distribution with the supplied name", say.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
	}
	return len(p), nil
}

func (t *tail) suffix() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := strings.TrimSpace(decode(t.buf)); s != "" {
		return ": " + lastLine(s)
	}
	return ""
}

// Bridge is the relay's end in the distro: `agentbox wsl-bridge`, a yamux
// server on its stdin and stdout, which connects each stream to the daemon's
// socket as the user it runs as. It returns when its stdin ends: wsl.exe, or
// the relay, has gone.
func Bridge(rw io.ReadWriteCloser, socket string) error {
	s, err := yamux.Server(rw, yamuxConfig())
	if err != nil {
		return err
	}
	defer s.Close()
	for {
		stream, err := s.Accept()
		if err != nil {
			if s.IsClosed() || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		go func() {
			defer stream.Close()
			conn, err := net.DialTimeout("unix", socket, 5*time.Second)
			if err != nil {
				stream.Write([]byte{bridgeNoDaemon})
				return
			}
			defer conn.Close()
			if _, err := stream.Write([]byte{bridgeOK}); err != nil {
				return
			}
			pipe(stream, conn)
		}()
	}
}

// ReadSocketLine reads the relay's first line of output, which says where it
// listens: the app reads it the same way (desktop/src/main/relay.ts).
func ReadSocketLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return "", err
	}
	const prefix = "listening "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("the relay said %q", strings.TrimSpace(line))
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), nil
}
