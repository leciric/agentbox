package hostwsl

import (
	"crypto/rand"
	"encoding/hex"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// listenPrivate listens on a named pipe only this user can open. The name is
// random, so no other user can have made it first and wait for the app to
// connect, and FILE_FLAG_FIRST_PIPE_INSTANCE, which go-winio sets on the first
// instance, fails the listen if the name is taken anyway.
func listenPrivate() (net.Listener, string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", err
	}
	path := `\\.\pipe\agentbox-` + hex.EncodeToString(b[:])
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", err
	}
	// Protected (no inherited entries), and full access for this user alone.
	sd := "D:P(A;;GA;;;" + user.User.Sid.String() + ")"
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sd, InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
	if err != nil {
		return nil, "", err
	}
	return ln, path, nil
}

// dialPrivate connects to what listenPrivate made; tests use it.
func dialPrivate(path string) (net.Conn, error) { return winio.DialPipe(path, nil) }
