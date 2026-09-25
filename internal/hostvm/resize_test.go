package hostvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseMemory(t *testing.T) {
	for in, want := range map[string]int64{
		"8GiB": 8 << 30, "8G": 8 << 30, "8gb": 8 << 30, "8": 8 << 30, " 12 GiB ": 12 << 30,
		"6.5GiB": 6<<30 + 512<<20, "6144MiB": 6 << 30, "512M": 512 << 20, "1TiB": 1 << 40,
	} {
		if got, err := ParseMemory(in); err != nil || got != want {
			t.Errorf("ParseMemory(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "GiB", "eight", "8 bananas", "-4GiB", "0", "8KiB"} {
		if got, err := ParseMemory(in); err == nil {
			t.Errorf("ParseMemory(%q) = %d, want an error", in, got)
		}
	}
}

func TestLimits(t *testing.T) {
	// A 16 GiB Mac with 10 cores keeps 2 GiB for itself.
	l := limitsFor(10, 16<<30)
	if l != (Limits{MinCPUs: 2, MaxCPUs: 10, MinMemory: 4 << 30, MaxMemory: 14 << 30}) {
		t.Fatalf("got %+v", l)
	}
	for _, ok := range [][2]int64{{2, 4 << 30}, {10, 14 << 30}, {6, 0}, {0, 8 << 30}} {
		if err := l.Check(int(ok[0]), ok[1]); err != nil {
			t.Errorf("%d CPUs, %d bytes: %v", ok[0], ok[1], err)
		}
	}
	for _, bad := range [][2]int64{{1, 0}, {11, 0}, {0, 3 << 30}, {0, 15 << 30}} {
		if err := l.Check(int(bad[0]), bad[1]); err == nil {
			t.Errorf("%d CPUs, %d bytes: no error", bad[0], bad[1])
		}
	}
	if err := l.Check(0, 15<<30); err == nil || !strings.Contains(err.Error(), "4GiB to 14GiB") {
		t.Errorf("the error doesn't say what's allowed: %v", err)
	}
	// A machine smaller than the minimum, or one that won't say, still
	// offers the minimum.
	if l := limitsFor(1, 4<<30); l.MaxCPUs != MinCPUs || l.MaxMemory != MinMemory {
		t.Errorf("a small machine: %+v", l)
	}
	if l := limitsFor(8, 0); l.MaxMemory != MinMemory {
		t.Errorf("unknown memory: %+v", l)
	}
}

func TestResizeArgs(t *testing.T) {
	l := limitsFor(8, 16<<30)
	if cpus, mem, err := resizeArgs(true, 6, true, "12GiB", l); err != nil || cpus != 6 || mem != 12<<30 {
		t.Errorf("both: %d, %d, %v", cpus, mem, err)
	}
	if cpus, mem, err := resizeArgs(false, 0, true, "6G", l); err != nil || cpus != 0 || mem != 6<<30 {
		t.Errorf("memory only: %d, %d, %v", cpus, mem, err)
	}
	for name, err := range map[string]error{
		"neither":      second(resizeArgs(false, 0, false, "", l)),
		"zero CPUs":    second(resizeArgs(true, 0, false, "", l)),
		"too many":     second(resizeArgs(true, 9, false, "", l)),
		"not a size":   second(resizeArgs(false, 0, true, "lots", l)),
		"too much":     second(resizeArgs(false, 0, true, "16GiB", l)),
		"too little":   second(resizeArgs(false, 0, true, "2GiB", l)),
		"both too big": second(resizeArgs(true, 64, true, "64GiB", l)),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second(_ int, _ int64, err error) error { return err }

const resizeList = `{"name":"agentbox","status":"%s","cpus":4,"memory":8589934592}` + "\n"

func writeList(t *testing.T, dir, status string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "list"), []byte(strings.Replace(resizeList, "%s", status, 1)), 0o644)
}

// A running VM is stopped, edited, started, and its daemon restarted, in that
// order: Lima won't edit a running instance.
func TestResizeRunning(t *testing.T) {
	vm, dir := newFake(t)
	writeList(t, dir, "Running")
	if err := vm.Resize(context.Background(), 6, 12<<30); err != nil {
		t.Fatal(err)
	}
	got := calls(t, dir)
	order := []string{"stop agentbox", "edit --tty=false --cpus 6 --memory 12 agentbox", "start --tty=false agentbox", "/usr/local/bin/agentbox daemon start"}
	at := 0
	for _, call := range got {
		if at < len(order) && strings.Contains(call, order[at]) {
			at++
		}
	}
	if at != len(order) {
		t.Errorf("wanted, in order, %q; it ran:\n%s", order, strings.Join(got, "\n"))
	}
}

// A stopped VM is edited and left stopped; only what's asked for changes.
func TestResizeStopped(t *testing.T) {
	vm, dir := newFake(t)
	writeList(t, dir, "Stopped")
	if err := vm.Resize(context.Background(), 0, 6<<30+512<<20); err != nil {
		t.Fatal(err)
	}
	got := calls(t, dir)
	if !slices.Contains(got, "edit --tty=false --memory 6.5 agentbox") {
		t.Errorf("no edit of the memory alone: %q", got)
	}
	for _, call := range got {
		if strings.HasPrefix(call, "start") || strings.HasPrefix(call, "stop") || strings.Contains(call, "daemon") {
			t.Errorf("a stopped VM was started or stopped: %q", got)
		}
	}
}

func TestResizeNothingToDo(t *testing.T) {
	vm, dir := newFake(t)
	writeList(t, dir, "Running")
	if err := vm.Resize(context.Background(), 4, 8<<30); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, dir); !slices.Equal(got, []string{"list --format json"}) {
		t.Errorf("a VM already that size was touched: %q", got)
	}
	os.Remove(filepath.Join(dir, "list"))
	if err := vm.Resize(context.Background(), 4, 0); !errors.Is(err, ErrNotCreated) {
		t.Errorf("no VM: got %v", err)
	}
}

// Nothing starts the VM while a resize has it stopped: Up waits for the resize
// to let go of the VM.
func TestUpWaitsForResize(t *testing.T) {
	vm, dir := newFake(t)
	writeList(t, dir, "Stopped")
	unlock, err := vm.lock(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- vm.Up(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("Up didn't wait for the resize: %v", err)
	case <-time.After(600 * time.Millisecond):
	}
	if slices.ContainsFunc(calls(t, dir), func(c string) bool { return strings.HasPrefix(c, "start") }) {
		t.Fatal("the VM was started under the resize")
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(vm.Log.(*bytes.Buffer).String(), "being resized") {
		t.Errorf("Up didn't say what it waits for: %q", vm.Log)
	}
	if !slices.Contains(calls(t, dir), "start --tty=false agentbox") {
		t.Errorf("Up didn't start the VM once the resize was done: %q", calls(t, dir))
	}
	// A shared lock doesn't keep out another.
	u1, err := vm.lock(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer u1()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if u2, err := vm.lock(ctx, false); err != nil {
		t.Errorf("two shared locks: %v", err)
	} else {
		u2()
	}
}
