package hostvm

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
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
)

// The tests' executable is the test binary: Ready mustn't start it as
// `vm lan`. lanStarts counts what it would have started.
var lanStarts struct {
	sync.Mutex
	n int
}

func TestMain(m *testing.M) {
	startLAN = func(v *VM) error {
		if v.CHV == nil {
			lanStarts.Lock()
			lanStarts.n++
			lanStarts.Unlock()
		}
		return nil
	}
	os.Exit(m.Run())
}

// Bringing Lima's VM up starts `vm lan`.
func TestReadyStartsLAN(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running"}`+"\n"), 0o644)
	want, _ := vm.Digest()
	_ = os.WriteFile(filepath.Join(dir, "digest"), []byte(want+"\n"), 0o644)
	lanStarts.Lock()
	before := lanStarts.n
	lanStarts.Unlock()
	if err := vm.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	lanStarts.Lock()
	defer lanStarts.Unlock()
	if lanStarts.n != before+1 {
		t.Errorf("vm lan started %d times", lanStarts.n-before)
	}
}

// `vm lan` opens phones' port on this computer while the daemon in Lima's VM
// has them on, passes their requests to it over the forwarded socket, runs
// once per VM, and ends when the VM has stopped.
func TestRunLAN(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running"}`+"\n"), 0o644)
	sockDir, err := os.MkdirTemp("", "lan")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "agentbox.sock")
	t.Setenv("AGENTBOX_SOCKET", socket)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	reports := make(chan api.LANHostReport, 100)
	daemonLn, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	daemon := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/lan":
			_ = json.NewEncoder(w).Encode(api.LANStatus{Enabled: true, Port: port})
		case r.Method == "PUT" && r.URL.Path == "/v1/lan/host":
			var rep api.LANHostReport
			_ = json.NewDecoder(r.Body).Decode(&rep)
			reports <- rep
		default:
			_, _ = io.WriteString(w, "phone: "+r.URL.Path)
		}
	})}
	go func() { _ = daemon.Serve(daemonLn) }()

	lanCheckEvery = 50 * time.Millisecond
	t.Cleanup(func() { lanCheckEvery = 30 * time.Second })
	done := make(chan error, 1)
	go func() { done <- vm.RunLAN(context.Background()) }()
	select {
	case rep := <-reports:
		if !rep.Listening || rep.Port != port || rep.Error != "" {
			t.Fatalf("report = %+v", rep)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no report")
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
	if !lanRunning(vm.lanLock()) {
		t.Error("lanRunning says no")
	}
	if err := vm.RunLAN(context.Background()); err != nil {
		t.Errorf("a second vm lan: %v", err)
	}

	// The VM stops: its socket goes, and Lima says so.
	_ = daemon.Close()
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Stopped"}`+"\n"), 0o644)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("vm lan didn't end with the VM")
	}
	if _, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/"); err == nil {
		t.Error("the port is still open")
	}
	if lanRunning(vm.lanLock()) {
		t.Error("lanRunning says yes after it ended")
	}
	if !strings.Contains(vm.Log.(interface{ String() string }).String(), "the VM has stopped") {
		t.Errorf("log: %s", vm.Log)
	}
}

func TestMacFirewall(t *testing.T) {
	const exe = "/Applications/AgentBox.app/Contents/Resources/bin/agentbox"
	for _, c := range []struct {
		name, global, blockAll, signed, app, want string
	}{
		{"off", "Firewall is disabled. (State = 0)", "", "", "", ""},
		{"silent", "", "", "", "", ""},
		{"allowed", "Firewall is enabled. (State = 1)", "Firewall has block all state set to disabled.", "", "The application " + exe + " is permitted", ""},
		{"blocked", "Firewall is enabled. (State = 1)", "Firewall has block all state set to disabled.", "", "The application " + exe + " is blocked", "set agentbox (" + exe + ") to Allow"},
		{"block all", "Firewall is enabled. (State = 2)", "", "", "", "turn off Block all incoming connections"},
		{"unknown", "Firewall is enabled. (State = 1)", "Block all DISABLED!", "Automatically allow downloaded signed software DISABLED.", "The application is not part of the firewall", "macOS may ask"},
		{"signed", "Firewall is enabled. (State = 1)", "Block all DISABLED!", "Automatically allow downloaded signed software ENABLED.", "The application is not part of the firewall", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := firewallWarning(c.global, c.blockAll, c.signed, c.app, exe)
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("firewallWarning = %q, want %q", got, c.want)
			}
		})
	}
}
