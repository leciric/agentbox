package homehost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Hub is the daemon's side: the helper connected now, if any.
type Hub struct {
	// OnChange is called, outside the lock, when a helper connects or goes.
	OnChange func(connected bool)

	mu  sync.Mutex
	cur *Runner
}

// ErrGone is a helper that went while it was being asked something: the app
// on the host closed, or the VM is stopping.
var ErrGone = errors.New("the AgentBox app on your computer closed")

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.MaxStreamWindowSize = 4 << 20
	return cfg
}

// Attach serves a helper's connection until it ends. A helper connecting
// while another is attached replaces it: the newer app is the one running.
func (h *Hub) Attach(ctx context.Context, conn net.Conn) error {
	sess, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		return err
	}
	r := &Runner{sess: sess}
	h.mu.Lock()
	old := h.cur
	h.cur = r
	h.mu.Unlock()
	if old != nil {
		_ = old.sess.Close()
	}
	if h.OnChange != nil {
		h.OnChange(true)
	}
	select {
	case <-sess.CloseChan():
	case <-ctx.Done():
		_ = sess.Close()
	}
	h.mu.Lock()
	still := h.cur == r
	if still {
		h.cur = nil
	}
	h.mu.Unlock()
	if still && h.OnChange != nil {
		h.OnChange(false)
	}
	return nil
}

// Runner is the helper attached now, or nil.
func (h *Hub) Runner() *Runner {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cur != nil && h.cur.sess.IsClosed() {
		return nil
	}
	return h.cur
}

// Runner is one connected helper.
type Runner struct {
	sess *yamux.Session
}

// ask opens a stream and sends req on it.
func (r *Runner) ask(req request) (net.Conn, *bufio.Reader, error) {
	s, err := r.sess.Open()
	if err != nil {
		return nil, nil, ErrGone
	}
	if err := writeLine(s, req); err != nil {
		_ = s.Close()
		return nil, nil, ErrGone
	}
	return s, bufio.NewReader(s), nil
}

// answer reads req's last answer, passing any status lines before it on.
func answer(ctx context.Context, s net.Conn, br *bufio.Reader, status func(string)) (reply, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.SetReadDeadline(time.Now()) })
	defer stop()
	for {
		var rep reply
		if err := readLine(br, &rep); err != nil {
			if ctx.Err() != nil {
				return reply{}, ctx.Err()
			}
			return reply{}, ErrGone
		}
		if rep.Status != "" && rep.Error == "" && rep.Info == nil && rep.ID == "" && rep.Exit == nil {
			if status != nil {
				status(rep.Status)
			}
			continue
		}
		if rep.Error != "" {
			return rep, errors.New(rep.Error)
		}
		return rep, nil
	}
}

// Probe asks the host what it has.
func (r *Runner) Probe(ctx context.Context) (Info, error) {
	s, br, err := r.ask(request{Op: opProbe})
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = s.Close() }()
	rep, err := answer(ctx, s, br, nil)
	if err != nil {
		return Info{}, err
	}
	if rep.Info == nil {
		return Info{}, errors.New("the host said nothing about itself")
	}
	return *rep.Info, nil
}

// Proc is an adapter running on the host.
type Proc struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	// Cwd is its working directory there, and MCP the servers its session
	// starts with: the host's paths, which the daemon has no other way to know.
	Cwd string
	MCP []MCPServer

	r      *Runner
	id     string
	done   chan struct{}
	err    error
	stderr string
	once   sync.Once
}

// Spawn starts spec on the host. status passes on what it is doing, such as
// installing the adapter there.
func (r *Runner) Spawn(ctx context.Context, spec Spec, status func(string)) (*Proc, error) {
	s, br, err := r.ask(request{Op: opSpawn, Spec: &spec})
	if err != nil {
		return nil, err
	}
	rep, err := answer(ctx, s, br, status)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	p := &Proc{
		Stdin: s, Stdout: &streamReader{br: br, s: s}, Cwd: rep.Cwd, MCP: rep.MCP,
		r: r, id: rep.ID, done: make(chan struct{}),
	}
	go p.watch()
	return p, nil
}

type streamReader struct {
	br *bufio.Reader
	s  net.Conn
}

func (sr *streamReader) Read(b []byte) (int, error) { return sr.br.Read(b) }
func (sr *streamReader) Close() error               { return sr.s.Close() }

// watch waits for the process to end, on a stream of its own.
func (p *Proc) watch() {
	defer close(p.done)
	s, br, err := p.r.ask(request{Op: opWait, ID: p.id})
	if err != nil {
		p.err = err
		return
	}
	defer func() { _ = s.Close() }()
	rep, err := answer(context.Background(), s, br, nil)
	switch {
	case err != nil:
		p.err = err
	case rep.Exit != nil && *rep.Exit != 0:
		p.err = fmt.Errorf("exit status %d", *rep.Exit)
	}
	p.stderr = rep.Stderr
}

// Stop ends the process: it closes its input, and kills it if it doesn't exit.
func (p *Proc) Stop() {
	p.once.Do(func() {
		_ = p.Stdin.Close()
		select {
		case <-p.done:
			return
		case <-time.After(3 * time.Second):
		}
		if s, _, err := p.r.ask(request{Op: opKill, ID: p.id}); err == nil {
			_ = s.Close()
		}
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	})
}

// Wait returns once the process has exited, or the helper has gone.
func (p *Proc) Wait() error {
	<-p.done
	return p.err
}

// Stderr is the end of what it wrote to stderr, once it has exited. Its
// output ending comes a moment before the wait's answer, so it waits for that
// a little.
func (p *Proc) Stderr() string {
	select {
	case <-p.done:
		return p.stderr
	case <-time.After(2 * time.Second):
		return ""
	}
}
