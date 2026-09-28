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

// TestHostDiskIOReadsTheRealProc only asks the real /proc/diskstats to parse:
// which disks a runner has, and whether it keeps PSI, differ from one to the
// next.
func TestHostDiskIOReadsTheRealProc(t *testing.T) {
	if _, err := hostDiskIO("/proc/diskstats", sysRoot); err != nil {
		t.Fatalf("hostDiskIO() = %v", err)
	}
	if p, ok := hostPressure("/proc/pressure"); ok && (p.IOFull < 0 || p.IOFull > 100 || p.MemoryFull < 0 || p.MemoryFull > 100) {
		t.Errorf("hostPressure() = %+v, want percentages", p)
	}
}

// In a Cloud Hypervisor VM, HostMemory is the cap the front end says the VM
// may grow to, not the little it has been granted so far; what's in use still
// comes from /proc/meminfo.
func TestHostMemoryHonoursTheVMsCap(t *testing.T) {
	t.Setenv(VMMemoryCapEnv, "25769803776") // 24 GiB
	if got := HostMemory(); got != 24<<30 {
		t.Errorf("HostMemory() = %d, want the VM's cap", got)
	}
	if total, _, err := hostMemory(); err != nil || total <= 0 {
		t.Errorf("hostMemory() = %d, %v: it should still read /proc/meminfo", total, err)
	}
	for _, bad := range []string{"", "0", "-1", "lots"} {
		t.Setenv(VMMemoryCapEnv, bad)
		total, _, _ := hostMemory()
		if got := HostMemory(); got != total {
			t.Errorf("with %s=%q, HostMemory() = %d, want /proc/meminfo's %d", VMMemoryCapEnv, bad, got, total)
		}
	}
}
