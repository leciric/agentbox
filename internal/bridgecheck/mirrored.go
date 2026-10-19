// A machine where nothing can listen on the bridge's address.
//
// WSL's mirrored networking intercepts bind() and, for an address that isn't
// loopback, returns success without binding: getsockname() then says
// 0.0.0.0:0 and nothing arrives. Incus starts dnsmasq with --bind-interfaces
// and --listen-address=<gateway>, so its DNS socket never exists, and nothing
// logs an error; DHCP still works, bound to the device. Windows' port space is
// shared in that mode too, so whatever holds port 53 on Windows (a DNS proxy
// such as Acrylic) would keep dnsmasq off it anyway. No upstream setting
// helps: containers must be given DNS servers to ask themselves, through DHCP
// option 6 (Incus's dns.nameservers). They then lose the gateway's .incus
// names for each other, which AgentBox doesn't use.

package bridgecheck

import (
	"context"
	"net"
	"net/netip"
	"slices"
)

// Nameservers is the bridge key whose servers Incus's dnsmasq hands containers
// by DHCP (option 6) instead of the gateway.
const Nameservers = "dns.nameservers"

// bindIgnored reports whether binding a UDP socket to ip succeeds without
// binding to it, as mirrored WSL does. An address this machine doesn't have
// fails to bind, which is not that.
func bindIgnored(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, 0)))
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	// Go fills LocalAddr from getsockname() once bound.
	got, ok := c.LocalAddr().(*net.UDPAddr)
	return ok && (got.IP.IsUnspecified() || got.Port == 0)
}

// isLocal reports whether addr is one of this machine's own: a resolver here
// (WSL's DNS tunnel on lo, a cache) that containers can't count on reaching.
func isLocal(addr string) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == addr {
			return true
		}
	}
	return false
}

// dnsCandidates are the servers containers could be given, in order of
// preference: systemd-resolved's upstreams, which keep a company's own DNS,
// then the public ones.
func dnsCandidates(env Env) []string {
	out := nameservers(env)
	for _, s := range Public {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// chooseServers picks, from the candidates that answered from the bridge,
// systemd-resolved's when any did, else the public ones; what says which.
func chooseServers(env Env, answering []string) (servers []string, what string) {
	listed := nameservers(env)
	var own, public []string
	for _, s := range answering {
		if slices.Contains(listed, s) {
			own = append(own, s)
		} else {
			public = append(public, s)
		}
	}
	if len(own) > 0 {
		return own, "this machine's own (systemd-resolved's upstream servers)"
	}
	return public, "public ones, as none of this machine's own answers from the bridge"
}

// answeringHere are the candidates that answer from this machine, short of
// its own addresses: what the bridge's namespace would find, when it can't be
// made.
func answeringHere(ctx context.Context, env Env, candidates []string) []string {
	var out []string
	for _, s := range candidates {
		if !env.Local(s) && env.Answers(ctx, s) {
			out = append(out, s)
		}
	}
	return out
}
