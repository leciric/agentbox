package incus

import (
	"os"
	"os/exec"
	"syscall"
)

// DefaultSocket is where the Incus daemon listens for local clients: Incus' own
// var path, which its systemd socket unit listens on and which the incus
// command connects to. Whoever can open it is trusted by the daemon, so this is
// the file host setup puts your user's ACL on.
const DefaultSocket = "/var/lib/incus/unix.socket"

// SocketPath is the socket the incus command would use: INCUS_SOCKET when it's
// set, as the command itself reads it, and otherwise DefaultSocket.
func SocketPath() string {
	if s := os.Getenv("INCUS_SOCKET"); s != "" {
		return s
	}
	return DefaultSocket
}

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

// Reachable reports whether this process could use Incus right now: the incus
// command is on its PATH and it may open the daemon's socket. A daemon that
// says no while the process asking says yes is one that started before host
// setup, and has to be restarted to see Incus.
func (c Client) Reachable() bool {
	if _, err := exec.LookPath(c.Path()); err != nil {
		return false
	}
	return CanOpenSocket()
}
