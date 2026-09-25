package agent

import "testing"

// TestHostCPUAndHostMemoryReadTheRealProc reads the host's own /proc, which
// exists on every Linux CI runner: they only have to come back sane, not any
// particular number.
func TestHostCPUAndHostMemoryReadTheRealProc(t *testing.T) {
	times, err := hostCPU()
	if err != nil {
		t.Fatalf("hostCPU() = %v", err)
	}
	if times.total == 0 {
		t.Error("hostCPU() total = 0, want some elapsed time")
	}
	if times.idle > times.total {
		t.Errorf("hostCPU() idle %d > total %d", times.idle, times.total)
	}

	total, used, err := hostMemory()
	if err != nil {
		t.Fatalf("hostMemory() = %v", err)
	}
	if total <= 0 {
		t.Errorf("hostMemory() total = %d, want > 0", total)
	}
	if used < 0 || used > total {
		t.Errorf("hostMemory() used = %d, total = %d", used, total)
	}
	if got := HostMemory(); got != total {
		t.Errorf("HostMemory() = %d, want the same total = %d", got, total)
	}
}

func TestHostCoresIsAtLeastOne(t *testing.T) {
	if HostCores() < 1 {
		t.Errorf("HostCores() = %d, want at least 1", HostCores())
	}
}
