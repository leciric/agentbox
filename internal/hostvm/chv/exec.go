package chv

import (
	"context"
	"io"
)

// SSHArgs is the command line that runs argv in the VM as its user, in
// workdir, over ssh on vsock (self is this agentbox, which is ssh's
// ProxyCommand: `agentbox vm proxy 22`). tty asks for a terminal.
func SSHArgs(c Config, l Layout, self, workdir string, tty bool, argv []string) []string {
	panic("TODO(supervisor): SSHArgs")
}

// Proxy is `agentbox vm proxy PORT`: it connects to the VM's vsock port and
// copies stdin to it and it to stdout, until either side closes.
func Proxy(ctx context.Context, l Layout, port uint32, stdin io.Reader, stdout io.Writer) error {
	panic("TODO(supervisor): Proxy")
}
