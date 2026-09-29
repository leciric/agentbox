package lan

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestRank(t *testing.T) {
	var in []netip.Addr
	for _, s := range []string{"100.64.1.2", "172.20.0.5", "10.1.2.3", "192.168.1.40", "10.1.2.3", "192.168.0.9"} {
		in = append(in, netip.MustParseAddr(s))
	}
	want := []string{"192.168.0.9", "192.168.1.40", "10.1.2.3", "172.20.0.5", "100.64.1.2"}
	if got := Rank(in); !slices.Equal(got, want) {
		t.Errorf("Rank = %v, want %v", got, want)
	}
}

func TestVirtual(t *testing.T) {
	for name, want := range map[string]bool{"eth0": false, "wlp3s0": false, "enp5s0": false, "docker0": true, "incusbr0": true, "veth12ab": true, "br-1234": true, "lo": true} {
		if got := virtual(name); got != want {
			t.Errorf("virtual(%q) = %t", name, got)
		}
	}
}

func TestQR(t *testing.T) {
	rows, err := QR("http://192.168.1.40:7780/#pair=abcdefghijklmnopqrstuvwxyz012345")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 21 || (len(rows)-17)%4 != 0 {
		t.Fatalf("%d rows isn't a QR code's size", len(rows))
	}
	// The finder pattern: a dark 7-module ring in the top left corner.
	if rows[0][:7] != "1111111" || rows[1][:7] != "1000001" || rows[3][:7] != "1011101" {
		t.Errorf("no finder pattern: %q %q %q", rows[0][:7], rows[1][:7], rows[3][:7])
	}
	term := Terminal(rows)
	lines := strings.Split(strings.TrimSuffix(term, "\n"), "\n")
	if len(lines) != (len(rows)+4+1)/2 || len([]rune(lines[0])) != len(rows)+4 {
		t.Errorf("Terminal drew %d lines of %d", len(lines), len([]rune(lines[0])))
	}
}
