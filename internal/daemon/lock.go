package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StopWait is how long a daemon that is stopping may take to let go of its
// lock: the jobs it cancels roll back before it exits (Run's s.jobs.wait).
const StopWait = 30 * time.Second

// lockPath is the lock a daemon holds for as long as it runs, next to its
// socket, so a daemon with a socket of its own (AGENTBOX_SOCKET) has a lock of
// its own too.
func lockPath(socket string) string { return socket + ".lock" }

// lockDaemon takes the lock only one daemon of a socket holds, for as long as
// it runs, and writes this process's pid in it. Answering on the socket isn't
// the same thing: a daemon that is stopping has stopped answering, but still
// runs until its jobs are rolled back, and then removes the socket, which by
// then may be the next daemon's. So a daemon waits up to wait for the one
// before it to be gone, rather than start beside it.
func lockDaemon(socket string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(lockPath(socket), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(wait); ; time.Sleep(100 * time.Millisecond) {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, fmt.Errorf("another AgentBox daemon%s is still running for %s", pidNote(socket), socket)
			}
			return nil, err
		}
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() { _ = f.Close() }, nil
}

// WaitStopped waits up to wait for no daemon to be running for socket, and
// reports whether none is. A daemon stops answering before it is gone, so
// whatever stops one to start another waits for this, not for the socket.
func WaitStopped(socket string, wait time.Duration) bool {
	for deadline := time.Now().Add(wait); ; time.Sleep(100 * time.Millisecond) {
		if !running(socket) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// StillRunning says which daemon holds socket's lock, for an error: " (pid
// 123)", or "" when the lock doesn't say.
func StillRunning(socket string) string { return pidNote(socket) }

// running reports whether a daemon holds socket's lock. A daemon too old to
// take the lock isn't seen.
func running(socket string) bool {
	f, err := os.Open(lockPath(socket))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}

func pidNote(socket string) string {
	b, err := os.ReadFile(lockPath(socket))
	if err != nil {
		return ""
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
		return fmt.Sprintf(" (pid %d)", pid)
	}
	return ""
}
