package hostvm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

const GiB = int64(1) << 30

func TestParseSwapProbe(t *testing.T) {
	d, err := parseSwapProbe("107374182400 64424509440\n8589934592\n1\n")
	if err != nil || d != (swapDisk{Total: 100 * GiB, Avail: 60 * GiB, Current: 8 * GiB, Active: true}) {
		t.Fatalf("got %+v, %v", d, err)
	}
	d, err = parseSwapProbe("107374182400 64424509440\n0\n0\n")
	if err != nil || d.Current != 0 || d.Active {
		t.Fatalf("no swapfile: got %+v, %v", d, err)
	}
	for _, bad := range []string{"", "sudo: a password is required\n", "1 2 3 x\n"} {
		if _, err := parseSwapProbe(bad); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestCheckSwap(t *testing.T) {
	// A 100 GiB disk keeps the larger of 10 GiB and 5%: 10 GiB.
	floor := agent.DefaultDiskFloor
	d := swapDisk{Total: 100 * GiB, Avail: 20 * GiB}
	if err := checkSwap(d, 8*GiB, floor); err != nil {
		t.Errorf("8 GiB of 20 free, keeping 10: %v", err)
	}
	if err := checkSwap(d, 10*GiB, floor); err != nil {
		t.Errorf("exactly down to the floor: %v", err)
	}
	err := checkSwap(d, 16*GiB, floor)
	if err == nil || !strings.Contains(err.Error(), "under the 10.0 GiB it keeps free") || !strings.Contains(err.Error(), "at most 10GiB") {
		t.Errorf("16 GiB of 20 free: %v", err)
	}
	// The swapfile there now is given back before the new one is made.
	if err := checkSwap(swapDisk{Total: 100 * GiB, Avail: 12 * GiB, Current: 8 * GiB}, 10*GiB, floor); err != nil {
		t.Errorf("resizing 8 to 10 GiB with 12 free: %v", err)
	}
	err = checkSwap(swapDisk{Total: 100 * GiB, Avail: 10 * GiB}, 1*GiB, floor)
	if err == nil || !strings.Contains(err.Error(), "no room for swap") {
		t.Errorf("a disk at its floor: %v", err)
	}
}

func TestSwapScript(t *testing.T) {
	on := swapScript(8 * GiB)
	for _, want := range []string{"swapoff \"$f\"", "fallocate -l 8589934592", "count=8192", "mkswap", "swapon \"$f\"", "/swapfile none swap sw,nofail 0 0\" >>/etc/fstab"} {
		if !strings.Contains(strings.ReplaceAll(on, "$f none", "/swapfile none"), want) {
			t.Errorf("on doesn't %q:\n%s", want, on)
		}
	}
	off := swapScript(0)
	if !strings.Contains(off, "swapoff") || !strings.Contains(off, "/etc/fstab") || strings.Contains(off, "mkswap") {
		t.Errorf("off:\n%s", off)
	}
}

func TestSwapArgs(t *testing.T) {
	for _, c := range []struct {
		args    []string
		sizeSet bool
		size    string
		current int64
		want    int64
	}{
		{nil, false, "", 0, -1},
		{[]string{"on"}, false, "", 0, api.VMSwapDefault},
		{[]string{"on"}, false, "", 4 * GiB, 4 * GiB},
		{[]string{"on"}, true, "12G", 4 * GiB, 12 * GiB},
		{[]string{"off"}, false, "", 4 * GiB, 0},
	} {
		if got, err := swapArgs(c.args, c.sizeSet, c.size, c.current); err != nil || got != c.want {
			t.Errorf("%v --size %q: got %d, %v; want %d", c.args, c.size, got, err, c.want)
		}
	}
	for _, bad := range [][]string{{"maybe"}, {"off"}, nil} {
		if _, err := swapArgs(bad, true, "8G", 0); err == nil {
			t.Errorf("%v --size 8G: no error", bad)
		}
	}
	if _, err := swapArgs([]string{"on"}, true, "lots", 0); err == nil {
		t.Error("--size lots: no error")
	}
}

func TestSwap(t *testing.T) {
	vm, dir := newFake(t)
	ctx := context.Background()
	if err := vm.Swap(ctx, 8*GiB); err == nil || !strings.Contains(err.Error(), "isn't set up") {
		t.Fatalf("no VM: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Stopped"}`+"\n"), 0o644)
	if err := vm.Swap(ctx, 8*GiB); err == nil || !strings.Contains(err.Error(), "is stopped: start it") {
		t.Fatalf("a stopped VM: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running"}`+"\n"), 0o644)
	if err := vm.Swap(ctx, 256<<20); err == nil || !strings.Contains(err.Error(), "at least 512MiB") {
		t.Fatalf("256 MiB: %v", err)
	}

	// 20 GiB free on a 100 GiB disk: room for 8, not for 16. No daemon
	// answers here, so the floor is the default 10 GiB.
	probe := filepath.Join(dir, "swapprobe")
	_ = os.WriteFile(probe, []byte("107374182400 21474836480\n0\n0\n"), 0o644)
	if err := vm.Swap(ctx, 16*GiB); err == nil || !strings.Contains(err.Error(), "make it at most 10GiB") {
		t.Fatalf("16 GiB: %v", err)
	}
	if strings.Contains(strings.Join(calls(t, dir), "\n"), "mkswap") {
		t.Fatal("a refused swapfile was made")
	}
	if err := vm.Swap(ctx, 8*GiB); err != nil {
		t.Fatal(err)
	}
	log := strings.Join(calls(t, dir), "\n")
	if !strings.Contains(log, "shell agentbox -- sudo sh -c set -e") || !strings.Contains(log, "fallocate -l 8589934592") {
		t.Errorf("the swapfile wasn't made as root; it ran:\n%s", log)
	}
	if got := SwapSize(vm.Paths, vm.Name); got != 8*GiB {
		t.Errorf("the host kept %d", got)
	}
	if out := vm.Log.(*bytes.Buffer).String(); !strings.Contains(out, "8GiB of swap now") {
		t.Errorf("it said:\n%s", out)
	}

	// The VM has it already: nothing to do.
	_ = os.WriteFile(probe, []byte("107374182400 12884901888\n8589934592\n1\n"), 0o644)
	_ = os.Remove(filepath.Join(dir, "calls"))
	if err := vm.Swap(ctx, 8*GiB); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(calls(t, dir), "\n"), "sudo") {
		t.Error("a swapfile the VM has already was made again")
	}

	// Off takes it away, and the host forgets it.
	if err := vm.Swap(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if log := strings.Join(calls(t, dir), "\n"); !strings.Contains(log, "swapoff") || strings.Contains(log, "mkswap") {
		t.Errorf("off ran:\n%s", log)
	}
	if got := SwapSize(vm.Paths, vm.Name); got != 0 {
		t.Errorf("the host still keeps %d", got)
	}
}

func TestWithSwap(t *testing.T) {
	vm, _ := newFake(t)
	if err := writeSwapSize(vm.Paths, "agentbox", 6*GiB); err != nil {
		t.Fatal(err)
	}
	st := api.VMStatus{Mode: api.ModeVM, Name: "agentbox", State: api.VMRunning, Swap: &api.VMSwap{Total: 6 * GiB, Used: GiB}}
	withSwap(&st, vm.Paths)
	if *st.Swap != (api.VMSwap{Size: 6 * GiB, Total: 6 * GiB, Used: GiB}) {
		t.Errorf("running: %+v", *st.Swap)
	}
	off := api.VMStatus{Mode: api.ModeVM, Name: "agentbox", State: api.VMOff}
	withSwap(&off, vm.Paths)
	if off.Swap == nil || off.Swap.Size != 6*GiB {
		t.Errorf("off: %+v", off.Swap)
	}
	missing := api.VMStatus{Mode: api.ModeVM, Name: "agentbox", State: api.VMMissing}
	if withSwap(&missing, vm.Paths); missing.Swap != nil {
		t.Errorf("missing: %+v", missing.Swap)
	}
	for sw, want := range map[api.VMSwap]string{
		{}:              "no swap",
		{Size: 8 * GiB}: "has 8GiB of swap.",
		{Size: 8 * GiB, Total: 8 * GiB, Used: GiB}: "1GiB of it in use",
		{Total: 2 * GiB}: "didn't make",
	} {
		if got := describeSwap(sw); !strings.Contains(got, want) {
			t.Errorf("describeSwap(%+v) = %q, want %q", sw, got, want)
		}
	}
}
