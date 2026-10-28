//go:build unix

package daemon

import (
	"syscall"

	"agentbox/internal/api"
)

// ownFiles is the daemon's open files against its soft RLIMIT_NOFILE.
func ownFiles() api.FileCount {
	c := api.FileCount{Open: countOwnFDs()}
	var l syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &l); err == nil && l.Cur < 1<<62 {
		c.Limit = int64(l.Cur)
	}
	return c
}
