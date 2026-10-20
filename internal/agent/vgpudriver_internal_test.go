package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"agentbox/internal/state"
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

// ApplyVGPUDriver reads the machine's devices and makes the steps.
func TestApplyVGPUDriver(t *testing.T) {
	withVenus(t, true)
	log := filepath.Join(t.TempDir(), "log")
	m := &Manager{Incus: fakeIncus(t, `case "$1" in
  query) echo '{"config":{},"devices":{}}' ;;
  *) echo "$1 $2 $3 $4 $5" >> `+log+` ;;
esac`)}
	if err := m.ApplyVGPUDriver(context.Background(), state.Agent{Instance: "i"}, true); err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(log); string(out) != "config device add i agentbox-vgpu-driver\n" {
		t.Errorf("ran %q", out)
	}
}
