package chv

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The helper: this test binary, run as `agentbox vm gpu-device` is.
func TestMain(m *testing.M) {
	if node := os.Getenv("CHV_TEST_GPU_DEVICE"); node != "" {
		if err := RunGPUDevice(node, []string{"ls", filepath.Dir(node)}); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// In its namespaces, the GPU device sees only its node in /dev/dri; outside,
// nothing changes.
func TestRunGPUDevice(t *testing.T) {
	dri := filepath.Join(t.TempDir(), "dri")
	_ = os.MkdirAll(dri, 0o755)
	for _, n := range []string{"card0", "renderD128", "renderD129"} {
		_ = os.WriteFile(filepath.Join(dri, n), []byte(n), 0o644)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "CHV_TEST_GPU_DEVICE="+filepath.Join(dri, "renderD129"))
	cmd.SysProcAttr = gpuDeviceAttr()
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("no unprivileged user namespaces here: %v", err)
	}
	if err != nil || strings.TrimSpace(string(out)) != "renderD129" {
		t.Fatalf("%v: %q", err, out)
	}
	if left, _ := os.ReadDir(dri); len(left) != 3 {
		t.Errorf("outside, %s changed: %v", dri, left)
	}
}

// One render node: crosvm runs as it is; more: through `vm gpu-device`, in
// namespaces of its own, seeing only the VM's.
func TestGPUCommand(t *testing.T) {
	m := &chMachine{l: Layout{Root: "/v", Name: "vm"}, self: "/usr/bin/agentbox"}
	path, args, attr := m.gpuCommand(amdDiscrete, 1)
	if path != "/v/bin/gpu/bin/crosvm" || attr != nil || args[0] != "--no-syslog" {
		t.Errorf("one node: %s %q %v", path, args, attr)
	}
	path, args, attr = m.gpuCommand(amdDiscrete, 2)
	if path != "/usr/bin/agentbox" || attr == nil || attr.Cloneflags == 0 || !slices.Equal(args[:5], []string{"vm", "gpu-device", amdDiscrete.Path, "--", "/v/bin/gpu/bin/crosvm"}) || args[5] != "--no-syslog" {
		t.Errorf("two nodes: %s %q %+v", path, args, attr)
	}
}
