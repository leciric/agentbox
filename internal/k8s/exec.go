package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecOptions is one command run in an agent's pod.
type ExecOptions struct {
	// Command is the argv, run as the container's own user.
	Command []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// TTY asks for a terminal, which is what a shell needs. With one, the
	// output is one stream and Stderr has to be nil.
	TTY        bool
	Cols, Rows uint16
}

// Exec runs one command in an agent's pod and waits for it to finish, over the
// API server's exec (SPDY). It is the bootstrap path: it needs nothing from the
// agent and works before agentd has connected, which is what makes it the way to
// find out why agentd didn't. Everything after that goes through the gateway, one
// connection out of the pod, rather than through the API server.
func (c *Cluster) Exec(ctx context.Context, project, agent string, opts ExecOptions) error {
	if len(opts.Command) == 0 {
		return errors.New("no command to run")
	}
	if opts.TTY && opts.Stderr != nil {
		return errors.New("a terminal has one output stream: with TTY, Stderr has to be nil")
	}
	restConfig, err := c.RESTConfig()
	if err != nil {
		return err
	}
	request := c.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(c.namespace).
		Name(PodName(project, agent)).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "agent",
			Command:   opts.Command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", request.URL())
	if err != nil {
		return fmt.Errorf("opening a session to %s/%s: %w", project, agent, err)
	}
	stream := remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	}
	if opts.TTY && opts.Cols > 0 && opts.Rows > 0 {
		stream.TerminalSizeQueue = &fixedSize{cols: opts.Cols, rows: opts.Rows}
	}
	if err := exec.StreamWithContext(ctx, stream); err != nil {
		return fmt.Errorf("running %v in %s/%s: %w", opts.Command, project, agent, err)
	}
	return nil
}

// fixedSize is the one size a terminal opens at and keeps: this path is a single
// command, not an interactive session, so nothing resizes it.
type fixedSize struct{ cols, rows uint16 }

func (f *fixedSize) Next() *remotecommand.TerminalSize {
	size := remotecommand.TerminalSize{Width: f.cols, Height: f.rows}
	return &size
}
