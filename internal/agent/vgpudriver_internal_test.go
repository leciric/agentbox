package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/paths"
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

// The driver and venusChromium go in beside a Mac's render node when the VM
// built a driver, are left alone when they're in already, are mounted again
// when the driver moved, and come off when the GPU goes or there's no driver
// any more.
func TestVenusDeviceSteps(t *testing.T) {
	dir := withVenus(t, true)
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	chromium := filepath.Join(m.Paths.Data, "gpu", "venus-chromium.sh")
	source := filepath.Join(dir, "libvulkan_virtio.so")
	driverPath := "/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so"
	render := VMGPU{Kind: GPURender, Node: "/dev/dri/renderD128"}
	gpu := map[string]string{"type": "gpu", "gputype": "physical", "mode": "0666"}
	driver := map[string]string{"type": "disk", "source": source, "path": driverPath, "readonly": "true"}
	flags := map[string]string{"type": "disk", "source": chromium, "path": "/etc/chromium.d/agentbox-gpu", "readonly": "true"}
	addDriver := []string{"config", "device", "add", "i", "agentbox-vgpu-driver", "disk", "source=" + source, "path=" + driverPath, "readonly=true"}
	addFlags := []string{"config", "device", "add", "i", "agentbox-vgpu-chromium", "disk", "source=" + chromium, "path=/etc/chromium.d/agentbox-gpu", "readonly=true"}
	remove := func(name string) []string { return []string{"config", "device", "remove", "i", name} }
	moved := map[string]string{"type": "disk", "source": "/elsewhere/libvulkan_virtio.so", "path": driverPath, "readonly": "true"}

	for _, tc := range []struct {
		name    string
		want    VMGPU
		devices map[string]map[string]string
		steps   [][]string
	}{
		{"render node, none in", render, map[string]map[string]string{"agentbox-vgpu": gpu}, [][]string{addDriver, addFlags}},
		{"render node, both in", render, map[string]map[string]string{"agentbox-vgpu": gpu, "agentbox-vgpu-driver": driver, "agentbox-vgpu-chromium": flags}, nil},
		{"render node, driver moved", render, map[string]map[string]string{"agentbox-vgpu": gpu, "agentbox-vgpu-driver": moved, "agentbox-vgpu-chromium": flags},
			[][]string{remove("agentbox-vgpu-driver"), addDriver}},
		{"GPU off", VMGPU{}, map[string]map[string]string{"agentbox-vgpu-driver": driver, "agentbox-vgpu-chromium": flags},
			[][]string{remove("agentbox-vgpu-driver"), remove("agentbox-vgpu-chromium")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stepArgs(t, m.gpuSteps("i", tc.want, tc.devices)); !slices.EqualFunc(got, tc.steps, slices.Equal) {
				t.Errorf("steps:\n got %q\nwant %q", got, tc.steps)
			}
		})
	}
	if b, err := os.ReadFile(chromium); err != nil || string(b) != venusChromium {
		t.Errorf("venus-chromium.sh wasn't written before it was mounted: %v", err)
	}

	withVenus(t, false)
	both := map[string]map[string]string{"agentbox-vgpu": gpu, "agentbox-vgpu-driver": driver, "agentbox-vgpu-chromium": flags}
	if got := stepArgs(t, m.gpuSteps("i", render, both)); !slices.EqualFunc(got, [][]string{remove("agentbox-vgpu-driver"), remove("agentbox-vgpu-chromium")}, slices.Equal) {
		t.Errorf("the driver went: %q, want both taken off", got)
	}
}

// Every Chromium gets the flags that run WebGL on the Mac's GPU, from
// /etc/chromium.d, only in an agent the VM's Venus driver is mounted into, and
// only one built with venus/0005; elsewhere it keeps its own choice.
func TestVenusChromiumFlags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	patched := filepath.Join(dir, "patched", "libvulkan_virtio.so")
	old := filepath.Join(dir, "old", "libvulkan_virtio.so")
	for path, body := range map[string]string{patched: "\x7fELF..query feedback off: the renderer has no dma-buf\x00..", old: "\x7fELF..software wsi: the renderer has no dma-buf\x00.."} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "agentbox-gpu")
	if err := os.WriteFile(script, []byte(venusChromium), 0o644); err != nil {
		t.Fatal(err)
	}
	mount := func(at string) string {
		return "388 369 254:1 /usr/local/lib/agentbox/venus/libvulkan_virtio.so " + at + " ro,relatime - ext4 /dev/vda1 rw\n"
	}
	other := "29 1 0:26 / /proc rw,nosuid - proc proc rw\n"
	for _, tc := range []struct{ name, mounts, want string }{
		{"patched driver", mount(patched) + other, "--enable-gpu-rasterization --use-angle=vulkan --ignore-gpu-blocklist"},
		{"driver from before 0005", mount(old) + other, "--enable-gpu-rasterization"},
		{"no driver", other, "--enable-gpu-rasterization"},
	} {
		mountinfo := filepath.Join(t.TempDir(), "mountinfo")
		if err := os.WriteFile(mountinfo, []byte(tc.mounts), 0o644); err != nil {
			t.Fatal(err)
		}
		// As Debian's wrapper does: flags from the files before, then each one sourced.
		cmd := exec.Command("bash", "-c", `CHROMIUM_FLAGS="--enable-gpu-rasterization"; . "$1"; echo "$CHROMIUM_FLAGS $agentbox_venus"`, "sh", script)
		cmd.Env = append(os.Environ(), "mountinfo="+mountinfo)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", tc.name, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Errorf("%s: CHROMIUM_FLAGS = %q, want %q", tc.name, got, tc.want)
		}
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
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	want := [][]string{
		{"config", "device", "add", "i", "agentbox-vgpu", "gpu", "gputype=physical", "mode=0666"},
		{"config", "device", "add", "i", "agentbox-vgpu-driver", "disk", "source=" + filepath.Join(dir, "libvulkan_virtio.so"),
			"path=/usr/lib/aarch64-linux-gnu/libvulkan_virtio.so", "readonly=true"},
		{"config", "device", "add", "i", "agentbox-vgpu-chromium", "disk", "source=" + filepath.Join(m.Paths.Data, "gpu", "venus-chromium.sh"),
			"path=/etc/chromium.d/agentbox-gpu", "readonly=true"},
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
