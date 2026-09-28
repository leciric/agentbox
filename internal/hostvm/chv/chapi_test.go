package chv

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCH is Cloud Hypervisor's API, on a unix socket, recording what it's
// asked.
type fakeCH struct {
	mu    sync.Mutex
	calls []string
	info  string // vm.info's body
	fail  string // an endpoint that answers 500
}

func (f *fakeCH) serve(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "ch.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		call := r.Method + " " + endpoint
		if len(body) > 0 {
			call += " " + string(body)
		}
		f.calls = append(f.calls, call)
		info, fail := f.info, f.fail
		f.mu.Unlock()
		switch {
		case endpoint == fail:
			http.Error(w, "Error from API: InvalidStateTransition", http.StatusInternalServerError)
		case endpoint == "vm.info":
			_, _ = io.WriteString(w, info)
		case endpoint == "vmm.ping":
			_, _ = io.WriteString(w, `{"version":"v53.0"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket
}

func (f *fakeCH) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestCHClient(t *testing.T) {
	f := &fakeCH{info: `{"config":{"memory":{"size":4294967296,"hotplug_size":8589934592,"hotplugged_size":2147483648}},"state":"Running","memory_actual_size":5368709120}`}
	c := newCHClient(f.serve(t))
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != chRunning || info.Requested() != 6*GiB || info.MemoryActualSize != 5*GiB {
		t.Errorf("Info = %+v", info)
	}
	for _, call := range []func(context.Context) error{c.Ping, c.Pause, c.Resume, c.PowerButton, c.ShutdownVMM} {
		if err := call(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Resize(ctx, 6*GiB); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET vm.info", "GET vmm.ping", "PUT vm.pause", "PUT vm.resume", "PUT vm.power-button", "PUT vmm.shutdown", `PUT vm.resize {"desired_ram":6442450944}`}
	if got := f.called(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCHClientError(t *testing.T) {
	f := &fakeCH{fail: "vm.pause"}
	err := newCHClient(f.serve(t)).Pause(context.Background())
	if err == nil || !strings.Contains(err.Error(), "InvalidStateTransition") {
		t.Errorf("Pause = %v, want Cloud Hypervisor's error", err)
	}
}

func TestCHClientNoVM(t *testing.T) {
	err := newCHClient(filepath.Join(t.TempDir(), "none.sock")).Ping(context.Background())
	if err == nil {
		t.Error("Ping succeeded with nothing listening")
	}
}
