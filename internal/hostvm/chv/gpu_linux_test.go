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
	if err != nil && strings.Contains(err.Error(), "operation not permitted") || strings.Contains(string(out), "permission denied") {
		// No user namespaces, or ones a program can't mount in (Ubuntu
		// 24.04's AppArmor restriction): the supervisor tries bubblewrap.
		t.Skipf("unprivileged user namespaces are restricted here: %v %s", err, out)
	}
	if err != nil || strings.TrimSpace(string(out)) != "renderD129" {
		t.Fatalf("%v: %q", err, out)
	}
	if left, _ := os.ReadDir(dri); len(left) != 3 {
		t.Errorf("outside, %s changed: %v", dri, left)
	}
}

// One render node: crosvm runs as it is; more: through `vm gpu-device`, in
// namespaces of its own, seeing only the VM's, and then through bubblewrap
// where there is one.
func TestGPULaunches(t *testing.T) {
	old := bwrapPath
	t.Cleanup(func() { bwrapPath = old })
	bwrapPath = func() string { return "" }
	m := &chMachine{l: Layout{Root: "/v", Name: "vm"}, self: "/usr/bin/agentbox"}
	one := m.gpuLaunches(amdDiscrete, 1)
	if len(one) != 1 || one[0].path != "/v/bin/gpu/bin/crosvm" || one[0].attr != nil || one[0].args[0] != "--no-syslog" {
		t.Errorf("one node: %+v", one)
	}
	two := m.gpuLaunches(amdDiscrete, 2)
	if len(two) != 1 || two[0].path != "/usr/bin/agentbox" || two[0].attr == nil || two[0].attr.Cloneflags == 0 ||
		!slices.Equal(two[0].args[:5], []string{"vm", "gpu-device", amdDiscrete.Path, "--", "/v/bin/gpu/bin/crosvm"}) || two[0].args[5] != "--no-syslog" {
		t.Errorf("two nodes: %+v", two)
	}
	bwrapPath = func() string { return "/usr/bin/bwrap" }
	two = m.gpuLaunches(amdDiscrete, 2)
	if len(two) != 2 || two[1].path != "/usr/bin/bwrap" || two[1].attr != nil ||
		!slices.Equal(two[1].args[:9], []string{"--dev-bind", "/", "/", "--tmpfs", "/dev/dri", "--dev-bind", amdDiscrete.Path, amdDiscrete.Path, "--"}) || two[1].args[9] != "/v/bin/gpu/bin/crosvm" {
		t.Errorf("with bwrap: %+v", two)
	}
}
