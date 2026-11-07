package agent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSwapLimitIsAQuarterOfMemoryMax(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		limit, swap int64
		share       int
		want        int64
	}{
		{8 * gib, 9 * gib, 0, 2 * gib},            // a quarter of memory.max
		{8 * gib, 2 * gib, 0, 2 * gib / 100 * 50}, // its share of a small swap
		{8 * gib, 2 * gib, 30, 2 * gib / 100 * 30},
		{8 * gib, 0, 50, -1}, // no swap: no limit, nothing to swap to
		{0, 9 * gib, 50, -1}, // no memory.max: none
	} {
		if got := SwapLimit(tc.limit, tc.swap, tc.share); got != tc.want {
			t.Errorf("SwapLimit(%d, %d, %d) = %d, want %d", tc.limit, tc.swap, tc.share, got, tc.want)
		}
	}
	if c := AgentCaps(8*gib, -1, 50); c.SwapMax != 0 || c.CPUWeight != AgentCPUWeight || c.PidsMax != AgentPidsMax {
		t.Errorf("AgentCaps with unreadable swap = %+v, want swap untouched and the rest set", c)
	}
	// Below the daemon's and Incus's default 100, so builds yield to them.
	if AgentCPUWeight >= 100 || AgentCPUWeight < 1 {
		t.Errorf("AgentCPUWeight = %d, want below the default 100", AgentCPUWeight)
	}
}

func TestSetCapsWritesSwapCPUAndPidsOnlyWhenTheyChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// As a machine starts: Incus leaves them all at the kernel's defaults.
	writeCgroupFile(t, filepath.Join(dir, "memory.swap.max"), "max\n")
	writeCgroupFile(t, filepath.Join(dir, "cpu.weight"), "100\n")
	writeCgroupFile(t, filepath.Join(dir, "pids.max"), "max\n")
	var writes []string
	write := func(path, value string) error {
		writes = append(writes, filepath.Base(path)+"="+value)
		return os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
	caps := Caps{SwapMax: 4*gib + 5, CPUWeight: AgentCPUWeight, PidsMax: AgentPidsMax}
	if err := setCaps(dir, caps, write); err != nil {
		t.Fatal(err)
	}
	want := []string{"memory.swap.max=4294967296", "cpu.weight=10", "pids.max=8192"}
	if !slices.Equal(writes, want) {
		t.Errorf("writes = %v, want %v", writes, want)
	}
	// Every few seconds after: nothing changed, nothing written.
	if err := setCaps(dir, caps, write); err != nil || len(writes) != 3 {
		t.Errorf("a second pass wrote %v, %v", writes[3:], err)
	}
	// The VM's swap went away: no limit rather than none of it.
	if err := setCaps(dir, Caps{SwapMax: -1}, write); err != nil || writes[len(writes)-1] != "memory.swap.max=max" {
		t.Errorf("no swap: %v, %v", writes, err)
	}
	// A zero field leaves its file as it is.
	n := len(writes)
	if err := setCaps(dir, Caps{}, write); err != nil || len(writes) != n {
		t.Errorf("empty caps wrote %v, %v", writes[n:], err)
	}
}

func TestSetCapsSkipsWhatTheKernelDoesntHave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No cpu or pids controller delegated, and no swap accounting.
	writeCgroupFile(t, filepath.Join(dir, "memory.max"), "max\n")
	write := func(path, value string) error {
		t.Errorf("wrote %s=%s to a file that isn't there", filepath.Base(path), value)
		return nil
	}
	caps := Caps{SwapMax: gib, CPUWeight: AgentCPUWeight, PidsMax: AgentPidsMax}
	if err := setCaps(dir, caps, write); err != nil {
		t.Errorf("setCaps = %v", err)
	}
	if err := setCaps(filepath.Join(dir, "stopped"), caps, write); err != nil {
		t.Errorf("a machine with no cgroup: %v", err)
	}
}
