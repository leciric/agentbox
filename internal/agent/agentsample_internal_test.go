package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadAgentCountersAndRates(t *testing.T) {
	root, sys := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "lxc.payload.box")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 259:0 is a physical disk; 253:0, device-mapper on top of it, isn't.
	if err := os.MkdirAll(filepath.Join(sys, "dev", "block", "259:0", "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.current", "1000\n")
	write("memory.stat", "anon 600\nactive_file 300\ninactive_file 100\n")
	write("memory.swap.current", "50\n")
	write("cpu.stat", "usage_usec 1000000\nuser_usec 1\n")
	write("io.stat", "259:0 rbytes=1000 wbytes=2000\n253:0 rbytes=1000 wbytes=2000\n")

	t0 := time.Unix(100, 0)
	before, ok := readAgentCounters(root, sys, "box", t0)
	if !ok {
		t.Fatal("not read")
	}
	if before.Memory != 600 || before.Swap != 50 || before.CPU != time.Second || before.DiskRead != 1000 || before.DiskWrite != 2000 {
		t.Fatalf("before = %+v", before)
	}

	write("cpu.stat", "usage_usec 4000000\n") // 3 s of CPU over 2 s: 150%
	write("io.stat", "259:0 rbytes=5000 wbytes=2000\n")
	after, _ := readAgentCounters(root, sys, "box", t0.Add(2*time.Second))
	r := Rates(before, after)
	if r.CPU != 150 || r.DiskRead != 2000 || r.DiskWrite != 0 || r.Memory != 600 || r.Swap != 50 {
		t.Fatalf("rates = %+v", r)
	}

	// A restarted machine's counters start again from zero: no negative rate.
	write("cpu.stat", "usage_usec 10\n")
	write("io.stat", "259:0 rbytes=1 wbytes=1\n")
	again, _ := readAgentCounters(root, sys, "box", t0.Add(4*time.Second))
	if r := Rates(after, again); r.CPU != 0 || r.DiskRead != 0 || r.DiskWrite != 0 {
		t.Fatalf("after a restart = %+v", r)
	}

	if _, ok := readAgentCounters(root, sys, "gone", t0); ok {
		t.Fatal("a stopped machine read")
	}
}
