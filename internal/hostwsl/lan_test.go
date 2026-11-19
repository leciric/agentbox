package hostwsl

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

// The relay opens phones' port on Windows while the daemon in the distro has
// them on, passes their requests to the daemon's /v1/lan/net/, and reports.
func TestRelayLAN(t *testing.T) {
	d := fakeDistro(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	d.Env = append(d.Env, "FAKEWSL_LAN_PORT="+strconv.Itoa(port))
	imported(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := d.EnsureBinary(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.StartDaemon(ctx); err != nil {
		t.Fatal(err)
	}
	in, inW := io.Pipe()
	defer func() { _ = inW.Close() }()
	outR, out := io.Pipe()
	go func() { _ = RunRelay(ctx, d, in, out) }()
	path, err := ReadSocketLine(outR)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	t.Cleanup(func() {
		client := &http.Client{Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) { return dialPrivate(path) },
		}}
		if resp, err := client.Post("http://agentbox/v1/shutdown", "", nil); err == nil {
			_ = resp.Body.Close()
		}
	})

	var rep api.LANHostReport
	for deadline := time.Now().Add(15 * time.Second); !rep.Listening; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no report of the port open: %+v", rep)
		}
		b, _ := os.ReadFile(filepath.Join(d.fakeEnv("FAKEWSL_ROOT"), "lan-host"))
		_ = json.Unmarshal(b, &rep)
	}
	if rep.Port != port || rep.Error != "" || rep.Addresses == nil {
		t.Errorf("report = %+v", rep)
	}
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "phone: /v1/lan/net/api/v1/agents" {
		t.Errorf("the daemon got %q", b)
	}
}

func TestFirewallWarning(t *testing.T) {
	const exe = `C:\Program Files\AgentBox\resources\bin\agentbox.exe`
	for _, c := range []struct {
		name, json, want string
	}{
		{"off", `{"networks":["Private"],"on":["Domain","Public"],"rules":[]}`, ""},
		{"allowed", `{"networks":["Private"],"on":["Domain","Private","Public"],"rules":[{"action":"Allow","profile":"Private"}]}`, ""},
		{"allowed anywhere", `{"networks":["Public","DomainAuthenticated"],"on":["Domain","Private","Public"],"rules":[{"action":"Allow","profile":"Any"}]}`, ""},
		{"no rule", `{"networks":["Private"],"on":["Domain","Private","Public"],"rules":[]}`, "may turn phones away on this private network"},
		{"allowed elsewhere", `{"networks":["Public"],"on":["Public"],"rules":[{"action":"Allow","profile":"Domain, Private"}]}`, "may turn phones away on this public network"},
		{"blocked", `{"networks":["Private"],"on":["Private"],"rules":[{"action":"Allow","profile":"Any"},{"action":"Block","profile":"Private, Public"}]}`, "blocks phones on this private network"},
		{"one of each", `{"networks":"Public","on":"Public","rules":{"action":"Block","profile":"Public"}}`, "blocks phones on this public network"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fw, err := parseFirewall([]byte(c.json))
			if err != nil {
				t.Fatal(err)
			}
			got := firewallWarning(fw, exe, 7780)
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("firewallWarning = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "may turn") && !strings.Contains(got, `localport=7780 program="`+exe+`"`) {
				t.Errorf("no rule to add in %q", got)
			}
		})
	}
}
