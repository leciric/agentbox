package agent

import (
	"context"
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
//     libd3d12.so and libdxcore.so; there is no DRM render node, so the
//     desktop renders through Mesa's software winsys (drisw) with d3d12 under
//     it, and copies each frame into Xvnc.
//   - A DRM render node (GPURender): an Incus gpu device; the desktop's Xvnc
//     takes it with -rendernode, so its clients get DRI3. The Mac's VM
//     (virtio-gpu) is one; agentbox-vgpu-driver, its darwin-only Mesa, is
//     added beside it by the macOS code.

// Kinds of GPU FindVMGPU can find.
const (
	GPUNone   = ""
	GPUDXG    = "dxg"
	GPURender = "render"
)

// The devices "GPU for agents" adds. vgpuDevice is the GPU itself; the others
// go with /dev/dxg. agentbox-vgpu-driver is the macOS code's, and left alone
// here.
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
)

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
	for _, node := range nodes {
		if fi, err := os.Stat(node); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return VMGPU{Kind: GPURender, Node: node}
		}
	}
	if hostos.WSL() {
		return VMGPU{Reason: "WSL gives AgentBox's distro no GPU (no /dev/dxg): it needs Windows 10 21H2 or later, a current WSL (wsl --update) and a GPU driver with WSL support"}
	}
	return VMGPU{Reason: "AgentBox's VM has no GPU to give agents"}
}

// AgentGPU reads the installation's "GPU for agents" choice, apart from
// whether the VM has a GPU now.
func (m *Manager) AgentGPU(ctx context.Context) (bool, error) {
	return m.Store.Flag(ctx, state.SettingAgentGPU)
}

// wantGPU is the GPU agents should have now: chosen and there, or none.
func (m *Manager) wantGPU(ctx context.Context) (VMGPU, error) {
	gpu := FindVMGPU()
	if gpu.Kind == GPUNone {
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
}

// devices are the Incus devices gpu needs in an agent; profile is the host
// file mounted as gpuProfilePath. mode=0666, as for android.go's /dev/kvm: a
// device added to a running agent doesn't get its group.
func (gpu VMGPU) devices(profile string) []gpuDevice {
	switch gpu.Kind {
	case GPUDXG:
		ds := []gpuDevice{
			{vgpuDevice, "unix-char", []string{"source=" + gpu.Node, "path=/dev/dxg", "mode=0666"}},
			{vgpuLibDevice, "disk", []string{"source=" + wslLib, "path=/usr/lib/wsl/lib", "readonly=true"}},
		}
		if _, err := os.Stat(wslDrivers); err == nil {
			ds = append(ds, gpuDevice{vgpuDriversDevice, "disk", []string{"source=" + wslDrivers, "path=/usr/lib/wsl/drivers", "readonly=true"}})
		}
		return append(ds, gpuDevice{vgpuProfileDevice, "disk", []string{"source=" + profile, "path=" + gpuProfilePath, "readonly=true"}})
	case GPURender:
		// Incus's own gpu device passes the GPU's card and render nodes.
		return []gpuDevice{{vgpuDevice, "gpu", []string{"gputype=physical", "mode=0666"}}}
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

// writeGPUProfile writes gpuProfileFile, before a device mounts it.
func (m *Manager) writeGPUProfile() error {
	path := m.gpuProfileFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(path); err == nil && string(b) == gpuProfile {
		return nil
	}
	return os.WriteFile(path, []byte(gpuProfile), 0o644)
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
			if d.name == vgpuProfileDevice {
				if err := m.writeGPUProfile(); err != nil {
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
	return steps
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
