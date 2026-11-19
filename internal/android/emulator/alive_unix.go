//go:build unix

package emulator

import "syscall"

// alive reports whether process pid runs.
func alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}
