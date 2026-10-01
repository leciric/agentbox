//go:build linux || darwin

package chv

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// detachedAttr starts the supervisor in a session of its own, so it outlives
// the command that started it and the terminal it ran in.
func detachedAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// lockFile takes an exclusive lock on file, or fails at once when another
// supervisor holds it.
func lockFile(file string) (func(), error) {
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// supervisorAlive reports whether pid is a supervisor that still runs: a pid
// file a killed one left can name another process by now.
func supervisorAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	return strings.Contains(processArgs(pid), "\x00vm\x00run")
}

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// allocated is what a file takes on disk, which for a sparse disk image is
// less than its size.
func allocated(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}

// hostFree is what's free on the file system that holds dir, to its user, or
// 0 when that can't be read.
func hostFree(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
