package machines

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// vncRelay is run by node in the machine: it connects to the display's VNC
// server, which browser.sh starts on 127.0.0.1:5900 of the machine's own
// network, and relays it over its stdin and stdout. Node is in the image
// for Playwright's MCP server, so this needs nothing added to it.
const vncRelay = `const s = require("net").connect(5900, "127.0.0.1");
process.stdin.pipe(s); s.pipe(process.stdout);
s.on("error", (e) => { process.stderr.write(e.message); process.exit(1); });
s.on("close", () => process.exit(0));
process.stdin.on("end", () => s.end());`

// DialVNC connects to the display's VNC server in the worktree's machine,
// through the runtime's exec: the server never listens anywhere but in the
// machine, and reaching it takes access to the runtime, as running anything
// in the machine does.
func (d *Docker) DialVNC(ctx context.Context, worktree string) (io.ReadWriteCloser, error) {
	return DialCommand(d.Command(ctx, worktree, "node", "-e", vncRelay))
}

// DialCommand starts cmd and makes its stdin and stdout one connection.
// Closing it ends cmd; what cmd wrote to stderr is the error of a read once
// it has exited with nothing more to say.
func DialCommand(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &cmdConn{cmd: cmd, in: in, out: out}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

type cmdConn struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    io.Reader
	stderr bytes.Buffer
	once   sync.Once
	err    error
}

func (c *cmdConn) Read(p []byte) (int, error) {
	n, err := c.out.Read(p)
	if errors.Is(err, io.EOF) && n == 0 {
		if werr := c.close(false); werr != nil {
			return 0, werr
		}
	}
	return n, err
}

func (c *cmdConn) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *cmdConn) Close() error {
	_ = c.close(true)
	return nil
}

// close waits for the command once, killing it first when it may not have
// ended by itself, and says why it failed, if it did.
func (c *cmdConn) close(kill bool) error {
	c.once.Do(func() {
		_ = c.in.Close()
		if kill {
			_ = c.cmd.Process.Kill()
		}
		if err := c.cmd.Wait(); err != nil && c.stderr.Len() > 0 {
			c.err = fmt.Errorf("%s", strings.TrimSpace(c.stderr.String()))
		}
	})
	return c.err
}
