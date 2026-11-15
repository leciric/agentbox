package homehost

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

// dialSocket connects to the app's relay pipe into AgentBox's WSL distro
// (hostwsl.Relay), which the app names in SocketEnv.
func dialSocket(ctx context.Context, socket string) (net.Conn, error) {
	if strings.HasPrefix(socket, `\\.\pipe\`) {
		return winio.DialPipeContext(ctx, socket)
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}
