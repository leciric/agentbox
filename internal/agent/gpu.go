package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"agentbox/internal/hostos"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// "GPU for agents" (state.SettingAgentGPU) gives every agent the GPU of
// AgentBox's VM, when the VM has one. It replaces #66's host-mode setting,
// whose device and key oldlimits.go still takes off machines made by then:
// this one keeps its devices under other names (agentbox-vgpu*).
//
// What an agent gets depends on the kind of GPU the VM has, and nothing else
// does: the desktop (browser.sh) and the probe (GPUReport) look at what is
// in the machine, not at which OS is under the VM.
//
//   - WSL2 (GPUDXG): /dev/dxg, the paravirtual Direct3D 12 device, with the
//     Windows driver's user-mode half in /usr/lib/wsl/lib and /usr/lib/wsl/drivers,
//     both mounted read-only. Mesa's d3d12 gallium driver (in Debian's
//     libgl1-mesa-dri) renders OpenGL on it once LD_LIBRARY_PATH finds
//     libd3d12.so and libdxcore.so; there is no DRM render node, so a program
//     renders through Mesa's software winsys (drisw) with d3d12 under it,
//     reading each frame back. The desktop and its Chromium don't: they kept
//     Windows' GPU busy enough to reset its driver (TDR), so browser.sh keeps
//     them on llvmpipe and SwiftShader, and only what agents run uses d3d12.
//   - A DRM render node (GPURender): an Incus gpu device; the desktop's Xvnc
//     takes it with -rendernode, so its clients get DRI3. The Mac's VM
//     (virtio-gpu) is one; agentbox-vgpu-driver, the Venus driver it built
//     (vgpudriver.go), is added beside it. So is the Linux VM's (Cloud
//     Hypervisor and crosvm), which it gets only as it starts: until then
//     its supervisor says what the host has (vmGPUStatusFile), GPUHost.

// Kinds of GPU FindVMGPU can find.
const (
	GPUNone   = ""
	GPUDXG    = "dxg"
	GPURender = "render"
	// GPUHost is the Linux host's GPU, which AgentBox's VM gets when it
	// next starts with "GPU for agents" on: the switch can be turned on,
	// and agents get nothing until then.
	GPUHost = "host"
)

// OldVMReason is VMGPU.Reason of a GPUHost with no Node: the Linux VM runs
// from an older AgentBox, which wrote no status (vmGPUStatusFile).
const OldVMReason = "started by an older AgentBox"

// The devices "GPU for agents" adds. vgpuDevice is the GPU itself; the others
// go with /dev/dxg. agentbox-vgpu-driver is the macOS code's (vgpudriver.go),
// which gpuSteps adds beside a render node.
const (
	vgpuDevice        = "agentbox-vgpu"
	vgpuLibDevice     = "agentbox-vgpu-wsl-lib"
	vgpuDriversDevice = "agentbox-vgpu-wsl-drivers"
	vgpuProfileDevice = "agentbox-vgpu-profile"
)

// vgpuDevices are every device this file adds, to take off when it is off.
var vgpuDevices = []string{vgpuDevice, vgpuLibDevice, vgpuDriversDevice, vgpuProfileDevice}

// Where WSL puts the GPU and its driver, in its distro: variables for tests,
// which point them at files of their own. wslLib holds libd3d12.so and
// libdxcore.so; wslDrivers the GPU vendor's user-mode driver, which
// libdxcore loads from there.
var (
	dxgPath        = "/dev/dxg"
	wslLib         = "/usr/lib/wsl/lib"
	wslDrivers     = "/usr/lib/wsl/drivers"
	renderNodeGlob = "/dev/dri/renderD*"
	// vmGPUStatusFile is where the Linux VM's supervisor says what GPU the
	// host has for it, and why the VM has none (chv.GPUStatus, written to
	// chv's guestGPUFile).
	vmGPUStatusFile = "/run/agentbox-vm-gpu.json"
)

// VMGPUReasonFile is where Setup on a Mac says why a VM made without krunkit
// (vz, or Lima's qemu) has no GPU and what gives it one, for Settings: the
// daemon can't tell its VM's type from inside it. {"vmType": …, "reason": …},
// written by hostvm's writeGPUReason.
var VMGPUReasonFile = "/usr/local/share/agentbox/vm-gpu.json"

// gpuProfilePath is where the agent's login shells read gpuProfile from.
const gpuProfilePath = "/etc/profile.d/agentbox-gpu.sh"

// gpuProfile points the agent's programs at WSL's driver. A file mounted into
// /etc/profile.d rather than instance environment keys: commands reach an
// agent through `runuser -l`, which starts from a clean environment and reads
// the profile. It checks for the device, so a machine whose device was taken
// off while it ran sets nothing in its next shell.
//
// No GALLIUM_DRIVER: Mesa's software winsys tries d3d12 before llvmpipe on its
// own (sw_helper.h), and falls back when d3d12 can't start, which a set
// GALLIUM_DRIVER stops it doing: every OpenGL program then fails instead.
const gpuProfile = `# Written by AgentBox: "GPU for agents" (internal/agent/gpu.go).
# WSL's GPU: Mesa's d3d12 driver, with the Windows driver's libraries.
if [ -c /dev/dxg ] && [ -d /usr/lib/wsl/lib ]; then
  case ":${LD_LIBRARY_PATH:-}:" in
  *:/usr/lib/wsl/lib:*) ;;
  *) export LD_LIBRARY_PATH="/usr/lib/wsl/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" ;;
  esac
fi
`

// VMGPU is the GPU AgentBox's VM has to give agents.
type VMGPU struct {
	Kind string // GPUDXG, GPURender, or GPUNone
	Node string // /dev/dxg or the render node; "" for GPUNone
	// Reason says why there is none, for Settings to show beside the switch.
	Reason string
	// Vulkan is the host GPU's PCI vendor:device ids, for the Linux VM's
	// render node: Venus lists only this GPU of the host's (the VM's
	// virglrenderer is patched to), and agents' Vulkan is pointed at it before
	// the guest's llvmpipe (vulkanProfile). "" elsewhere.
	Vulkan string
}

// FindVMGPU looks for the VM's GPU: a device file and, on WSL, its driver's
// libraries, not a working driver stack, which only an agent can tell
// (GPUReport). Checked at each read: a stat or two.
func FindVMGPU() VMGPU {
	if fi, err := os.Stat(dxgPath); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		if _, err := os.Stat(wslLib); err != nil {
			return VMGPU{Reason: "WSL has /dev/dxg but no driver libraries in " + wslLib + ": update WSL (wsl --update) and your GPU's driver, then wsl --shutdown"}
		}
		return VMGPU{Kind: GPUDXG, Node: dxgPath}
	}
	nodes, _ := filepath.Glob(renderNodeGlob)
	sort.Strings(nodes)
	venus := VenusVM()
	for _, node := range nodes {
		if fi, err := os.Stat(node); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			// Debian's own Venus driver can't work with the Mac's pages:
			// until venus.sh has built one that does, agents get none.
			if source, _ := venusDriver(); venus && source == "" {
				return VMGPU{Reason: "the VM's GPU has no driver that works on a Mac yet: turning GPU for agents on builds one"}
			}
			return VMGPU{Kind: GPURender, Node: node, Vulkan: vmVulkanDevice()}
		}
	}
	if venus {
		return VMGPU{Reason: "the VM's kernel has no GPU driver yet: turning GPU for agents on installs one"}
	}
	if hostos.WSL() {
		return VMGPU{Reason: "WSL gives AgentBox's distro no GPU (no /dev/dxg): it needs Windows 10 21H2 or later, a current WSL (wsl --update) and a GPU driver with WSL support"}
	}
	if gpu, ok := hostGPU(); ok {
		return gpu
	}
	if reason := macVMReason(); reason != "" {
		return VMGPU{Reason: reason}
	}
	return VMGPU{Reason: "AgentBox's VM has no GPU to give agents"}
}

// macVMReason is why a Mac's VM has no GPU, as Setup wrote it
// (VMGPUReasonFile), or "".
func macVMReason() string {
	b, err := os.ReadFile(VMGPUReasonFile)
	if err != nil {
		return ""
	}
	var st struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(b, &st) != nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(st.Reason), ".")
}

// pciIDs is a PCI vendor:device pair, as MESA_VK_DEVICE_SELECT takes it.
var pciIDs = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{4}$`)

// vmVulkanDevice is the host GPU the Linux VM's supervisor gave the VM
// (chv.GPUStatus.Vulkan), or "".
func vmVulkanDevice() string {
	b, err := os.ReadFile(vmGPUStatusFile)
	if err != nil {
		return ""
	}
	var st struct {
		Vulkan string `json:"vulkan"`
	}
	if json.Unmarshal(b, &st) != nil || !pciIDs.MatchString(st.Vulkan) {
		return ""
	}
	return st.Vulkan
}

// vulkanProfile points an agent's Vulkan at the VM's GPU, before the guest's
// llvmpipe and any other GPU an older Cloud Hypervisor build's Venus lists,
// with Mesa's device-select layer, which is harmless in the guest (on the
// host, in Venus's render server, it breaks Venus).
func vulkanProfile(ids string) string {
	return `# Written by AgentBox: "GPU for agents" (internal/agent/gpu.go).
# Vulkan uses the GPU AgentBox's VM was given, not llvmpipe.
export MESA_VK_DEVICE_SELECT=` + ids + `
`
}

// hostGPU is what the Linux VM's supervisor says of the host's GPU when the
// VM has no render node; false when it said nothing (another VM, or an
// older AgentBox). Its states and fields are chv.GPUStatus's.
func hostGPU() (VMGPU, bool) {
	b, err := os.ReadFile(vmGPUStatusFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && hostos.CloudHypervisorVM() {
			// The Cloud Hypervisor VM was started by an AgentBox whose
			// supervisor knew no GPU: it learns of the host's only when it
			// restarts, which is also when it gets the GPU, so the switch
			// can go on now and one restart does both.
			return VMGPU{Kind: GPUHost, Reason: OldVMReason}, true
		}
		return VMGPU{}, false
	}
	var st struct {
		State  string `json:"state"`
		Node   string `json:"node"`
		GPU    string `json:"gpu"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(b, &st) != nil {
		return VMGPU{}, false
	}
	switch st.State {
	case "off", "restart":
		return VMGPU{Kind: GPUHost, Node: st.Node, Reason: st.Reason}, true
	case "on":
		// It has the device, but the kernel made no render node of it.
		return VMGPU{Reason: "AgentBox's VM has the GPU (" + st.GPU + "), but its kernel made no render node of it: see agentbox vm gpu"}, true
	}
	return VMGPU{Reason: strings.TrimSuffix(cmp.Or(st.Reason, "AgentBox's VM has no GPU to give agents"), ".")}, true
}

// AgentGPU reads the installation's "GPU for agents" choice, apart from
// whether the VM has a GPU now.
func (m *Manager) AgentGPU(ctx context.Context) (bool, error) {
	return m.Store.Flag(ctx, state.SettingAgentGPU)
}

// wantGPU is the GPU agents should have now: chosen and there, or none.
func (m *Manager) wantGPU(ctx context.Context) (VMGPU, error) {
	gpu := FindVMGPU()
	if gpu.Kind != GPUDXG && gpu.Kind != GPURender {
		return VMGPU{}, nil
	}
	on, err := m.AgentGPU(ctx)
	if err != nil || !on {
		return VMGPU{}, err
	}
	return gpu, nil
}

// gpuDevice is one Incus device "GPU for agents" adds.
type gpuDevice struct {
	name, kind string
	options    []string // key=value
	// content, for a profile, is what its source file is written with
	// before the device mounts it.
	content string
}

// devices are the Incus devices gpu needs in an agent; profile is the host
// file mounted as gpuProfilePath. mode=0666, as for android.go's /dev/kvm: a
// device added to a running agent doesn't get its group.
func (gpu VMGPU) devices(profile string) []gpuDevice {
	switch gpu.Kind {
	case GPUDXG:
		ds := []gpuDevice{
			{vgpuDevice, "unix-char", []string{"source=" + gpu.Node, "path=/dev/dxg", "mode=0666"}, ""},
			{vgpuLibDevice, "disk", []string{"source=" + wslLib, "path=/usr/lib/wsl/lib", "readonly=true"}, ""},
		}
		if _, err := os.Stat(wslDrivers); err == nil {
			ds = append(ds, gpuDevice{vgpuDriversDevice, "disk", []string{"source=" + wslDrivers, "path=/usr/lib/wsl/drivers", "readonly=true"}, ""})
		}
		return append(ds, gpuDevice{vgpuProfileDevice, "disk", []string{"source=" + profile, "path=" + gpuProfilePath, "readonly=true"}, gpuProfile})
	case GPURender:
		// Incus's own gpu device passes the GPU's card and render nodes.
		ds := []gpuDevice{{name: vgpuDevice, kind: "gpu", options: []string{"gputype=physical", "mode=0666"}}}
		if gpu.Vulkan != "" {
			// Named for the GPU, so another GPU replaces the device.
			source := filepath.Join(filepath.Dir(profile), "vulkan-"+strings.ReplaceAll(gpu.Vulkan, ":", "-")+".sh")
			ds = append(ds, gpuDevice{vgpuProfileDevice, "disk", []string{"source=" + source, "path=" + gpuProfilePath, "readonly=true"}, vulkanProfile(gpu.Vulkan)})
		}
		return ds
	}
	return nil
}

// matches says whether have, an instance's device, is d.
func (d gpuDevice) matches(have map[string]string) bool {
	if have == nil || have["type"] != d.kind {
		return false
	}
	for _, opt := range d.options {
		k, v, _ := strings.Cut(opt, "=")
		if have[k] != v {
			return false
		}
	}
	return true
}

// gpuProfileFile is the host's copy of gpuProfile, mounted into agents.
func (m *Manager) gpuProfileFile() string {
	return filepath.Join(m.Paths.Data, "gpu", "profile.sh")
}

// writeProfile writes a profile a device mounts, before it does.
func writeProfile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(path); err == nil && string(b) == content {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// gpuSteps are the changes that give instance want's GPU, from devices, its
// own: devices added or replaced, and the ones this file adds and want
// doesn't need taken off. Incus adds and removes them live; programs started
// before see the change only when they start again, and the desktop's Xvnc
// takes a render node when it starts.
func (m *Manager) gpuSteps(instance string, want VMGPU, devices map[string]map[string]string) []incusStep {
	wanted := want.devices(m.gpuProfileFile())
	var steps []incusStep
	keep := map[string]bool{}
	for _, d := range wanted {
		keep[d.name] = true
		have, ok := devices[d.name]
		if d.matches(have) {
			continue
		}
		if ok {
			steps = append(steps, func(ctx context.Context, c incus.Client) error { return c.RemoveDevice(ctx, instance, d.name) })
		}
		steps = append(steps, func(ctx context.Context, c incus.Client) error {
			if d.content != "" {
				if err := writeProfile(strings.TrimPrefix(d.options[0], "source="), d.content); err != nil {
					return err
				}
			}
			return c.AddDevice(ctx, instance, d.name, d.kind, d.options...)
		})
	}
	for _, name := range vgpuDevices {
		if _, ok := devices[name]; ok && !keep[name] {
			steps = append(steps, func(ctx context.Context, c incus.Client) error { return c.RemoveDevice(ctx, instance, name) })
		}
	}
	// A Mac's VM has its own Venus driver to go with its render node.
	return append(steps, vgpuDriverSteps(instance, devices, want.Kind == GPURender)...)
}

// settleGPU gives one instance the GPU it should have, as it starts or is
// restored: the VM, or the setting, may have changed while it was stopped.
func (m *Manager) settleGPU(ctx context.Context, instance string) error {
	want, err := m.wantGPU(ctx)
	if err != nil {
		return err
	}
	devices, err := m.Incus.Devices(ctx, instance)
	if err != nil {
		return err
	}
	for _, step := range m.gpuSteps(instance, want, devices) {
		if err := step(ctx, m.Incus); err != nil {
			return err
		}
	}
	return nil
}

// ApplyGPU gives every agent the GPU "GPU for agents" says it should have,
// as the daemon starts and when the setting changes, and reports how many
// machines it changed.
func (m *Manager) ApplyGPU(ctx context.Context) (changed int, err error) {
	want, err := m.wantGPU(ctx)
	if err != nil {
		return 0, err
	}
	return m.eachAgentMachine(ctx, func(inst incus.Instance) []incusStep {
		return m.gpuSteps(inst.Name, want, inst.Devices)
	})
}

// GPUReport is what an agent's desktop renders on, as its own tools see it.
type GPUReport struct {
	// Device is the GPU device in the agent, "" for none.
	Device string
	// Display is whether the desktop answered; the rest is empty when not.
	Display bool
	// Renderer is glxinfo's OpenGL renderer string on the desktop, and
	// Accelerated whether it is a GPU's rather than Mesa's software.
	Renderer    string
	Accelerated bool
	// Vulkan are vulkaninfo's devices: name and type.
	Vulkan []VulkanDevice
	// Error is what went wrong asking, when something did.
	Error string
}

// VulkanDevice is one of vulkaninfo's physical devices.
type VulkanDevice struct {
	Name string
	Type string // vulkaninfo's deviceType, less PHYSICAL_DEVICE_TYPE_
}

// gpuProbeScript asks the agent's own tools, on its desktop, in sections
// parseGPUProbe reads.
const gpuProbeScript = `echo '## devices'; ls -1 /dev/dxg /dev/dri/renderD* 2>/dev/null
echo '## glx'; DISPLAY=:99 timeout 20 glxinfo -B 2>&1
echo '## vulkan'; DISPLAY=:99 timeout 20 vulkaninfo --summary 2>&1
true`

var (
	glxRenderer  = regexp.MustCompile(`(?m)^\s*OpenGL renderer string:\s*(.+?)\s*$`)
	vkDevice     = regexp.MustCompile(`^GPU\d+:\s*$`)
	vkDeviceName = regexp.MustCompile(`^\s*deviceName\s*=\s*(.+?)\s*$`)
	vkDeviceType = regexp.MustCompile(`^\s*deviceType\s*=\s*(?:PHYSICAL_DEVICE_TYPE_)?(\S+)`)
)

// softwareRenderers are Mesa's renderers that run on the CPU.
var softwareRenderers = []string{"llvmpipe", "softpipe", "swrast", "lavapipe"}

// software says whether a renderer or Vulkan device name is Mesa's own CPU one.
func software(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range softwareRenderers {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// parseGPUProbe reads gpuProbeScript's output.
func parseGPUProbe(out string) GPUReport {
	sections := map[string]string{}
	var name string
	for line := range strings.SplitSeq(out, "\n") {
		if s, ok := strings.CutPrefix(line, "## "); ok {
			name = strings.TrimSpace(s)
			continue
		}
		sections[name] += line + "\n"
	}
	var r GPUReport
	for line := range strings.SplitSeq(sections["devices"], "\n") {
		if line = strings.TrimSpace(line); line != "" && r.Device == "" {
			r.Device = line
		}
	}
	glx := sections["glx"]
	if m := glxRenderer.FindStringSubmatch(glx); m != nil {
		r.Display = true
		r.Renderer = m[1]
		r.Accelerated = !software(m[1])
	} else if msg := strings.TrimSpace(glx); strings.Contains(msg, "unable to open display") {
		r.Error = "the agent's desktop isn't running: start it with agentbox browser start"
	} else if msg != "" {
		r.Error = "glxinfo: " + firstLineOf(msg)
	}
	// --summary lists each device as a "GPU0:" block of key = value lines.
	for line := range strings.SplitSeq(sections["vulkan"], "\n") {
		switch {
		case vkDevice.MatchString(line):
			r.Vulkan = append(r.Vulkan, VulkanDevice{})
		case len(r.Vulkan) == 0:
		case vkDeviceName.MatchString(line):
			r.Vulkan[len(r.Vulkan)-1].Name = vkDeviceName.FindStringSubmatch(line)[1]
		case vkDeviceType.MatchString(line):
			r.Vulkan[len(r.Vulkan)-1].Type = vkDeviceType.FindStringSubmatch(line)[1]
		}
	}
	r.Vulkan = slices.DeleteFunc(r.Vulkan, func(d VulkanDevice) bool { return d.Name == "" })
	return r
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// GPUReport asks agent a what its desktop renders on: glxinfo's renderer and
// vulkaninfo's devices, run as the agent's user, so with its profile.
func (m *Manager) GPUReport(ctx context.Context, a state.Agent) (GPUReport, error) {
	out, err := m.agentShell(ctx, a, gpuProbeScript)
	if err != nil && out == "" {
		var exit *incus.ExitError
		if errors.As(err, &exit) {
			return GPUReport{}, fmt.Errorf("couldn't ask the agent about its GPU: %w", err)
		}
		return GPUReport{}, err
	}
	return parseGPUProbe(out), nil
}
