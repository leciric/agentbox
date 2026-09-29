package chv

import (
	"context"
	"io"
	"time"

	"agentbox/internal/api"
)

// machine is what the supervisor runs the VM with: Cloud Hypervisor and the
// programs beside it, on Linux (chMachine), or Apple's Virtualization
// framework in the supervisor's own process, on a Mac (vzMachine). Whatever
// runs it, the VM is the same: Debian's cloud image set up by the same seed,
// the host's home shared at its own path, and its ports reached over vsock
// through Layout.VsockSocket, which takes Cloud Hypervisor's hybrid vsock
// handshake either way (vmsock.go), so ssh, the forwards and `vm proxy` don't
// know which it is.
type machine interface {
	// boot starts the VM, and returns once it runs.
	boot(ctx context.Context) error
	// done is closed once the VM has stopped; exitErr is then why, or nil
	// when the guest powered off.
	done() <-chan struct{}
	exitErr() error
	running() bool
	pause(ctx context.Context) error
	resume(ctx context.Context) error
	// powerOff presses the VM's power button (resuming a paused one first)
	// and waits up to timeout for the guest to power off: false when it
	// didn't in time. An error is a button it couldn't press.
	powerOff(timeout time.Duration) (bool, error)
	// halt ends the VM at once, if it runs, and everything it ran with.
	halt()
	// setCPUs and setMemory resize the running VM, within live.
	setCPUs(ctx context.Context, n int) error
	setMemory(ctx context.Context, bytes int64) error
	// granted is the memory the guest has now, or 0 when it won't say.
	granted(ctx context.Context) int64
	// resident is what the VM holds of the host's memory, for the memory
	// policy, and what everything running it holds, for the status; 0 when
	// unknown.
	resident() (vm, all int64)
	// live is how far the running VM can be resized without a restart.
	live(c Config) api.VMLimits
	// balloon is what the VM booted with when its memory is sized with a
	// balloon over it (vz), or 0 when memory is plugged in and out of it
	// (virtio-mem).
	balloon() int64
	// runFiles are the files the VM runs with, which a supervisor that died
	// leaves behind.
	runFiles() []string
}

// newMachine is what runs c's VM on this host.
func newMachine(c Config, l Layout, log io.Writer, logf func(string, ...any)) (machine, error) {
	if c.VZ() {
		return newVZMachine(c, l, logf)
	}
	return &chMachine{c: c, l: l, log: log, logf: logf, ch: newCHClient(l.APISocket()), room: roomFor(c, hostCPUs(), hostMemory())}, nil
}
