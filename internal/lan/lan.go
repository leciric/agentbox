// Package lan is what the daemon (internal/daemon/lan.go) and the supervisor
// of AgentBox's VM (internal/hostvm/chv) share about serving phones on the
// local network: which of this machine's addresses a phone could reach it at,
// and the QR code that carries the pairing URL to the phone's camera.
package lan

import (
	"net"
	"net/netip"
	"slices"
	"strings"

	"rsc.io/qr"
)

// virtualPrefixes are interface names of networks that only exist on this
// machine: containers', VMs' and bridges' of their own, which no phone is on.
var virtualPrefixes = []string{"lo", "docker", "br-", "veth", "incusbr", "lxdbr", "virbr", "vnet", "tap", "cni", "flannel", "podman", "vmnet", "vboxnet", "cali", "kube"}

// Addresses are the IPv4 addresses of this machine that a phone on its local
// network could reach it at, the likeliest first: the private ranges home and
// office networks use (192.168/16 first, then 10/8, then 172.16/12), then any
// other. Loopback, link-local and the machine's own virtual networks are left
// out, as is IPv6, whose addresses no one reads off a QR code's URL.
func Addresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []netip.Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || virtual(iface.Name) {
			continue
		}
		list, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range list {
			prefix, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			if ip.Is4() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				addrs = append(addrs, ip)
			}
		}
	}
	return Rank(addrs)
}

// Rank orders addresses the way Addresses does, and drops duplicates.
func Rank(addrs []netip.Addr) []string {
	slices.SortStableFunc(addrs, func(a, b netip.Addr) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return a.Compare(b)
	})
	addrs = slices.Compact(addrs)
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}
	return out
}

func rank(a netip.Addr) int {
	switch {
	case netip.MustParsePrefix("192.168.0.0/16").Contains(a):
		return 0
	case netip.MustParsePrefix("10.0.0.0/8").Contains(a):
		return 1
	case netip.MustParsePrefix("172.16.0.0/12").Contains(a):
		return 2
	}
	return 3
}

func virtual(name string) bool {
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// QR is text as a QR code, one string a row, '1' for a dark module and '0'
// for a light one, without the quiet zone around it: whoever draws it adds
// that.
func QR(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	rows := make([]string, code.Size)
	var b strings.Builder
	for y := range code.Size {
		b.Reset()
		for x := range code.Size {
			if code.Black(x, y) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		rows[y] = b.String()
	}
	return rows, nil
}

// Terminal draws a QR code from QR for a terminal, two rows to a line with
// half blocks, with the quiet zone QR leaves out. The light modules are the
// ones drawn, in the terminal's foreground colour, so the code is right on a
// dark terminal; on a light one it comes out inverted, which phones' cameras
// read as well.
func Terminal(rows []string) string {
	const quiet = 2
	n := len(rows)
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return y >= 0 && y < n && x >= 0 && x < n && rows[y][x] == '1'
	}
	var b strings.Builder
	for y := 0; y < n+2*quiet; y += 2 {
		for x := range n + 2*quiet {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString(" ")
			case top:
				b.WriteString("▄")
			case bottom:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
