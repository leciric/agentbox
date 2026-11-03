package incus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/ws"
)

// Proc is a long-lived command running in an instance through the API, with
// its stdin and stdout as pipes for the caller: what `incus exec -T` gives a
// caller that starts it as a process, without the incus command in between.
//
// It exists for the chat's ACP adapters, which run for hours. Incus pings
// every exec websocket every 10 seconds, and the websocket library's default
// answer, which `incus exec` and Incus' own ExecInstance keep, writes the pong
// under a one-second deadline. A pong that can't be written in that second —
// a host short of memory stalls a process for longer than that, and a peer
// that isn't reading fills the socket — fails the connection for good: every
// later write returns the same "write unix @->/var/lib/incus/unix.socket: i/o
// timeout", and the incus command exits with it, taking the AI tool with it in
// the middle of whatever it was doing. Proc answers pings with no deadline, so
// a stall delays a pong rather than ending the stream.
type Proc struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser

	op     incusclient.Operation
	conns  []*websocket.Conn
	ctrl   *websocket.Conn
	ctrlMu sync.Mutex
	// output is closed once stdout and stderr have been copied out.
	output chan struct{}
	cancel context.CancelFunc
	ctx    context.Context
}

// UserStart starts a bash command in a login shell of user inside the
// instance, as UserExec runs it, and returns while it runs. What it writes to
// stderr goes to stderr.
func (c Client) UserStart(name, user, command string, stderr io.Writer) (*Proc, error) {
	return c.StartExec(name, []string{"runuser", "-l", user, "-c", command}, stderr)
}

// StartExec starts command in the instance through the API, its stdin and stdout
// left to the caller as pipes and its stderr copied to stderr. It needs the
// API: with Bin set there is no websocket to keep, and callers use Command.
func (c Client) StartExec(name string, command []string, stderr io.Writer) (*Proc, error) {
	if c.cli() {
		return nil, errors.New("incus: StartExec needs the API, not the incus command")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s, err := c.server(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	p, err := start(s, name, command, stderr)
	if err != nil {
		cancel()
		return nil, err
	}
	p.ctx, p.cancel = ctx, cancel
	return p, nil
}

func start(s incusclient.InstanceServer, name string, command []string, stderr io.Writer) (*Proc, error) {
	env := map[string]string{}
	if term, ok := os.LookupEnv("TERM"); ok {
		env["TERM"] = term
	}
	post := api.InstanceExecPost{Command: command, WaitForWS: true, Environment: env}
	op, _, err := s.RawOperation("POST", "/instances/"+url.PathEscape(name)+"/exec", post, "")
	if err != nil {
		return nil, err
	}
	fds := map[string]string{}
	if values, ok := op.Get().Metadata["fds"].(map[string]any); ok {
		for k, v := range values {
			if secret, ok := v.(string); ok {
				fds[k] = secret
			}
		}
	}
	p := &Proc{op: op, output: make(chan struct{})}
	dial := func(fd string) (*websocket.Conn, error) {
		if fds[fd] == "" {
			return nil, fmt.Errorf("incus exec %s: no websocket for %s", name, fd)
		}
		conn, err := s.GetOperationWebsocket(op.Get().ID, fds[fd])
		if err != nil {
			return nil, err
		}
		keepAlive(conn)
		p.conns = append(p.conns, conn)
		return conn, nil
	}
	fail := func(err error) (*Proc, error) {
		p.closeConns()
		_ = op.Cancel()
		return nil, err
	}
	// The order Incus' own client connects them in: control, then 0, 1, 2.
	if p.ctrl, err = dial(api.SecretNameControl); err != nil {
		return fail(err)
	}
	go drain(p.ctrl)
	in, err := dial("0")
	if err != nil {
		return fail(err)
	}
	go drain(in)
	out, err := dial("1")
	if err != nil {
		return fail(err)
	}
	errs, err := dial("2")
	if err != nil {
		return fail(err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	p.Stdin, p.Stdout = stdinW, stdoutR
	inDone := ws.MirrorRead(in, stdinR)
	outDone := ws.MirrorWrite(out, stdoutW)
	if stderr == nil {
		stderr = io.Discard
	}
	errDone := ws.MirrorWrite(errs, stderr)
	go func() {
		<-inDone
		_ = in.Close()
	}()
	go func() {
		<-outDone
		<-errDone
		_ = stdoutW.Close()
		close(p.output)
	}()
	return p, nil
}

// keepAlive answers the server's pings on conn with no write deadline: a pong
// that waits for a stalled host is late, where one under the library's default
// one-second deadline would break conn for every write after it.
func keepAlive(conn *websocket.Conn) {
	conn.SetPingHandler(func(data string) error {
		err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Time{})
		if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
			return err
		}
		return nil
	})
}

// drain reads conn until it closes, which is what answers its pings: the
// control and stdin websockets carry nothing the client reads.
func drain(conn *websocket.Conn) {
	for {
		if _, _, err := conn.NextReader(); err != nil {
			return
		}
	}
}

// Wait waits for the command to exit and its output to have been copied, and
// returns an ExitError for a status other than 0, or what went wrong in Incus.
func (p *Proc) Wait() error {
	defer p.closeConns()
	err := wait(p.ctx, p.op, nil)
	if p.ctx.Err() != nil {
		return errors.New("killed")
	}
	code := -1
	if status, ok := p.op.Get().Metadata["return"].(float64); ok {
		code = int(status)
	}
	if err != nil && code < 0 {
		return err
	}
	select {
	case <-p.output:
	case <-time.After(5 * time.Second): // as exec.Cmd's WaitDelay
	case <-p.ctx.Done():
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// Kill sends the command SIGKILL and stops waiting for it: Wait returns.
func (p *Proc) Kill() {
	p.ctrlMu.Lock()
	// Bounded: a wedged Incus mustn't hold the caller up.
	_ = p.ctrl.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = p.ctrl.WriteJSON(api.InstanceExecControl{Command: "signal", Signal: 9})
	p.ctrlMu.Unlock()
	p.cancel()
	p.closeConns()
}

func (p *Proc) closeConns() {
	for _, c := range p.conns {
		_ = c.Close()
	}
}
