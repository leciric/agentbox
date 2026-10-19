package bridgecheck

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeNet is incusbr0's configuration, and every raw.dnsmasq set on it.
type fakeNet struct {
	config map[string]string
	sets   []string
	leases []string
	reject string // a key this Incus doesn't know
}

func (f *fakeNet) NetworkConfig(_ context.Context, name, key string) (string, error) {
	if name != Bridge {
		return "", errors.New("Network not found")
	}
	return f.config[key], nil
}

func (f *fakeNet) NetworkLeases(context.Context, string) ([]string, error) {
	return f.leases, nil
}

func (f *fakeNet) SetNetworkConfig(_ context.Context, name, key, value string) error {
	if name != Bridge {
		return errors.New("unexpected set of " + name + " " + key)
	}
	if key == f.reject {
		return errors.New("Invalid option \"" + key + "\"")
	}
	f.config[key] = value
	if key == Key {
		f.sets = append(f.sets, value)
	}
	return nil
}

// world is a machine: which servers answer from it, and when its bridge does.
type world struct {
	net     *fakeNet
	answers map[string]bool // "" is the machine's resolver
	resolvd string          // ResolvedConf's text; "" is no such file
	// bridge reports whether the gateway answers with this raw.dnsmasq.
	bridge func(raw string) bool
	// deaf is a gateway nothing listens on; mirrored, one that can't be bound.
	deaf, mirrored bool
}

func (w *world) env() Env {
	return Env{
		Net: w.net,
		Answers: func(_ context.Context, server string) bool {
			if server == "10.80.39.1" {
				return w.bridge(w.net.config[Key])
			}
			return w.answers[server]
		},
		Listens:     func(context.Context, string) bool { return !w.deaf },
		BindIgnored: func(string) bool { return w.mirrored },
		Local:       func(addr string) bool { return addr == "10.255.255.254" },
		ReadFile: func(name string) ([]byte, error) {
			if name != ResolvedConf || w.resolvd == "" {
				return nil, fs.ErrNotExist
			}
			return []byte(w.resolvd), nil
		},
	}
}

// wsl is the reported machine: the bridge answers only once dnsmasq is given
// upstreams of its own, systemd-resolved's or public ones.
func wsl() *world {
	return &world{
		net:     &fakeNet{config: map[string]string{"ipv4.address": "10.80.39.1/24"}},
		answers: map[string]bool{"": true, "10.255.255.254": true, "1.1.1.1": true, "8.8.8.8": true},
		resolvd: "# This is /run/systemd/resolve/resolv.conf managed by man:systemd-resolved(8).\nnameserver 10.255.255.254\nsearch .\n",
		bridge:  func(raw string) bool { return raw != "" },
	}
}

func TestEnsureDNS(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		setup   func(w *world)
		changed bool
		raw     string // raw.dnsmasq after; prefix match
		message string // part of the message; "" for none
	}{
		{
			name:  "bridge answers",
			setup: func(w *world) { w.bridge = func(string) bool { return true } },
		},
		{
			name:  "no IPv4 on the bridge",
			setup: func(w *world) { w.net.config["ipv4.address"] = "none" },
		},
		{
			name:    "the user's own raw.dnsmasq",
			setup:   func(w *world) { w.net.config[Key] = "server=10.0.0.53"; w.bridge = func(string) bool { return false } },
			raw:     "server=10.0.0.53",
			message: "is your own, so AgentBox left it alone",
		},
		{
			name:    "nothing listens on the gateway: other upstreams can't help",
			setup:   func(w *world) { w.deaf = true },
			message: "nothing listens there",
		},
		{
			name:    "the machine doesn't resolve either",
			setup:   func(w *world) { w.answers[""] = false },
			message: "neither does this machine's own resolver",
		},
		{
			name:    "systemd-resolved's servers all answer",
			changed: true,
			raw:     Marker + " systemd-resolved's upstream servers, as the bridge's DNS didn't answer through /etc/resolv.conf\nresolv-file=" + ResolvedConf,
			message: "systemd-resolved's upstream servers (10.255.255.254, from " + ResolvedConf + ")",
		},
		{
			name: "only some of systemd-resolved's servers answer",
			setup: func(w *world) {
				w.resolvd = "nameserver 127.0.0.1\nnameserver 192.168.1.1\nnameserver 192.168.1.2\nnameserver fe80::1%eth0\n"
				w.answers["192.168.1.2"] = true
			},
			changed: true,
			raw:     Marker + " systemd-resolved's upstream servers that answer, as the bridge's DNS didn't answer through /etc/resolv.conf\nno-resolv\nserver=192.168.1.2",
		},
		{
			name:    "no systemd-resolved",
			setup:   func(w *world) { w.resolvd = "" },
			changed: true,
			raw:     Marker + " public DNS servers, as the bridge's DNS didn't answer through /etc/resolv.conf\nno-resolv\nserver=1.1.1.1\nserver=8.8.8.8",
			message: "public DNS servers (1.1.1.1, 8.8.8.8)",
		},
		{
			name: "systemd-resolved's servers answer here but not through dnsmasq",
			setup: func(w *world) {
				w.answers["8.8.8.8"] = false
				w.bridge = func(raw string) bool { return strings.Contains(raw, "server=1.1.1.1") }
			},
			changed: true,
			raw:     Marker + " public DNS servers, as the bridge's DNS didn't answer through /etc/resolv.conf\nno-resolv\nserver=1.1.1.1",
		},
		{
			name: "AgentBox's earlier choice stopped working",
			setup: func(w *world) {
				w.net.config[Key] = Marker + " public DNS servers\nno-resolv\nserver=9.9.9.9"
				w.bridge = func(raw string) bool { return strings.Contains(raw, "resolv-file") }
			},
			changed: true,
			raw:     Marker + " systemd-resolved's",
		},
		{
			name: "nothing helps: put back as it was",
			setup: func(w *world) {
				w.net.config[Key] = Marker + " old"
				w.bridge = func(string) bool { return false }
			},
			raw:     Marker + " old",
			message: "left as it was",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := wsl()
			if tc.setup != nil {
				tc.setup(w)
			}
			res, err := EnsureDNS(ctx, w.env())
			if err != nil {
				t.Fatal(err)
			}
			if res.Changed != tc.changed {
				t.Errorf("Changed = %v, want %v (%q)", res.Changed, tc.changed, res.Message)
			}
			if got := w.net.config[Key]; !strings.HasPrefix(got, tc.raw) || (tc.raw == "" && got != "") {
				t.Errorf("raw.dnsmasq = %q, want %q", got, tc.raw)
			}
			if tc.message == "" && !tc.changed && res.Message != "" {
				t.Errorf("Message = %q, want none", res.Message)
			}
			if tc.message != "" && !strings.Contains(res.Message, tc.message) {
				t.Errorf("Message = %q, want it to say %q", res.Message, tc.message)
			}
			if !tc.changed && tc.message == "" && len(w.net.sets) > 0 {
				t.Errorf("set raw.dnsmasq when it shouldn't have: %q", w.net.sets)
			}
		})
	}
}

func TestEnsureDNSLeavesTheUsersOwnUntouched(t *testing.T) {
	w := wsl()
	w.net.config[Key] = "server=10.0.0.53"
	w.bridge = func(string) bool { return false }
	if _, err := EnsureDNS(context.Background(), w.env()); err != nil {
		t.Fatal(err)
	}
	if len(w.net.sets) > 0 {
		t.Fatalf("changed the user's raw.dnsmasq: %q", w.net.sets)
	}
}

func TestEnsureDNSFails(t *testing.T) {
	w := wsl()
	w.net.config = map[string]string{"ipv4.address": "10.80.39.1/24"}
	env := w.env()
	env.Net = failing{w.net}
	if _, err := EnsureDNS(context.Background(), env); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("Ensure with a failing set = %v", err)
	}
}

type failing struct{ *fakeNet }

func (failing) SetNetworkConfig(context.Context, string, string, string) error {
	return errors.New("permission refused")
}

// serveDNS answers every question on a UDP socket of 127.0.0.1 with rcode,
// and an address when that is success; it returns the port. silent drops them.
func serveDNS(t *testing.T, rcode dnsmessage.RCode, silent bool) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var m dnsmessage.Message
			if err := m.Unpack(buf[:n]); err != nil || silent {
				continue
			}
			m.Response, m.RCode, m.RecursionAvailable = true, rcode, true
			if rcode == dnsmessage.RCodeSuccess && len(m.Questions) == 1 && m.Questions[0].Type == dnsmessage.TypeA {
				m.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: m.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
				}}
			}
			out, _ := m.Pack()
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port)
}

func TestAnswers(t *testing.T) {
	old := dnsPort
	t.Cleanup(func() { dnsPort = old })
	for _, tc := range []struct {
		name   string
		rcode  dnsmessage.RCode
		silent bool
		want   bool
	}{
		{"an address", dnsmessage.RCodeSuccess, false, true},
		{"no such name", dnsmessage.RCodeNameError, false, true},
		{"server failure, as dnsmasq gives with no upstream", dnsmessage.RCodeServerFailure, false, false},
		{"refused", dnsmessage.RCodeRefused, false, false},
		{"no answer", 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dnsPort = serveDNS(t, tc.rcode, tc.silent)
			// A server that doesn't answer is given up on sooner than the 3s answers allows.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if got := answers(ctx, "127.0.0.1"); got != tc.want {
				t.Errorf("answers = %v, want %v", got, tc.want)
			}
		})
	}
}
