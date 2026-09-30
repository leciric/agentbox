package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCgroupCPU(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, ok := cgroupCPU(dir); ok {
		t.Error("read a cgroup with no cpu.stat")
	}
	stat := "usage_usec 90500000\nuser_usec 60000000\nsystem_usec 30500000\nnr_periods 0\n"
	if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if used, ok := cgroupCPU(dir); !ok || used != 90500*time.Millisecond {
		t.Errorf("cgroupCPU = %v, %t, want 1m30.5s", used, ok)
	}
}
