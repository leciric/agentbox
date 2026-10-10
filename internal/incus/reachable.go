package incus

import (
	"os"
	"os/exec"
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
