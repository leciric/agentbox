//go:build unix

package incus

import "syscall"

// access(2)'s mode bits for read and write. Go's syscall package leaves them
// unnamed on Linux, and they are the same everywhere POSIX is.
const (
	readOK  = 0x4
	writeOK = 0x2
)

// CanOpenSocket reports whether this process may open the Incus daemon's
// socket. It is one syscall, so the daemon can answer it on every request for
// its version, unlike a real `incus query`.
//
// What host setup changes lands here in two ways, and they differ: the
// incus-admin group, which a process only has if it was started from a login
// made after joining it, and an ACL for the user's UID, which every process of
// that user gets at once, including ones already running.
func CanOpenSocket() bool {
	return syscall.Access(SocketPath(), readOK|writeOK) == nil
}
