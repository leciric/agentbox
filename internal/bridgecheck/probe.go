// The namespace the bridge is checked from.
//
// A veth pair puts a network namespace on the bridge with an address in its
// subnet, the way a container is, without a container to boot: what reaches
// the internet from it reaches it from an agent. Making it takes root, so the
// daemon runs `sudo -n agentbox host probe-bridge` (RunProbe), which runs
// itself again inside the namespace (Inside) and passes its Report back.

package bridgecheck

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Spec is the namespace to make, and what to ask from it.
type Spec struct {
	Addr4    string `json:"addr4"` // its address and prefix, as 10.80.39.253/24
	Gateway  string `json:"gateway"`
	Addr6    string `json:"addr6,omitempty"` // "" when the bridge has no IPv6
	Gateway6 string `json:"gateway6,omitempty"`
	MTU      int    `json:"mtu"` // the bridge's, which containers get by DHCP
	// Candidates are DNS servers to ask instead of the gateway, which can't
	// serve DNS (mirrored.go). The first that answers resolves the downloads.
	Candidates []string `json:"candidates,omitempty"`
	// Targets, for tests; empty is Default's.
	Connect4 string `json:"connect4,omitempty"`
	URL      string `json:"url,omitempty"`
	Ping4    string `json:"ping4,omitempty"`
}

// Defaults for Spec's targets: a connection, a download, and a ping.
const (
	DefaultConnect4 = "1.1.1.1:443"
	// The first file apt downloads, about 150 KB from a CDN with IPv4 and IPv6.
	DefaultURL   = "http://deb.debian.org/debian/dists/stable/Release"
	DefaultPing4 = "1.1.1.1"
)

// downloadSize is how much of URL a download reads: dozens of full packets.
const downloadSize = 64 << 10

// Report is what the namespace found.
type Report struct {
	DNS       bool `json:"dns"`       // the gateway, or a candidate, answered a lookup
	Connect4  bool `json:"connect4"`  // a TCP connection opened over IPv4
	Download4 bool `json:"download4"` // downloadSize came down over IPv4
	V6        bool `json:"v6"`        // the namespace had IPv6 to check
	Download6 bool `json:"download6"` // and it came down over IPv6
	// Payload is the largest ICMP echo payload, sent unfragmentable, that
	// Ping4 answered: fullPayload for a path that carries 1500 bytes, 0 when
	// it answered none.
	Payload int `json:"payload"`
	// Answering are the Spec's candidates that answered, in its order.
	Answering []string `json:"answering,omitempty"`
}

// Self is the agentbox binary, which the namespace runs.
var Self = os.Executable

// probeAsRoot is Env.Probe: RunProbe here as root, through sudo otherwise.
func probeAsRoot(ctx context.Context, spec Spec) (Report, error) {
	self, err := Self()
	if err != nil {
		return Report{}, err
	}
	if os.Geteuid() == 0 {
		return RunProbe(ctx, spec, self, runCmd)
	}
	js, _ := json.Marshal(spec)
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := runCmd(ctx, "sudo", "-n", self, "host", "probe-bridge", string(js))
	if err != nil {
		return Report{}, err
	}
	return parseReport(out)
}

// probeTimeout bounds the whole probe: a few seconds' checks, and their
// timeouts when things fail.
const probeTimeout = 45 * time.Second

// Run runs a command and returns its stdout; an error carries its stderr.
type Run func(ctx context.Context, name string, args ...string) ([]byte, error)

func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%s: %s", name, msg)
		}
	}
	return out, err
}

// RunProbe makes the namespace, runs `self host probe-bridge --inside` in it,
// and removes it. It takes root.
func RunProbe(ctx context.Context, spec Spec, self string, run Run) (Report, error) {
	if run == nil {
		run = runCmd
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	id := strconv.Itoa(os.Getpid() % 100000)
	ns, host := "agentbox-probe-"+id, "abprobe"+id
	ip := func(args ...string) error {
		_, err := run(ctx, "ip", args...)
		return err
	}
	// Left over from a probe that was killed: same pid, same names.
	_ = ip("netns", "del", ns)
	if err := ip("netns", "add", ns); err != nil {
		return Report{}, err
	}
	defer func() { _ = ip("netns", "del", ns) }() // takes the veth pair with it
	mtu := strconv.Itoa(spec.MTU)
	steps := [][]string{
		{"link", "add", host, "mtu", mtu, "type", "veth", "peer", "name", "eth0", "mtu", mtu, "netns", ns},
		{"link", "set", host, "master", Bridge, "up"},
		{"-n", ns, "link", "set", "lo", "up"},
		{"-n", ns, "link", "set", "eth0", "up"},
		{"-n", ns, "addr", "add", spec.Addr4, "dev", "eth0"},
		{"-n", ns, "route", "add", "default", "via", spec.Gateway},
	}
	if spec.Addr6 != "" {
		steps = append(steps,
			[]string{"-n", ns, "addr", "add", spec.Addr6, "dev", "eth0", "nodad"},
			[]string{"-n", ns, "-6", "route", "add", "default", "via", spec.Gateway6})
	}
	for _, s := range steps {
		if err := ip(s...); err != nil {
			_ = ip("link", "del", host)
			return Report{}, err
		}
	}
	js, _ := json.Marshal(spec)
	out, err := run(ctx, "ip", "netns", "exec", ns, self, "host", "probe-bridge", "--inside", string(js))
	if err != nil {
		return Report{}, err
	}
	return parseReport(out)
}

func parseReport(out []byte) (Report, error) {
	var r Report
	if err := json.Unmarshal(out, &r); err != nil {
		return Report{}, fmt.Errorf("the probe's report: %w", err)
	}
	return r, nil
}

// Inside runs the checks from the namespace it is in, with the bridge's
// gateway as its resolver, as a container has.
func Inside(ctx context.Context, spec Spec) Report {
	connect4, url, ping4 := or(spec.Connect4, DefaultConnect4), or(spec.URL, DefaultURL), or(spec.Ping4, DefaultPing4)
	r := Report{V6: spec.Addr6 != ""}
	resolver := spec.Gateway
	if len(spec.Candidates) > 0 {
		r.Answering = answering(ctx, spec.Candidates)
		resolver = ""
		if len(r.Answering) > 0 {
			resolver = r.Answering[0]
		}
	}
	r.DNS = resolver != "" && answers(ctx, resolver)
	r.Connect4 = connects(ctx, "tcp4", connect4)
	if r.DNS && r.Connect4 {
		r.Download4 = downloads(ctx, "tcp4", resolver, url)
	}
	if r.V6 && r.DNS {
		r.Download6 = downloads(ctx, "tcp6", resolver, url)
	}
	r.Payload = largestPayload(ctx, ping4)
	return r
}

// answering are the servers that answer a lookup, asked all at once, in
// their order.
func answering(ctx context.Context, servers []string) []string {
	ok := make([]bool, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Go(func() { ok[i] = answers(ctx, s) })
	}
	wg.Wait()
	var out []string
	for i, s := range servers {
		if ok[i] {
			out = append(out, s)
		}
	}
	return out
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// connects reports whether a TCP connection to addr opens.
func connects(ctx context.Context, network, addr string) bool {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// downloads reports whether downloadSize of url, or all of it, comes down
// over network, its name resolved by the DNS server at resolver.
func downloads(ctx context.Context, network, resolver, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, n, net.JoinHostPort(resolver, dnsPort))
	}}
	d := &net.Dialer{Resolver: r}
	client := &http.Client{Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, downloadSize))
	return resp.StatusCode < 400 && (n == downloadSize || err == nil && n > 0)
}

// payloads are the sizes largestPayload tries when the path doesn't say its
// MTU: 1500 bytes, then what VPNs, PPPoE and tunnels commonly leave.
var payloads = []int{fullPayload, 1452, 1422, 1372, 1332, 1252}

// largestPayload is the largest echo payload host answers when sent with
// Don't Fragment, through a raw socket (root, which the namespace has). A
// router that can't pass one says its next hop's MTU, and that is taken at
// its word. 0 when host answers none, or raw sockets can't be had.
func largestPayload(ctx context.Context, host string) int {
	dst, err := netip.ParseAddr(host)
	if err != nil {
		return 0
	}
	conn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return 0
	}
	defer func() { _ = conn.Close() }()
	if err := dontFragment(conn); err != nil {
		return 0
	}
	try := func(size, seq int) (ok bool, mtu int) {
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: seq, Data: make([]byte, size)}}
		b, _ := msg.Marshal(nil)
		if _, err := conn.WriteTo(b, &net.IPAddr{IP: dst.AsSlice()}); err != nil {
			return false, 0 // EMSGSIZE: larger than the namespace's own MTU
		}
		deadline := time.Now().Add(time.Second)
		buf := make([]byte, 2048)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			_ = conn.SetReadDeadline(deadline)
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return false, 0
			}
			m, err := icmp.ParseMessage(1, buf[:n])
			if err != nil {
				continue
			}
			switch body := m.Body.(type) {
			case *icmp.Echo:
				if m.Type == ipv4.ICMPTypeEchoReply && body.Seq == seq {
					return true, 0
				}
			case *icmp.DstUnreach:
				if m.Type == ipv4.ICMPTypeDestinationUnreachable && m.Code == 4 && n >= 8 {
					return false, int(binary.BigEndian.Uint16(buf[6:8]))
				}
			}
		}
		return false, 0
	}
	for i, size := range payloads {
		ok, mtu := try(size, i+1)
		if ok {
			return size
		}
		if mtu >= minMTU && mtu-28 < size {
			if ok, _ := try(mtu-28, 100+i); ok {
				return mtu - 28
			}
		}
	}
	return 0
}

// dontFragment sets Don't Fragment on what conn sends, whatever the path MTU
// this machine has learnt (IP_PMTUDISC_PROBE).
func dontFragment(conn net.PacketConn) error {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return errors.New("not a socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) { serr = setDontFragment(fd) }); err != nil {
		return err
	}
	return serr
}
