//go:build !windows

package hostwsl

import (
	"net"
	"os"
	"path/filepath"
)

// listenPrivate stands in for the named pipe off Windows, in tests and demos:
// a unix socket in a directory of its own, which only this user can enter.
func listenPrivate() (net.Listener, string, error) {
	dir, err := os.MkdirTemp("", "agentbox-relay-")
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "relay.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", err
	}
	return &removing{Listener: ln, dir: dir}, path, nil
}

type removing struct {
	net.Listener
	dir string
}

func (r *removing) Close() error {
	err := r.Listener.Close()
	_ = os.RemoveAll(r.dir)
	return err
}

func dialPrivate(path string) (net.Conn, error) { return net.Dial("unix", path) }
