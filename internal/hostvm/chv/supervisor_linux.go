package chv

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// childAttr makes a program the supervisor runs die with it (Pdeathsig), so
// a supervisor that crashed can't leave a VM running that nothing controls.
func childAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

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
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return err == nil && strings.Contains(string(cmdline), "\x00vm\x00run")
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

// hostMemory is the host's physical memory in bytes, or 0 if it won't say.
func hostMemory() int64 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	return int64(info.Totalram) * int64(info.Unit)
}

// maxOpenFiles is RLIMIT_NOFILE's hard limit, which the supervisor's children
// may raise theirs to; 0 if it won't say.
func maxOpenFiles() uint64 {
	var l syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &l); err != nil {
		return 0
	}
	return l.Max
}

// lockHolder is the supervisor holding a flock on file, from /proc/locks, or
// 0.
func lockHolder(file string) int {
	var st syscall.Stat_t
	if err := syscall.Stat(file, &st); err != nil {
		return 0
	}
	b, err := os.ReadFile("/proc/locks")
	if err != nil {
		return 0
	}
	for _, pid := range lockHolders(string(b), st.Ino) {
		if supervisorAlive(pid) {
			return pid
		}
	}
	return 0
}
