package image

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// tailLines is how much of a failed script's output goes into its error.
const tailLines = 15

// tailWriter passes everything on to w and keeps the last bytes written, so a
// failed script's error can carry the end of its output rather than only its
// exit status.
type tailWriter struct {
	w   io.Writer
	buf bytes.Buffer
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - 16<<10; over > 0 {
		t.buf.Next(over)
	}
	return t.w.Write(p)
}

// failure is err with the command that failed and the last lines of its output.
func (t *tailWriter) failure(cmd string, err error) error {
	lines := strings.Split(strings.TrimSpace(t.buf.String()), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if out == "" {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	return fmt.Errorf("%s: %w; the end of its output:\n%s", cmd, err, out)
}

// lookupHost is net.DefaultResolver.LookupHost, replaced in tests.
var lookupHost = func(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// HostNetworkNote says whether the machine AgentBox runs on (the VM or WSL
// distro, not the build's container) resolves the Debian archive, so a failed
// download can be told apart: "the host has network, the container doesn't"
// points at the container's route, such as Docker's firewall.
func HostNetworkNote(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := lookupHost(ctx, "deb.debian.org"); err != nil {
		return "This machine can't resolve deb.debian.org either, so the problem is its own network (a VPN/proxy, or WSL's networkingMode=mirrored in .wslconfig)."
	}
	return "This machine does resolve deb.debian.org, so only the build's container is cut off (Docker's firewall: DOCKER-USER/FORWARD DROP)."
}
