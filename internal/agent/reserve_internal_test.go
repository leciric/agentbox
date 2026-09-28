package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// The reserve only reaches the apps inside the slices with the cgroup2 mount's
// memory_recursiveprot.
func TestReserveReachesApps(t *testing.T) {
	old := mountInfo
	t.Cleanup(func() { mountInfo = old })
	for want, info := range map[bool]string{
		true:  "35 24 0:30 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:9 - cgroup2 cgroup2 rw,nsdelegate,memory_recursiveprot\n",
		false: "35 24 0:30 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:9 - cgroup2 cgroup2 rw,nsdelegate\n",
	} {
		mountInfo = filepath.Join(t.TempDir(), "mountinfo")
		if err := os.WriteFile(mountInfo, []byte("22 1 259:2 / / rw - btrfs /dev/mapper/root rw\n"+info), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ReserveReachesApps(); got != want {
			t.Errorf("ReserveReachesApps with %q = %v", info, got)
		}
	}
	mountInfo = filepath.Join(t.TempDir(), "missing")
	if !ReserveReachesApps() {
		t.Error("with nothing to read, it should assume the reserve works")
	}
}
