package chv

import (
	"context"
	"io"
)

// MakeDisks makes what the VM boots from, the parts not made yet: its root
// disk from Debian's cloud image, its pool disk for Incus, and the
// cloud-init seed that sets it up on first boot.
func MakeDisks(ctx context.Context, c Config, l Layout, log io.Writer) error {
	panic("TODO(image): MakeDisks")
}

// WaitProvisioned waits until the VM's first boot has finished setting it up
// (cloud-init), and fails with what went wrong if it didn't.
func WaitProvisioned(ctx context.Context, c Config, l Layout, log io.Writer) error {
	panic("TODO(image): WaitProvisioned")
}
