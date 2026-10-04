package cli

import (
	"net"
	"strconv"
	"testing"
)

func TestRunsOnFrontEnd(t *testing.T) {
	if !RunsOnFrontEnd([]string{"machines", "serve"}) || RunsOnFrontEnd([]string{"media"}) || RunsOnFrontEnd(nil) {
		t.Fatal("RunsOnFrontEnd")
	}
}

func TestListenMachinesFallsBackOnlyFromTheDefault(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	port := taken.Addr().(*net.TCPAddr).Port
	if _, err := listenMachines(port, true); err == nil {
		t.Fatal("an explicit port that's taken was replaced")
	}
	ln, err := listenMachines(port, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	host, p, _ := net.SplitHostPort(ln.Addr().String())
	if host != "127.0.0.1" || p == strconv.Itoa(port) {
		t.Fatalf("fell back to %s", ln.Addr())
	}
}
