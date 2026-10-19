package bridgecheck

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// good is what a namespace on a bridge that works finds.
var good = Report{DNS: true, Connect4: true, Download4: true, Payload: fullPayload}

func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		report  func(r *Report)
		changes []Change
		says    string // part of what is said; "" for nothing
	}{
		{name: "everything works"},
		{name: "IPv6 works too", report: func(r *Report) { r.V6, r.Download6 = true, true }},
		{
			name:    "IPv6 doesn't, IPv4 does: IPv6 off",
			cfg:     Config{"ipv6.address": "fd42:1::1/64"},
			report:  func(r *Report) { r.V6 = true },
			changes: []Change{{"ipv6.address", "none"}},
			says:    "turning the bridge's IPv6 off",
		},
		{
			name:   "neither IPv6 nor IPv4 downloads: IPv6 isn't to blame",
			report: func(r *Report) { r.V6, r.Download4, r.Payload = true, false, 0 },
			says:   "can't download over IPv6 either",
		},
		{
			name:    "full-size packets don't get out: bridge.mtu",
			report:  func(r *Report) { r.Download4, r.Payload = false, 1372 },
			changes: []Change{{"bridge.mtu", "1400"}},
			says:    "1400-byte packets at most",
		},
		{
			name:    "an MTU below IPv6's minimum is raised to it",
			report:  func(r *Report) { r.Download4, r.Payload = false, 1000 },
			changes: []Change{{"bridge.mtu", "1280"}},
		},
		{
			name:    "bridge.mtu AgentBox set before is set again",
			cfg:     Config{"bridge.mtu": "1450", Owned: "bridge.mtu"},
			report:  func(r *Report) { r.Download4, r.Payload = false, 1372 },
			changes: []Change{{"bridge.mtu", "1400"}},
		},
		{
			name:   "still stalling at the MTU AgentBox set",
			cfg:    Config{"bridge.mtu": "1400", Owned: "bridge.mtu"},
			report: func(r *Report) { r.Download4, r.Payload = false, 1372 },
			says:   "with bridge.mtu at 1400 already",
		},
		{
			name:   "the user's own bridge.mtu is left alone",
			cfg:    Config{"bridge.mtu": "1450"},
			report: func(r *Report) { r.Download4, r.Payload = false, 1372 },
			says:   "bridge.mtu is your own (1450), so AgentBox left it alone",
		},
		{
			name:   "downloads stall, but full-size pings get out",
			report: func(r *Report) { r.Download4 = false },
			says:   "not the MTU",
		},
		{
			name:   "downloads stall, and pings don't get out at all",
			report: func(r *Report) { r.Download4, r.Payload = false, 0 },
			says:   "not the MTU",
		},
		{
			name:   "no TCP out",
			report: func(r *Report) { r.Connect4, r.Download4 = false, false },
			says:   "can't open IPv4 connections out",
		},
		{
			name:   "no DNS: nothing about the MTU",
			report: func(r *Report) { r.DNS, r.Download4, r.Payload = false, false, 1372 },
			says:   "can't resolve names",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := good
			if tc.report != nil {
				tc.report(&r)
			}
			cfg := tc.cfg
			if cfg == nil {
				cfg = Config{}
			}
			changes, said := Decide(cfg, r)
			if !slices.Equal(changes, tc.changes) {
				t.Errorf("changes = %v, want %v", changes, tc.changes)
			}
			all := strings.Join(said, " ")
			if tc.says == "" && len(tc.changes) == 0 && all != "" {
				t.Errorf("said %q, want nothing", all)
			}
			if !strings.Contains(all, tc.says) {
				t.Errorf("said %q, want it to say %q", all, tc.says)
			}
		})
	}
}

// probes is a fake Env.Probe answering each call with the next report, and
// keeping the specs it was given.
type probes struct {
	reports []Report
	specs   []Spec
	err     error
}

func (p *probes) probe(_ context.Context, spec Spec) (Report, error) {
	p.specs = append(p.specs, spec)
	if p.err != nil {
		return Report{}, p.err
	}
	r := p.reports[0]
	if len(p.reports) > 1 {
		p.reports = p.reports[1:]
	}
	return r, nil
}

// working is a machine whose bridge's DNS answers.
func working(p *probes) (*world, Env) {
	w := wsl()
	w.bridge = func(string) bool { return true }
	w.net.leases = []string{"10.80.39.254", "10.80.39.10"}
	env := w.env()
	env.Probe = p.probe
	return w, env
}

func TestCheckEverythingWorks(t *testing.T) {
	p := &probes{reports: []Report{good}}
	w, env := working(p)
	res, err := Check(context.Background(), env)
	if err != nil || res.Changed || res.Message != "" {
		t.Fatalf("Check = %+v, %v; want nothing done or said", res, err)
	}
	if len(p.specs) != 1 {
		t.Fatalf("probed %d times, want once", len(p.specs))
	}
	want := Spec{Addr4: "10.80.39.253/24", Gateway: "10.80.39.1", MTU: 1500}
	if p.specs[0] != want {
		t.Errorf("spec = %+v, want %+v", p.specs[0], want)
	}
	if len(w.net.config) != 1 {
		t.Errorf("the bridge's configuration changed: %v", w.net.config)
	}
}

func TestCheckFixesTheMTU(t *testing.T) {
	stalls := good
	stalls.Download4, stalls.Payload = false, 1372
	p := &probes{reports: []Report{stalls, good}}
	w, env := working(p)
	res, err := Check(context.Background(), env)
	if err != nil || !res.Changed {
		t.Fatalf("Check = %+v, %v", res, err)
	}
	if w.net.config["bridge.mtu"] != "1400" || w.net.config[Owned] != "bridge.mtu" {
		t.Errorf("the bridge has %v", w.net.config)
	}
	for _, s := range []string{"1400-byte packets", "Set incusbr0's bridge.mtu to 1400 (it was unset); `incus network unset incusbr0 bridge.mtu` puts it back.", "Checked again: containers on incusbr0 now resolve, connect and download."} {
		if !strings.Contains(res.Message, s) {
			t.Errorf("message %q doesn't say %q", res.Message, s)
		}
	}
	if len(p.specs) != 2 || p.specs[1].MTU != 1400 {
		t.Errorf("checked again with %+v, want the new MTU", p.specs)
	}
}

func TestCheckTurnsOffIPv6(t *testing.T) {
	broken := good
	broken.V6 = true
	p := &probes{reports: []Report{broken}}
	w, env := working(p)
	w.net.config["ipv6.address"] = "fd42:1:2:3::1/64"
	res, err := Check(context.Background(), env)
	if err != nil || !res.Changed {
		t.Fatalf("Check = %+v, %v", res, err)
	}
	spec := p.specs[0]
	a6, err := netip.ParsePrefix(spec.Addr6)
	if err != nil || !netip.MustParsePrefix("fd42:1:2:3::/64").Contains(a6.Addr()) || a6.Bits() != 64 || spec.Gateway6 != "fd42:1:2:3::1" {
		t.Errorf("the namespace's IPv6 = %q via %q", spec.Addr6, spec.Gateway6)
	}
	if w.net.config["ipv6.address"] != "none" {
		t.Errorf("ipv6.address = %q", w.net.config["ipv6.address"])
	}
	if !strings.Contains(res.Message, "`incus network set incusbr0 ipv6.address=fd42:1:2:3::1/64` puts it back") {
		t.Errorf("message %q doesn't say how to undo it", res.Message)
	}
	// The same report again, as nothing changed for the fake: said so.
	if !strings.Contains(res.Message, "Checked again, and it still isn't right") {
		t.Errorf("message %q doesn't say the second check failed", res.Message)
	}
}

func TestCheckCantProbe(t *testing.T) {
	p := &probes{err: errors.New("sudo: a password is required")}
	_, env := working(p)
	res, err := Check(context.Background(), env)
	if err != nil || res.Changed || !strings.Contains(res.Message, "Could not check incusbr0 from a network namespace: sudo: a password is required") {
		t.Fatalf("Check = %+v, %v", res, err)
	}
}

func TestCheckNoIPv4(t *testing.T) {
	p := &probes{reports: []Report{good}}
	w, env := working(p)
	w.net.config["ipv4.address"] = "none"
	if res, err := Check(context.Background(), env); err != nil || res.Message != "" || len(p.specs) != 0 {
		t.Fatalf("Check of a bridge with no IPv4 = %+v, %v, probed %d times", res, err, len(p.specs))
	}
}

func TestFreeAddr(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		leases []string
		want   string
	}{
		{"10.80.39.1/24", nil, "10.80.39.254"},
		{"10.80.39.1/24", []string{"10.80.39.254", "10.80.39.253"}, "10.80.39.252"},
		{"10.80.39.254/24", nil, "10.80.39.253"}, // the gateway at the top
		{"10.0.0.1/30", nil, "10.0.0.2"},
		{"10.0.0.1/30", []string{"10.0.0.2"}, ""},
	} {
		got, ok := freeAddr(netip.MustParsePrefix(tc.prefix), tc.leases)
		if s := got.String(); ok && s != tc.want || !ok && tc.want != "" {
			t.Errorf("freeAddr(%s, %v) = %v, %v; want %q", tc.prefix, tc.leases, got, ok, tc.want)
		}
	}
}
