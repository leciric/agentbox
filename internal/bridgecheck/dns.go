// The bridge's DNS, where dnsmasq can listen on it (see mirrored.go where it
// can't).
//
// Incus runs a dnsmasq on the bridge for its containers' DHCP and DNS, and
// starts it with no --resolv-file, --server or --no-resolv: it forwards every
// name it doesn't own to the servers in /etc/resolv.conf. When those don't
// answer it — the machine moved networks, or names a resolver only reachable
// another way — dnsmasq still listens on the gateway, but answers nothing.
//
// EnsureDNS asks the gateway directly and, only when something listens there
// but doesn't answer while this machine resolves, points the bridge's dnsmasq
// at servers that answer from here: systemd-resolved's upstreams, else public
// ones. It writes raw.dnsmasq, marked as AgentBox's, and never touches one the
// user set. With nothing listening on the gateway, other upstreams can't help,
// and it only says so.

package bridgecheck

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Key is the network key whose text Incus hands dnsmasq as a configuration file.
const Key = "raw.dnsmasq"

// Marker starts every raw.dnsmasq AgentBox writes (dnsmasq reads it as a
// comment): one without it is the user's, and is left alone.
const Marker = "# agentbox:"

// ResolvedConf is systemd-resolved's own list of the servers it forwards to,
// where /etc/resolv.conf names only its stub, 127.0.0.53.
const ResolvedConf = "/run/systemd/resolve/resolv.conf"

// Public are the servers used when systemd-resolved's don't answer.
var Public = []string{"1.1.1.1", "8.8.8.8"}

// Probe is the name looked up: the first one an image build needs.
const Probe = "deb.debian.org."

// EnsureDNS checks that the bridge's DNS answers and, when its dnsmasq listens
// but doesn't answer while this machine resolves, sets the bridge's dnsmasq upstreams to servers that do.
// Each choice is checked by asking the gateway again; when none works, the
// earlier raw.dnsmasq is put back.
func EnsureDNS(ctx context.Context, env Env) (Result, error) {
	addr, err := env.Net.NetworkConfig(ctx, Bridge, "ipv4.address")
	if err != nil {
		return Result{}, err
	}
	prefix, err := netip.ParsePrefix(addr)
	if err != nil || !prefix.Addr().Is4() {
		return Result{}, nil // "none", or no bridge of AgentBox's making
	}
	gw := prefix.Addr().String()
	if env.Answers(ctx, gw) {
		return Result{}, nil
	}
	dead := fmt.Sprintf("%s's DNS (%s) doesn't answer", Bridge, gw)
	if !env.Listens(ctx, net.JoinHostPort(gw, "53")) {
		return Result{Message: dead + ": nothing listens there, so its dnsmasq isn't serving DNS, which other upstream servers can't fix"}, nil
	}
	was, err := env.Net.NetworkConfig(ctx, Bridge, Key)
	if err != nil {
		return Result{}, err
	}
	if was != "" && !strings.HasPrefix(was, Marker) {
		return Result{Message: dead + ", and its " + Key + " is your own, so AgentBox left it alone"}, nil
	}
	if !env.Answers(ctx, "") {
		return Result{Message: dead + ", and neither does this machine's own resolver: no network yet?"}, nil
	}
	for _, c := range choices(ctx, env) {
		if err := env.Net.SetNetworkConfig(ctx, Bridge, Key, c.raw); err != nil {
			return Result{}, err
		}
		if settled(ctx, env, gw) {
			return Result{Changed: true, Message: fmt.Sprintf("%s while this machine resolves: set its %s to forward to %s", dead, Key, c.what)}, nil
		}
	}
	if err := env.Net.SetNetworkConfig(ctx, Bridge, Key, was); err != nil {
		return Result{}, err
	}
	return Result{Message: dead + ", and didn't with any upstream server that answers from this machine either: left as it was"}, nil
}

// choice is one raw.dnsmasq to try, and what it forwards to, for the log.
type choice struct{ raw, what string }

// choices are the upstreams to try, in order: systemd-resolved's when they
// answer from here — its file itself when all of them do, which dnsmasq
// rereads as the network changes — then the public servers that answer.
func choices(ctx context.Context, env Env) []choice {
	var out []choice
	listed := nameservers(env)
	var up []string
	for _, s := range listed {
		if env.Answers(ctx, s) {
			up = append(up, s)
		}
	}
	switch {
	case len(up) > 0 && len(up) == len(listed):
		out = append(out, choice{
			raw:  Marker + " systemd-resolved's upstream servers, as the bridge's DNS didn't answer through /etc/resolv.conf\nresolv-file=" + ResolvedConf,
			what: "systemd-resolved's upstream servers (" + strings.Join(up, ", ") + ", from " + ResolvedConf + ")",
		})
	case len(up) > 0:
		out = append(out, static("systemd-resolved's upstream servers that answer", up))
	}
	var public []string
	for _, s := range Public {
		if env.Answers(ctx, s) {
			public = append(public, s)
		}
	}
	if len(public) > 0 {
		out = append(out, static("public DNS servers", public))
	}
	return out
}

func static(what string, servers []string) choice {
	lines := []string{Marker + " " + what + ", as the bridge's DNS didn't answer through /etc/resolv.conf", "no-resolv"}
	for _, s := range servers {
		lines = append(lines, "server="+s)
	}
	return choice{raw: strings.Join(lines, "\n"), what: what + " (" + strings.Join(servers, ", ") + ")"}
}

// nameservers are the servers systemd-resolved forwards to, as dnsmasq could
// use them: no loopback address, which on this machine is a stub or a cache of
// its own, and no address scoped to an interface.
func nameservers(env Env) []string {
	data, err := env.ReadFile(ResolvedConf)
	if err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || f[0] != "nameserver" {
			continue
		}
		a, err := netip.ParseAddr(f[1])
		if err != nil || a.IsLoopback() || a.IsUnspecified() || a.Zone() != "" {
			continue
		}
		out = append(out, a.String())
	}
	return out
}

// settled waits up to env.Settle for the gateway to answer.
func settled(ctx context.Context, env Env, gw string) bool {
	deadline := time.Now().Add(env.Settle)
	for {
		if env.Answers(ctx, gw) {
			return true
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// dnsPort is where answers asks a server; tests run theirs elsewhere.
var dnsPort = "53"

// answers looks Probe up at server, or with this machine's resolver.
func answers(ctx context.Context, server string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r := net.DefaultResolver
	if server != "" {
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(server, dnsPort))
		}}
	}
	_, err := r.LookupNetIP(ctx, "ip4", Probe)
	var dnsErr *net.DNSError
	return err == nil || errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// listens reports whether a TCP connection to addr opens: dnsmasq listens on
// TCP as on UDP, and UDP can't tell a server that is silent from no server.
func listens(ctx context.Context, addr string) bool {
	return connects(ctx, "tcp4", addr)
}
