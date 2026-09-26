package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentSwap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.ab-app-agent-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.swap.current"), []byte("18790481920\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := agentSwap(root, "ab-app-agent-01"), int64(18790481920); got != want {
		t.Errorf("agentSwap = %d, want %d", got, want)
	}
	if got := agentSwap(root, "ab-other-agent-01"); got != 0 {
		t.Errorf("without a cgroup: agentSwap = %d, want 0", got)
	}
}

func TestLimitBytes(t *testing.T) {
	tests := []struct {
		memory    string
		hostTotal int64
		want      int64
	}{
		{"", 32 << 30, 0},
		{"8GiB", 32 << 30, 8 << 30},
		{"50%", 32 << 30, 16 << 30},
		{"50%", 0, 0},
		{"not a size", 32 << 30, 0},
	}
	for _, tc := range tests {
		if got := limitBytes(tc.memory, tc.hostTotal); got != tc.want {
			t.Errorf("limitBytes(%q, %d) = %d, want %d", tc.memory, tc.hostTotal, got, tc.want)
		}
	}
}

func TestZramTotal(t *testing.T) {
	root := t.TempDir()

	// zram0 is sized and backing swap.
	zram0 := filepath.Join(root, "zram0")
	if err := os.MkdirAll(zram0, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zram0, "disksize"), []byte("17179869184\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// orig_data_size compr_data_size mem_used_total mem_limit mem_used_max same_pages pages_compacted huge_pages huge_pages_since
	if err := os.WriteFile(filepath.Join(zram0, "mm_stat"), []byte("18790481920 3758096384 3958096384 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// zram1 is loaded but never sized, the way zramctl skips it too.
	zram1 := filepath.Join(root, "zram1")
	if err := os.MkdirAll(zram1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zram1, "disksize"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := zramTotal(root)
	if !ok {
		t.Fatal("zramTotal ok = false, want true")
	}
	if got.SwapBytes != 18790481920 || got.RealBytes != 3958096384 {
		t.Errorf("zramTotal = %+v, want SwapBytes 18790481920, RealBytes 3958096384", got)
	}

	if _, ok := zramTotal(t.TempDir()); ok {
		t.Error("zramTotal on an empty directory: ok = true, want false")
	}
}
