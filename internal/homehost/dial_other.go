//go:build !windows

package homehost

import (
	"context"
	"net"
)

func dialSocket(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}
