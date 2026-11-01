package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/hostos"
	"agentbox/internal/paths"
)

// fakeGPU points FindVMGPU at dxg and render (device files, or "" for none)
// and at a WSL library directory of its own when lib is true. /dev/null
// stands in for a device: it is a character device anyone can stat.
func fakeGPU(t *testing.T, dxg, render string, lib bool) {
	t.Helper()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	old := []string{dxgPath, wslLib, wslDrivers, RenderNodeGlob, VenusScript, vmGPUStatusFile, VMGPUReasonFile}
	t.Cleanup(func() {
		dxgPath, wslLib, wslDrivers, RenderNodeGlob, VenusScript, vmGPUStatusFile, VMGPUReasonFile = old[0], old[1], old[2], old[3], old[4], old[5], old[6]
	})
	dxgPath, RenderNodeGlob, VenusScript, vmGPUStatusFile, VMGPUReasonFile = missing, missing, missing, missing, missing
	if dxg != "" {
		dxgPath = dxg
	}
	if render != "" {
		RenderNodeGlob = render
	}
	wslLib, wslDrivers = missing, missing
	if lib {
		wslLib, wslDrivers = filepath.Join(dir, "lib"), filepath.Join(dir, "drivers")
		for _, d := range []string{wslLib, wslDrivers} {
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The VM's GPU is WSL's /dev/dxg when it and its libraries are there, a
// render node otherwise, and none with a reason to show when it has neither.
func TestFindVMGPU(t *testing.T) {
	fakeGPU(t, "/dev/null", "/dev/null", true)
	if got := FindVMGPU(); got.Kind != GPUDXG || got.Node != "/dev/null" || got.Reason != "" {
		t.Errorf("WSL with its libraries: %+v", got)
	}

	fakeGPU(t, "/dev/null", "", false)
	if got := FindVMGPU(); got.Kind != GPUNone || !strings.Contains(got.Reason, "wsl --update") {
		t.Errorf("WSL without its libraries: %+v", got)
	}

	fakeGPU(t, "", "/dev/nul*", false)
	if got := FindVMGPU(); got.Kind != GPURender || got.Node != "/dev/null" {
		t.Errorf("a render node: %+v", got)
	}

	// A regular file isn't a device.
	file := filepath.Join(t.TempDir(), "renderD128")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGPU(t, file, file, true)
	if got := FindVMGPU(); got.Kind != GPUNone || got.Reason == "" {
		t.Errorf("no GPU: %+v", got)
	}
}

// The Linux VM gets the host's GPU only as it starts: until it has a render
// node, its supervisor's status says whether the switch can be turned on
// (the host has a GPU) and why the VM has none.
func TestFindVMGPUHost(t *testing.T) {
	if hostos.WSL() {
		t.Skip("WSL has its own reasons")
	}
	for _, tc := range []struct {
		status string
		kind   string
		node   string
		reason string
	}{
		{`{"state":"off","node":"/dev/dri/renderD129","gpu":"amdgpu, 1002:73df"}`, GPUHost, "/dev/dri/renderD129", ""},
		{`{"want":true,"state":"restart","node":"/dev/dri/renderD128","reason":"the VM gets this GPU when it next starts: restart."}`, GPUHost, "/dev/dri/renderD128", "next starts"},
		{`{"want":true,"state":"none","reason":"NVIDIA's proprietary driver can't share its memory."}`, GPUNone, "", "NVIDIA's proprietary driver can't share its memory"},
		{`{"want":true,"state":"on","gpu":"amdgpu"}`, GPUNone, "", "no render node"},
		{`not json`, GPUNone, "", "no GPU to give agents"},
	} {
		fakeGPU(t, "", "", false)
		vmGPUStatusFile = filepath.Join(t.TempDir(), "status.json")
		if err := os.WriteFile(vmGPUStatusFile, []byte(tc.status), 0o644); err != nil {
			t.Fatal(err)
		}
		got := FindVMGPU()
		if got.Kind != tc.kind || got.Node != tc.node || !strings.Contains(got.Reason, tc.reason) || strings.HasSuffix(got.Reason, ".") && tc.kind == GPUNone {
			t.Errorf("%s: %+v", tc.status, got)
		}
	}

	// The host's GPU isn't the VM's: agents get nothing from it.
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	if steps := m.gpuSteps("i", VMGPU{Kind: GPUHost, Node: "/dev/dri/renderD128"}, map[string]map[string]string{vgpuDevice: {"type": "gpu", "gputype": "physical", "mode": "0666"}}); len(stepArgs(t, steps)) != 1 {
		t.Errorf("steps for the host's GPU: %v", stepArgs(t, steps))
	}
}

// A Mac's VM made without krunkit has no GPU, and Settings says why and what
// gives it one, as Setup wrote it; a render node wins over what it wrote.
func TestFindVMGPUMacVM(t *testing.T) {
	if hostos.WSL() || hostos.CloudHypervisorVM() {
		t.Skip("this machine has its own reasons")
	}
	for _, tc := range []struct{ file, reason string }{
		{`{"vmType":"vz","reason":"no 3D in vz. Install krunkit (brew install krunkit), then agentbox vm init."}`, "no 3D in vz. Install krunkit (brew install krunkit), then agentbox vm init"},
		{`{"vmType":"vz"}`, "no GPU to give agents"},
		{`not json`, "no GPU to give agents"},
	} {
		fakeGPU(t, "", "", false)
		VMGPUReasonFile = filepath.Join(t.TempDir(), "vm-gpu.json")
		if err := os.WriteFile(VMGPUReasonFile, []byte(tc.file), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FindVMGPU(); got.Kind != GPUNone || got.Reason != tc.reason && !strings.Contains(got.Reason, tc.reason) {
			t.Errorf("%s: %+v", tc.file, got)
		}
	}

	fakeGPU(t, "", "/dev/nul*", false)
	VMGPUReasonFile = filepath.Join(t.TempDir(), "vm-gpu.json")
	_ = os.WriteFile(VMGPUReasonFile, []byte(`{"vmType":"vz","reason":"no 3D"}`), 0o644)
	if got := FindVMGPU(); got.Kind != GPURender {
		t.Errorf("a render node with a reason file: %+v", got)
	}
}

// Turned on in WSL, an agent gets /dev/dxg, WSL's libraries and drivers
// read-only and the profile that points Mesa at them; the profile file is
// written before the device that mounts it.
func TestGPUStepsWSL(t *testing.T) {
	fakeGPU(t, "/dev/null", "", true)
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	gpu := FindVMGPU()
	got := stepArgs(t, m.gpuSteps("i", gpu, map[string]map[string]string{"worktree": {"type": "disk"}}))
	profile := m.gpuProfileFile()
	want := [][]string{
		{"config", "device", "add", "i", "agentbox-vgpu", "unix-char", "source=/dev/null", "path=/dev/dxg", "mode=0666"},
		{"config", "device", "add", "i", "agentbox-vgpu-wsl-lib", "disk", "source=" + wslLib, "path=/usr/lib/wsl/lib", "readonly=true"},
		{"config", "device", "add", "i", "agentbox-vgpu-wsl-drivers", "disk", "source=" + wslDrivers, "path=/usr/lib/wsl/drivers", "readonly=true"},
		{"config", "device", "add", "i", "agentbox-vgpu-profile", "disk", "source=" + profile, "path=/etc/profile.d/agentbox-gpu.sh", "readonly=true"},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("steps:\n got %q\nwant %q", got, want)
	}
	if b, err := os.ReadFile(profile); err != nil || !strings.Contains(string(b), "LD_LIBRARY_PATH=\"/usr/lib/wsl/lib") || strings.Contains(string(b), "GALLIUM_DRIVER") {
		t.Errorf("profile = %q, %v", b, err)
	}

	// A machine that has them all is left alone.
	have := map[string]map[string]string{}
	for _, d := range gpu.devices(profile) {
		dev := map[string]string{"type": d.kind}
		for _, o := range d.options {
			k, v, _ := strings.Cut(o, "=")
			dev[k] = v
		}
		have[d.name] = dev
	}
	if steps := m.gpuSteps("i", gpu, have); len(steps) != 0 {
		t.Errorf("a machine with its GPU got %d steps", len(steps))
	}

	// One whose device points somewhere else gets it replaced.
	have[vgpuDevice] = map[string]string{"type": "unix-char", "source": "/dev/other", "path": "/dev/dxg", "mode": "0666"}
	got = stepArgs(t, m.gpuSteps("i", gpu, have))
	if len(got) != 2 || got[0][2] != "remove" || got[1][2] != "add" || got[1][4] != vgpuDevice {
		t.Errorf("replacing a stale device: %q", got)
	}
}

// Turned off, or with no GPU in the VM, this file's devices and the Mac's
// driver mount come off and nothing else: not #66's device, which
// oldlimits.go takes off.
func TestGPUStepsOff(t *testing.T) {
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	have := map[string]map[string]string{
		"worktree":             {"type": "disk"},
		vgpuDevice:             {"type": "unix-char"},
		vgpuLibDevice:          {"type": "disk"},
		vgpuProfileDevice:      {"type": "disk"},
		"agentbox-vgpu-driver": {"type": "disk"},
		oldGPUDevice:           {"type": "gpu"},
	}
	got := stepArgs(t, m.gpuSteps("i", VMGPU{}, have))
	var removed []string
	for _, args := range got {
		if args[2] != "remove" {
			t.Errorf("turned off, it ran %q", args)
		}
		removed = append(removed, args[4])
	}
	if want := []string{vgpuDevice, vgpuLibDevice, vgpuProfileDevice, "agentbox-vgpu-driver"}; !slices.Equal(removed, want) {
		t.Errorf("removed %q, want %q", removed, want)
	}
}

// A render node is Incus's own gpu device.
func TestGPUStepsRenderNode(t *testing.T) {
	m := &Manager{}
	got := stepArgs(t, m.gpuSteps("i", VMGPU{Kind: GPURender, Node: "/dev/dri/renderD128"}, nil))
	want := [][]string{{"config", "device", "add", "i", "agentbox-vgpu", "gpu", "gputype=physical", "mode=0666"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("steps = %q, want %q", got, want)
	}
}

// The probe's output, as glxinfo -B and vulkaninfo --summary print it in WSL,
// on a desktop with no GPU, and with no desktop.
func TestParseGPUProbe(t *testing.T) {
	wsl := parseGPUProbe(`## devices
/dev/dxg
## glx
name of display: :99
display: :99  screen: 0
direct rendering: Yes
Extended renderer info (GLX_MESA_query_renderer):
    Vendor: Microsoft Corporation (0xffffffff)
    Device: D3D12 (NVIDIA GeForce RTX 3060) (0xffffffff)
OpenGL vendor string: Microsoft Corporation
OpenGL renderer string: D3D12 (NVIDIA GeForce RTX 3060)
OpenGL core profile version string: 4.6 (Core Profile) Mesa 25.0.7-2
## vulkan
==========
VULKANINFO
==========
Devices:
========
GPU0:
	apiVersion         = 1.4.305
	driverVersion      = 25.0.7
	vendorID           = 0x10005
	deviceType         = PHYSICAL_DEVICE_TYPE_CPU
	deviceName         = llvmpipe (LLVM 19.1.7, 256 bits)
	driverName         = llvmpipe
`)
	if wsl.Device != "/dev/dxg" || !wsl.Display || wsl.Renderer != "D3D12 (NVIDIA GeForce RTX 3060)" || !wsl.Accelerated || wsl.Error != "" {
		t.Errorf("WSL: %+v", wsl)
	}
	if len(wsl.Vulkan) != 1 || wsl.Vulkan[0].Name != "llvmpipe (LLVM 19.1.7, 256 bits)" || wsl.Vulkan[0].Type != "CPU" {
		t.Errorf("WSL's Vulkan: %+v", wsl.Vulkan)
	}

	soft := parseGPUProbe("## devices\n## glx\nOpenGL renderer string: llvmpipe (LLVM 19.1.7, 256 bits)\n## vulkan\nERROR: no devices\n")
	if soft.Device != "" || !soft.Display || soft.Accelerated || len(soft.Vulkan) != 0 {
		t.Errorf("software: %+v", soft)
	}

	none := parseGPUProbe("## devices\n## glx\nError: unable to open display :99\n## vulkan\n")
	if none.Display || !strings.Contains(none.Error, "browser start") {
		t.Errorf("no desktop: %+v", none)
	}
}

// On the Linux VM, whose supervisor names the host GPU it gave the VM,
// agents' Vulkan is pointed at that one (Venus lists every host GPU): a
// profile written before the device that mounts it, named for the GPU.
func TestGPUStepsLinuxVulkan(t *testing.T) {
	if hostos.WSL() {
		t.Skip("WSL")
	}
	fakeGPU(t, "", "/dev/nul*", false)
	vmGPUStatusFile = filepath.Join(t.TempDir(), "status.json")
	_ = os.WriteFile(vmGPUStatusFile, []byte(`{"want":true,"state":"on","vulkan":"1002:73df"}`), 0o644)
	gpu := FindVMGPU()
	if gpu.Kind != GPURender || gpu.Vulkan != "1002:73df" {
		t.Fatalf("%+v", gpu)
	}
	m := &Manager{Paths: paths.Paths{Data: t.TempDir()}}
	got := stepArgs(t, m.gpuSteps("i", gpu, map[string]map[string]string{}))
	source := filepath.Join(m.Paths.Data, "gpu", "vulkan-1002-73df.sh")
	want := [][]string{
		{"config", "device", "add", "i", "agentbox-vgpu", "gpu", "gputype=physical", "mode=0666"},
		{"config", "device", "add", "i", "agentbox-vgpu-profile", "disk", "source=" + source, "path=/etc/profile.d/agentbox-gpu.sh", "readonly=true"},
	}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("got %q", got)
	}
	if b, _ := os.ReadFile(source); !strings.Contains(string(b), "export MESA_VK_DEVICE_SELECT=1002:73df\n") {
		t.Errorf("profile: %q", b)
	}

	// A status that isn't ids (or none, as on a Mac) adds no profile.
	_ = os.WriteFile(vmGPUStatusFile, []byte(`{"vulkan":"1002:73df; rm -rf /"}`), 0o644)
	if gpu := FindVMGPU(); gpu.Vulkan != "" || len(gpu.devices("p")) != 1 {
		t.Errorf("%+v", gpu)
	}
}

// A Cloud Hypervisor VM started by an older AgentBox has no status file: it
// can have the GPU after one restart, so the switch can be turned on. Host
// mode, root installs and nested agents have no VM to restart: they keep the
// plain "no GPU" reason.
func TestFindVMGPUOldVM(t *testing.T) {
	if hostos.WSL() {
		t.Skip("WSL has its own reasons")
	}
	oldMarker := hostos.CHVMarker
	t.Cleanup(func() { hostos.CHVMarker = oldMarker })
	fakeGPU(t, "", "", false)

	marker := filepath.Join(t.TempDir(), "host-guard.nft")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	hostos.CHVMarker = marker
	got := FindVMGPU()
	if got.Kind != GPUHost || got.Node != "" || got.Reason != OldVMReason {
		t.Errorf("an old Linux VM: %+v", got)
	}

	hostos.CHVMarker = filepath.Join(t.TempDir(), "missing")
	t.Setenv(hostos.Env, hostos.Linux) // even a front end's variable is no VM
	got = FindVMGPU()
	if got.Kind != GPUNone || !strings.Contains(got.Reason, "no GPU to give agents") {
		t.Errorf("host mode: %+v", got)
	}
}
