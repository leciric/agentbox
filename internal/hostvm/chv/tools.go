package chv

import (
	"context"
	"io"
)

// EnsureTools fetches the programs that run the VM into l.Bin, when they
// aren't there at the versions this build pins: cloud-hypervisor, its UEFI
// firmware (CLOUDHV.fd), virtiofsd and passt, all static, all checked
// against pinned SHA256 sums.
func EnsureTools(ctx context.Context, l Layout, log io.Writer) error {
	panic("TODO(image): EnsureTools")
}
