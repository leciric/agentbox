package chv

import (
	"context"
	"io"

	"agentbox/internal/api"
	"agentbox/internal/paths"
)

// Supervise is `agentbox vm run`: it runs the VM in the foreground (passt,
// virtiofsd, cloud-hypervisor), forwards its sockets to the host, sizes its
// memory, and serves its state on p.VMSocket, until the VM powers off.
func Supervise(ctx context.Context, c Config, l Layout, p paths.Paths) error {
	panic("TODO(supervisor): Supervise")
}

// Start starts the supervisor in the background, detached from this process,
// and waits until the VM answers ssh. A VM that runs already is left alone;
// a paused one is resumed.
func Start(ctx context.Context, c Config, l Layout, p paths.Paths, log io.Writer) error {
	panic("TODO(supervisor): Start")
}

// Stop powers the VM off and waits for its supervisor to exit. With agents
// set, every running agent is stopped through the daemon first.
func Stop(ctx context.Context, c Config, l Layout, p paths.Paths, agents bool, log io.Writer) error {
	panic("TODO(supervisor): Stop")
}

func Pause(ctx context.Context, l Layout, p paths.Paths) error {
	panic("TODO(supervisor): Pause")
}

func Resume(ctx context.Context, l Layout, p paths.Paths) error {
	panic("TODO(supervisor): Resume")
}

// Status is the VM's state; it never fails, a VM it can't reach is off.
func Status(ctx context.Context, c Config, l Layout, p paths.Paths) api.VMStatus {
	panic("TODO(supervisor): Status")
}
