// Package bridgecheck makes sure containers on the Incus bridge can reach the
// internet the way an image build and an agent need: names, IPv4 and IPv6
// connections, and full-size packets.
//
// A machine can resolve and download fine while its bridge can't, WSL above
// all: with mirrored networking nothing can listen on the bridge's address,
// so its dnsmasq serves DHCP but never DNS (mirrored.go); elsewhere dnsmasq
// may not be able to use the machine's resolver (dns.go); the machine may have
// no IPv6 out while Incus gives containers IPv6 (apt then tries the mirror's
// AAAA first and waits); and the path out may carry less than 1500 bytes
// (connections open, downloads stall). Ping works throughout.
//
// Check finds each from a throwaway network namespace on the bridge, which
// sees what a container does without one having to boot (probe.go), and fixes
// what it can: the DNS servers containers are given or the gateway forwards
// to, ipv6.address and bridge.mtu. It logs every change, never overrides a
// value the user set, takes a second or two when everything works, and
// changes nothing then. host setup (which `agentbox wsl init` runs), the
// daemon's start (which `wsl start` does) and an image build call it.
package bridgecheck

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Bridge is AgentBox's Incus network.
const Bridge = "incusbr0"

// Owned is the network key listing the keys Check set, comma-separated: one
// of those it may set again; any other that is set is the user's.
const Owned = "user.agentbox.bridge-check"

// fullPayload is the ICMP payload of a 1500-byte packet: 1500 less the IPv4
// and ICMP headers.
const fullPayload = 1472

// minMTU is the least bridge.mtu Check sets: IPv6's minimum.
const minMTU = 1280

// Network reads and sets a network's configuration: incus.Client.
type Network interface {
	NetworkConfig(ctx context.Context, name, key string) (string, error)
	SetNetworkConfig(ctx context.Context, name, key, value string) error
	NetworkLeases(ctx context.Context, name string) ([]string, error)
}

// Env is what the checks ask and change; tests fake every part.
type Env struct {
	Net Network
	// Answers reports whether a DNS server answers a lookup of Probe, with an
	// address or with "no such name". "" is this machine's own resolver.
	Answers func(ctx context.Context, server string) bool
	// Listens reports whether a TCP connection to addr opens.
	Listens func(ctx context.Context, addr string) bool
	// BindIgnored reports whether binding to ip succeeds without binding, as
	// mirrored WSL does (bindIgnored).
	BindIgnored func(ip string) bool
	// Local reports whether addr is one of this machine's own.
	Local func(addr string) bool
	// ReadFile reads ResolvedConf.
	ReadFile func(name string) ([]byte, error)
	// Settle is how long the bridge gets to answer after raw.dnsmasq
	// changes, while Incus restarts its dnsmasq.
	Settle time.Duration
	// Probe checks the bridge from a namespace on it (RunProbe); nil skips
	// it, leaving the DNS checks.
	Probe func(ctx context.Context, spec Spec) (Report, error)
}

// System is the Env of this machine's Incus.
func System(n Network) Env {
	return Env{
		Net: n, Answers: answers, Listens: listens, BindIgnored: bindIgnored, Local: isLocal,
		ReadFile: os.ReadFile, Settle: 10 * time.Second, Probe: probeAsRoot,
	}
}

// Result is what a check found and did.
type Result struct {
	// Changed is true when it changed the bridge.
	Changed bool
	// Message says what it found and did, a line each, for a log; "" when
	// everything works.
	Message string
}

// Check checks the bridge's DNS, then everything else from a namespace on it,
// and fixes what it can. After a fix it checks again and says how that went.
func Check(ctx context.Context, env Env) (Result, error) {
	cfg, err := readConfig(ctx, env.Net)
	if err != nil {
		return Result{}, err
	}
	gw, ok := gateway(cfg)
	if !ok {
		return Result{}, nil // no IPv4: nothing of AgentBox's making to check
	}
	var lines []string
	changed := false
	say := func() Result { return Result{Changed: changed, Message: strings.Join(lines, "\n")} }
	mirrored := env.BindIgnored(gw)
	if mirrored {
		lines = append(lines, fmt.Sprintf("This machine ignores binds to %s's address, %s (WSL's mirrored networking does), so its dnsmasq can't serve DNS there, though DHCP works.", Bridge, gw))
	} else {
		res, err := EnsureDNS(ctx, env)
		if err != nil {
			return res, err
		}
		changed = res.Changed
		if res.Message != "" {
			lines = append(lines, res.Message)
		}
	}
	report, probed, err := probe(ctx, env, cfg, mirrored)
	if err != nil {
		lines = append(lines, fmt.Sprintf("Could not check %s from a network namespace: %v", Bridge, err))
	}
	changes, said := decideAll(ctx, env, cfg, mirrored, report, probed)
	lines = append(lines, said...)
	if len(changes) == 0 {
		return say(), nil
	}
	set, err := apply(ctx, env, cfg, changes)
	changed = changed || len(set) > 0
	lines = append(lines, set...)
	if err != nil {
		return say(), err
	}
	if probed {
		lines = append(lines, recheck(ctx, env, mirrored))
	}
	return say(), nil
}

// gateway is the bridge's IPv4 address.
func gateway(cfg Config) (string, bool) {
	p, err := netip.ParsePrefix(cfg["ipv4.address"])
	if err != nil || !p.Addr().Is4() {
		return "", false
	}
	return p.Addr().String(), true
}

// probe checks the bridge from a namespace. With mirrored networking the
// namespace asks the DNS servers containers could be given — the user's own
// dns.nameservers if set — instead of the gateway. probed is false when there
// was nothing to probe from, or it failed (err).
func probe(ctx context.Context, env Env, cfg Config, mirrored bool) (Report, bool, error) {
	if env.Probe == nil {
		return Report{}, false, nil
	}
	spec, ok, err := newSpec(ctx, env.Net, cfg)
	if err != nil || !ok {
		return Report{}, false, err
	}
	if mirrored {
		spec.Candidates = dnsCandidates(env)
		if cfg.usersOwn(Nameservers) {
			spec.Candidates = strings.Split(cfg[Nameservers], ",")
		}
	}
	report, err := env.Probe(ctx, spec)
	if err != nil {
		return Report{}, false, err
	}
	return report, true, nil
}

// decideAll is Decide, and with mirrored networking the DNS servers
// containers are to be given.
func decideAll(ctx context.Context, env Env, cfg Config, mirrored bool, report Report, probed bool) ([]Change, []string) {
	var changes []Change
	var said []string
	if mirrored {
		answering := report.Answering
		if !probed {
			answering = answeringHere(ctx, env, dnsCandidates(env))
		}
		changes, said = DecideNameservers(cfg, env, answering)
	}
	if probed {
		c, s := Decide(cfg, report)
		changes, said = append(changes, c...), append(said, s...)
	}
	return changes, said
}

// DecideNameservers works out, with mirrored networking, which DNS servers
// containers are to be given, from those that answered from the bridge.
func DecideNameservers(cfg Config, env Env, answering []string) ([]Change, []string) {
	if cfg.usersOwn(Nameservers) {
		return nil, nil // the user's already
	}
	servers, what := chooseServers(env, answering)
	want := strings.Join(servers, ",")
	switch want {
	case "":
		return nil, []string{"No DNS server answers from " + Bridge + ", neither this machine's nor public ones: containers have no DNS."}
	case cfg[Nameservers]:
		return nil, nil
	}
	return []Change{{Nameservers, want}}, []string{fmt.Sprintf(
		"Containers on %s are given DNS servers to ask directly, by DHCP (option 6): %s, %s. They lose the gateway's .incus names for each other. Running agents pick them up when they restart.",
		Bridge, want, what)}
}

// apply sets the changes, and records them as AgentBox's. A dns.nameservers
// an older Incus doesn't know is given as dnsmasq's own option instead, in a
// raw.dnsmasq that isn't the user's. It returns a line for each change.
func apply(ctx context.Context, env Env, cfg Config, changes []Change) ([]string, error) {
	owned := cfg.owned()
	var lines []string
	for _, c := range changes {
		err := env.Net.SetNetworkConfig(ctx, Bridge, c.Key, c.Value)
		if err != nil && c.Key == Nameservers && (cfg[Key] == "" || strings.HasPrefix(cfg[Key], Marker)) {
			raw := Marker + " DNS servers for containers, as dnsmasq can't listen on the gateway\ndhcp-option=6," + c.Value
			if err = env.Net.SetNetworkConfig(ctx, Bridge, Key, raw); err == nil {
				lines = append(lines, fmt.Sprintf("Set %s's %s to give containers %s by DHCP, as this Incus has no %s; `%s` puts it back.", Bridge, Key, c.Value, Nameservers, undo(Key, cfg[Key])))
				continue
			}
		}
		if err != nil {
			return lines, err
		}
		lines = append(lines, fmt.Sprintf("Set %s's %s to %s (it was %s); `%s` puts it back.", Bridge, c.Key, c.Value, orUnset(cfg[c.Key]), undo(c.Key, cfg[c.Key])))
		if !slices.Contains(owned, c.Key) {
			owned = append(owned, c.Key)
		}
	}
	return lines, env.Net.SetNetworkConfig(ctx, Bridge, Owned, strings.Join(owned, ","))
}

// recheck probes the bridge again after a change, and says how it went.
func recheck(ctx context.Context, env Env, mirrored bool) string {
	cfg, err := readConfig(ctx, env.Net)
	if err != nil {
		return "Could not check it again: " + err.Error()
	}
	report, probed, err := probe(ctx, env, cfg, mirrored)
	if err != nil || !probed {
		return fmt.Sprintf("Could not check it again: %v", err)
	}
	changes, said := decideAll(ctx, env, cfg, mirrored, report, true)
	if len(changes) == 0 && len(said) == 0 {
		return "Checked again: containers on " + Bridge + " now resolve, connect and download."
	}
	return "Checked again, and it still isn't right: " + strings.Join(said, " ")
}

// undo is the command that puts key back to was.
func undo(key, was string) string {
	if was == "" {
		return "incus network unset " + Bridge + " " + key
	}
	return "incus network set " + Bridge + " " + key + "=" + was
}

func orUnset(v string) string {
	if v == "" {
		return "unset"
	}
	return v
}

// Config is the bridge's configuration that Check reads.
type Config map[string]string

// configKeys are the keys readConfig reads.
var configKeys = []string{"ipv4.address", "ipv6.address", "bridge.mtu", Nameservers, Key, Owned}

func readConfig(ctx context.Context, n Network) (Config, error) {
	cfg := Config{}
	for _, k := range configKeys {
		v, err := n.NetworkConfig(ctx, Bridge, k)
		if err != nil {
			return nil, err
		}
		cfg[k] = v
	}
	return cfg, nil
}

// owned are the keys Check set before.
func (c Config) owned() []string {
	if c[Owned] == "" {
		return nil
	}
	return strings.Split(c[Owned], ",")
}

// usersOwn reports whether key holds a value Check didn't set.
func (c Config) usersOwn(key string) bool {
	return c[key] != "" && !slices.Contains(c.owned(), key)
}

// Change is one key Check sets on the bridge.
type Change struct{ Key, Value string }

// Decide works out, from what the namespace found, what to change on the
// bridge and what to say. Nothing to change and nothing to say is a bridge
// that works.
func Decide(cfg Config, r Report) (changes []Change, said []string) {
	if !r.DNS {
		said = append(said, "Containers on "+Bridge+" can't resolve names.")
	}
	if !r.Connect4 {
		said = append(said, "Containers on "+Bridge+" can't open IPv4 connections out (ping may still work): a firewall on this machine, or Windows', is likely blocking forwarded TCP.")
	}
	if r.V6 && !r.Download6 {
		if r.Download4 {
			// The usual way: apt tries the mirror's AAAA first, and waits.
			changes = append(changes, Change{"ipv6.address", "none"})
			said = append(said, "Containers on "+Bridge+" get IPv6 but can't download over it, while IPv4 works: turning the bridge's IPv6 off, so they use IPv4.")
		} else {
			said = append(said, "Containers on "+Bridge+" can't download over IPv6 either.")
		}
	}
	if r.DNS && r.Connect4 && !r.Download4 {
		mtu := r.Payload + 28
		switch {
		case r.Payload <= 0 || r.Payload >= fullPayload:
			said = append(said, "Containers on "+Bridge+" open connections out but downloads stall, and a full-size ping isn't what fails: not the MTU, as far as can be told.")
		case cfg.usersOwn("bridge.mtu"):
			said = append(said, fmt.Sprintf("Containers on %s open connections out but downloads stall: the path out carries %d bytes at most, but bridge.mtu is your own (%s), so AgentBox left it alone.", Bridge, mtu, cfg["bridge.mtu"]))
		default:
			mtu = max(mtu, minMTU)
			if strconv.Itoa(mtu) == cfg["bridge.mtu"] {
				said = append(said, fmt.Sprintf("Containers on %s open connections out but downloads stall, with bridge.mtu at %d already.", Bridge, mtu))
				break
			}
			changes = append(changes, Change{"bridge.mtu", strconv.Itoa(mtu)})
			said = append(said, fmt.Sprintf("Containers on %s open connections out but downloads stall: the path out carries %d-byte packets at most. Running agents get the new MTU when they restart.", Bridge, mtu))
		}
	}
	return changes, said
}

// newSpec is the namespace for this bridge: an IPv4 address in its subnet that
// no lease holds, from the top down, and one in its IPv6 subnet, if it has
// IPv6, with a random interface identifier. ok is false for a bridge with no
// IPv4, which has nothing to check.
func newSpec(ctx context.Context, n Network, cfg Config) (Spec, bool, error) {
	p4, err := netip.ParsePrefix(cfg["ipv4.address"])
	if err != nil || !p4.Addr().Is4() || p4.Bits() > 30 {
		return Spec{}, false, nil
	}
	leases, err := n.NetworkLeases(ctx, Bridge)
	if err != nil {
		return Spec{}, false, err
	}
	addr, ok := freeAddr(p4, leases)
	if !ok {
		return Spec{}, false, nil
	}
	spec := Spec{
		Addr4:   netip.PrefixFrom(addr, p4.Bits()).String(),
		Gateway: p4.Addr().String(),
		MTU:     1500,
	}
	if m, err := strconv.Atoi(cfg["bridge.mtu"]); err == nil && m > 0 {
		spec.MTU = m
	}
	if p6, err := netip.ParsePrefix(cfg["ipv6.address"]); err == nil && p6.Addr().Is6() && p6.Bits() <= 64 {
		b := p6.Masked().Addr().As16()
		binary.BigEndian.PutUint64(b[8:], rand.Uint64()|1)
		spec.Addr6 = netip.PrefixFrom(netip.AddrFrom16(b), p6.Bits()).String()
		spec.Gateway6 = p6.Addr().String()
	}
	return spec, true, nil
}

// freeAddr is the highest address of p, short of its broadcast, that is
// neither its gateway nor leased.
func freeAddr(p netip.Prefix, leases []string) (netip.Addr, bool) {
	b := p.Masked().Addr().As4()
	top := binary.BigEndian.Uint32(b[:]) | (1<<(32-p.Bits()) - 1)
	for i := uint32(1); i < 1<<(32-p.Bits())-1 && i < 256; i++ {
		var a [4]byte
		binary.BigEndian.PutUint32(a[:], top-i)
		addr := netip.AddrFrom4(a)
		if addr == p.Addr() || slices.Contains(leases, addr.String()) || !p.Contains(addr) {
			continue
		}
		return addr, true
	}
	return netip.Addr{}, false
}
