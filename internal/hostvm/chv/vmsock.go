package chv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// dialVsock connects to a port the VM listens on over vsock, through Cloud
// Hypervisor's hybrid vsock: a unix socket on the host (--vsock socket=) that
// takes "CONNECT <port>\n", answers "OK <host port>\n", and is then the
// stream. It needs no vhost-vsock device on the host, nor root.
func dialVsock(ctx context.Context, socket string, port uint32) (*net.UnixConn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connecting to the VM's vsock: %w", err)
	}
	conn := c.(*net.UnixConn)
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err := vsockHandshake(conn, port); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// errVsockRefused is a port nothing in the VM listens on (yet): Cloud
// Hypervisor closes the connection instead of answering OK.
var errVsockRefused = errors.New("nothing in the VM answers on that vsock port")

func vsockHandshake(conn net.Conn, port uint32) error {
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		return fmt.Errorf("vsock CONNECT %d: %w", port, err)
	}
	// One byte at a time, so nothing after the answer's newline — the
	// stream's first bytes — is read into a buffer here and lost.
	var line []byte
	b := make([]byte, 1)
	for len(line) < 64 {
		n, err := conn.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			line = append(line, b[0])
			continue
		}
		if err != nil {
			if len(line) == 0 {
				return fmt.Errorf("vsock port %d: %w", port, errVsockRefused)
			}
			return fmt.Errorf("vsock CONNECT %d: %w", port, err)
		}
	}
	if !strings.HasPrefix(string(line), "OK ") {
		return fmt.Errorf("vsock CONNECT %d: unexpected answer %q", port, line)
	}
	return nil
}
