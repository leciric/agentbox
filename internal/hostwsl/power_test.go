package hostwsl

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestVMCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"vm", "power", "--json"}, true},
		{[]string{"vm", "start"}, true},
		{[]string{"vm", "stop"}, true},
		{[]string{"vm", "pause"}, true},
		{[]string{"vm", "status", "--json"}, false},
		{[]string{"vm", "disk", "--json"}, false},
		{[]string{"vm"}, false},
		{[]string{"list"}, false},
	} {
		if got := vmCommand(tc.args); got != tc.want {
			t.Errorf("vmCommand(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestParseMeminfo(t *testing.T) {
	total, available, cpus := parseMeminfo("MemTotal:       8000000 kB\nMemFree:  100 kB\nMemAvailable:   2000000 kB\nSwapTotal: 0 kB\n12\n")
	if total != 8000000<<10 || available != 2000000<<10 || cpus != 12 {
		t.Fatalf("got %d, %d, %d", total, available, cpus)
	}
}

// TestPower follows the distro through the top bar's eyes: missing, stopped,
// running with its memory, and stopped again by Free resources, without
// the poll or the app's requests starting it in between.
func TestPower(t *testing.T) {
	d := fakeDistro(t)
	ctx := context.Background()
	if p := d.Power(ctx); p.State != api.VMOff || p.Error != ErrNotCreated.Error() || p.Driver != api.VMDriverWSL {
		t.Fatalf("before import: %+v", p)
	}
	imported(t, d)
	if err := d.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stopped := func(when string) {
		t.Helper()
		if p := d.Power(ctx); p.State != api.VMOff || p.Error != "" || p.MemoryGranted != 0 {
			t.Fatalf("%s: %+v", when, p)
		}
		if st, err := d.State(ctx); err != nil || st.State != "Stopped" {
			t.Fatalf("%s: the distro is %+v, %v", when, st, err)
		}
	}
	stopped("stopped")

	// The relay says there's no daemon rather than start the distro to ask.
	ln, path, err := listenPrivate()
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = (&Relay{Distro: d}).Serve(rctx, ln) }()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return dialPrivate(path) },
	}}
	get := func() (int, string) {
		t.Helper()
		resp, err := client.Get("http://agentbox/v1/version")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(); code != http.StatusServiceUnavailable || !strings.Contains(body, "is stopped") {
		t.Fatalf("relay to a stopped distro: %d %s", code, body)
	}
	stopped("after a request through the relay")

	// vm start is wsl start: the distro's agentbox, the distro and its daemon.
	if _, err := d.EnsureBinary(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.StartDaemon(ctx); err != nil {
		t.Fatal(err)
	}
	p := d.Power(ctx)
	const GiB = 1 << 30
	if p.State != api.VMRunning || p.MemoryGranted != 16*GiB || p.MemoryCap != 16*GiB || p.MemoryUsed != 6.5*GiB || p.CPUs != 8 || p.Error != "" {
		t.Fatalf("running: %+v", p)
	}
	if code, body := get(); code != http.StatusOK {
		t.Fatalf("relay to the daemon: %d %s", code, body)
	}

	// vm stop: the daemon, then the distro.
	if err := d.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if d.daemonAnswers(ctx, 0) {
		t.Fatal("the daemon still answers after Stop")
	}
	if err := d.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stopped("after Stop")
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("Stop on a stopped distro: %v", err)
	}
}
