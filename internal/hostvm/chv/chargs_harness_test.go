package chv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteCHArgs writes the VM's Cloud Hypervisor command line, as chArgs
// makes it for a VM in $CHV_ARGS_OUT, without the GPU (nogpu.args) and with
// it (gpu.args), and crosvm's (crosvm.args, for /dev/dri/renderD128), one
// argument a line, for scripts/chv-gpu/test/chv_args_test.sh,
// which runs AgentBox's Cloud Hypervisor and upstream's on them with no KVM.
func TestWriteCHArgs(t *testing.T) {
	dir := os.Getenv("CHV_ARGS_OUT")
	if dir == "" {
		t.Skip("CHV_ARGS_OUT isn't set")
	}
	l := Layout{Root: dir, Name: "vm"}
	c := Config{Name: "vm", CPUs: 2, MemoryMin: 2 * GiB, MemoryCap: 8 * GiB, Home: "/home/u", IOLimit: 100 << 20}
	for _, f := range []string{l.RootDisk(), l.PoolDisk(), l.Seed(), l.Bin("CLOUDHV.fd")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(f, 4<<20); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(l.Run(), 0o755); err != nil {
		t.Fatal(err)
	}
	room := Room{CPUs: 4, Memory: 16 * GiB}
	for name, gpu := range map[string]bool{"nogpu.args": false, "gpu.args": true} {
		args := strings.Join(chArgs(c, l, room, gpu), "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(args), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := strings.Join(crosvmArgs(l, "/dev/dri/renderD128"), "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "crosvm.args"), []byte(args), 0o644); err != nil {
		t.Fatal(err)
	}
}
