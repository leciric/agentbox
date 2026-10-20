package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// withVenus points VenusDir at a directory of its own, with the driver the
// Mac's VM builds in it, or nothing.
func withVenus(t *testing.T, built bool) string {
	t.Helper()
	dir := t.TempDir()
	old := VenusDir
	VenusDir = dir
	t.Cleanup(func() { VenusDir = old })
	if built {
		_ = os.WriteFile(filepath.Join(dir, "libvulkan_virtio.so"), []byte("ELF"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "multiarch"), []byte("aarch64-linux-gnu\n"), 0o644)
	}
	return dir
}

func TestVenusDriver(t *testing.T) {
	withVenus(t, false)
	if s, p := venusDriver(); s != "" || p != "" {
		t.Errorf("no driver built: %q, %q", s, p)
	}
	dir := withVenus(t, true)
	s, p := venusDriver()
	if s != filepath.Join(dir, "libvulkan_virtio.so") || p != "/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so" {
		t.Errorf("got %q, %q", s, p)
	}
	// A build that stopped before it said where agents keep theirs.
	_ = os.Remove(filepath.Join(dir, "multiarch"))
	if s, _ := venusDriver(); s != "" {
		t.Errorf("without multiarch: %q", s)
	}
}

// The driver goes in when GPU for agents is on and the VM built one, is left
// alone when it's in already, is mounted again when it moved, and comes off
// when the setting is off or there's no driver any more.
func TestVGPUDriverSteps(t *testing.T) {
	dir := withVenus(t, true)
	source := filepath.Join(dir, "libvulkan_virtio.so")
	add := []string{"config", "device", "add", "i", "agentbox-vgpu-driver", "disk",
		"source=" + source, "path=/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so", "readonly=true"}
	remove := []string{"config", "device", "remove", "i", "agentbox-vgpu-driver"}
	mounted := map[string]map[string]string{"agentbox-vgpu-driver": {"type": "disk", "source": source, "path": "/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so"}}
	moved := map[string]map[string]string{"agentbox-vgpu-driver": {"type": "disk", "source": "/elsewhere/libvulkan_virtio.so", "path": "/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so"}}
	none := map[string]map[string]string{"worktree": {"type": "disk"}}

	for _, tc := range []struct {
		name    string
		devices map[string]map[string]string
		on      bool
		want    [][]string
	}{
		{"on, not mounted", none, true, [][]string{add}},
		{"on, mounted", mounted, true, nil},
		{"on, moved", moved, true, [][]string{remove, add}},
		{"off, mounted", mounted, false, [][]string{remove}},
		{"off, not mounted", none, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stepArgs(t, vgpuDriverSteps("i", tc.devices, tc.on)); !slices.EqualFunc(got, tc.want, slices.Equal) {
				t.Errorf("steps = %q, want %q", got, tc.want)
			}
		})
	}

	withVenus(t, false)
	if got := stepArgs(t, vgpuDriverSteps("i", none, true)); got != nil {
		t.Errorf("no driver to give, on: %q", got)
	}
	if got := stepArgs(t, vgpuDriverSteps("i", mounted, true)); !slices.EqualFunc(got, [][]string{remove}, slices.Equal) {
		t.Errorf("the driver went: %q, want it taken off", got)
	}
}

// withVenusVM makes this a krunkit VM, whose GPU venus.sh readies, or not.
func withVenusVM(t *testing.T, venus bool) {
	t.Helper()
	old := VenusScript
	t.Cleanup(func() { VenusScript = old })
	VenusScript = filepath.Join(t.TempDir(), "venus.sh")
	if venus {
		_ = os.WriteFile(VenusScript, nil, 0o644)
	}
}

// In a krunkit VM, a render node is a GPU for agents only once venus.sh has
// built the driver that works on a Mac, and the driver goes in beside it.
func TestVenusGPU(t *testing.T) {
	fakeGPU(t, "", "/dev/nul*", false)
	withVenusVM(t, true)
	withVenus(t, false)
	if got := FindVMGPU(); got.Kind != GPUNone || !strings.Contains(got.Reason, "builds one") {
		t.Errorf("render node, no driver: %+v", got)
	}
	dir := withVenus(t, true)
	gpu := FindVMGPU()
	if gpu.Kind != GPURender {
		t.Fatalf("render node and driver: %+v", gpu)
	}
	m := &Manager{}
	want := [][]string{
		{"config", "device", "add", "i", "agentbox-vgpu", "gpu", "gputype=physical", "mode=0666"},
		{"config", "device", "add", "i", "agentbox-vgpu-driver", "disk", "source=" + filepath.Join(dir, "libvulkan_virtio.so"),
			"path=/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so", "readonly=true"},
	}
	if got := stepArgs(t, m.gpuSteps("i", gpu, nil)); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("steps:\n got %q\nwant %q", got, want)
	}

	// No render node: the kernel has no driver yet.
	fakeGPU(t, "", "", false)
	withVenusVM(t, true)
	if got := FindVMGPU(); got.Kind != GPUNone || !strings.Contains(got.Reason, "installs one") {
		t.Errorf("no render node: %+v", got)
	}
}

// ReadyVenus runs venus.sh, saying whether a kernel that didn't boot may be
// tried again, and its error is the last thing venus.sh said.
func TestReadyVenus(t *testing.T) {
	withVenusVM(t, true)
	old := VenusSudo
	VenusSudo = nil
	t.Cleanup(func() { VenusSudo = old })
	_ = os.WriteFile(VenusScript, []byte("echo \"retry=$AGENTBOX_VENUS_RETRY\"\n"), 0o644)
	var out strings.Builder
	if err := ReadyVenus(context.Background(), true, &out); err != nil || out.String() != "retry=1\n" {
		t.Errorf("ReadyVenus = %v, said %q", err, out.String())
	}
	_ = os.WriteFile(VenusScript, []byte("echo working; echo 'GPU: it broke' >&2; echo >&2; exit 1\n"), 0o644)
	if err := ReadyVenus(context.Background(), false, io.Discard); err == nil || err.Error() != "GPU: it broke" {
		t.Errorf("failing: %v", err)
	}
}
