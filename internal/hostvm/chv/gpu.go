package chv

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GPU for agents on Linux. The VM gets the host's GPU as a virtio-gpu device
// that Cloud Hypervisor (with Spectrum OS's patchset, chvBundle) fronts over
// vhost-user and crosvm's GPU device serves, beside it, as the user: the
// guest's OpenGL goes to virglrenderer's virgl contexts and its Vulkan to
// Venus, both on the host's render node. Agents get the VM's render node from
// the daemon when "GPU for agents" is on (package agent), as on a Mac.
//
// The switch is the daemon's (state.SettingAgentGPU), in the VM; the device
// can only be given as the VM starts. So the supervisor asks the daemon for the
// switch while the VM runs (gpuLoop) and keeps it in the VM's gpu.json, and the
// next start reads it there: crosvm and --gpu only when it's on and the host
// has a render node crosvm can use. Anything else, the VM boots as it would
// without, and gpu.json and the guest's guestGPUFile say why.
//
// The VM's Debian cloud kernel has no DRM at all, so a VM that is to have the
// GPU needs Debian's generic kernel: installed beside it while the VM runs,
// booted once (grub-reboot) at the next start, and made the default only once
// that boot has come up (kernelScript, defaultKernelScript). A boot that
// doesn't come up within kernelTrialTimeout is reset, and GRUB then boots the
// cloud kernel again. The cloud kernel is never removed.

// GPU states, in GPUStatus.State.
const (
	// GPUOn: the VM has the GPU.
	GPUOn = "on"
	// GPUOff: the host has a GPU the VM can have; "GPU for agents" is off.
	GPUOff = "off"
	// GPURestart: the VM gets the GPU at its next start.
	GPURestart = "restart"
	// GPUNone: the VM can't have the GPU; Reason says why.
	GPUNone = "none"
)

// GPUStatus is the VM's GPU as the supervisor saw it last, kept in the VM's
// gpu.json on the host for `agentbox vm gpu`, and in guestGPUFile for the
// daemon, which shows it in Settings.
type GPUStatus struct {
	// Want is "GPU for agents", as the daemon said it last.
	Want bool `json:"want"`
	// State is GPUOn, GPUOff, GPURestart or GPUNone.
	State string `json:"state"`
	// Node is the host's render node the VM gets, and GPU what it is.
	Node string `json:"node,omitempty"`
	GPU  string `json:"gpu,omitempty"`
	// Vulkan is the GPU's PCI vendor:device ids, when the VM has it: Venus
	// lists every GPU of the host, and agents' Vulkan picks this one
	// (MESA_VK_DEVICE_SELECT, package agent).
	Vulkan string `json:"vulkan,omitempty"`
	// Reason says what's wrong, or what the user has to do, in a sentence.
	Reason string `json:"reason,omitempty"`
	// Trial is the generic kernel the VM boots once at its next start, to
	// be made its default once it comes up; KernelFailed one that didn't.
	Trial        string `json:"trial,omitempty"`
	KernelFailed string `json:"kernelFailed,omitempty"`
}

// GPUFile is where a VM's GPUStatus is kept on the host.
func (l Layout) GPUFile() string { return filepath.Join(l.Dir(), "gpu.json") }

// GPUSocket is crosvm's GPU device's vhost-user socket.
func (l Layout) GPUSocket() string { return filepath.Join(l.Run(), "gpu.sock") }

// ReadGPUStatus reads a VM's GPUStatus; the zero one when there's none.
func ReadGPUStatus(l Layout) GPUStatus {
	var st GPUStatus
	if b, err := os.ReadFile(l.GPUFile()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func writeGPUStatus(l Layout, st GPUStatus) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.GPUFile() + ".new"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.GPUFile())
}

// renderNode is one of the host's DRM render nodes.
type renderNode struct {
	Path    string // /dev/dri/renderD128
	Driver  string // its kernel driver: amdgpu, i915, xe, nouveau, nvidia, ...
	Vendor  string // PCI vendor and device ids, hex: 1002, 73df
	Device  string
	Slot    string // PCI slot: 0000:03:00.0
	BootVGA bool   // the firmware's display
	VRAM    int64  // amdgpu's VRAM, bytes; 0 when unknown
}

// name says what it is, for messages.
func (n renderNode) name() string {
	ids := ""
	if n.Vendor != "" {
		ids = ", " + n.Vendor + ":" + n.Device
	}
	return cmp.Or(n.Driver, "unknown driver") + ids
}

// integrated guesses whether it's a GPU on the CPU, which a machine with a
// discrete one should leave alone: Intel's at 00:02.0, and an AMD APU's,
// which has little memory of its own.
func (n renderNode) integrated() bool {
	switch n.Driver {
	case "i915", "xe":
		return strings.HasSuffix(n.Slot, ":00:02.0")
	case "amdgpu":
		return n.VRAM > 0 && n.VRAM < 2<<30
	}
	return false
}

// sysDRM is where the kernel lists DRM devices: a variable for tests.
var sysDRM = "/sys/class/drm"

// findRenderNodes lists the host's render nodes from sysfs, with what their
// driver and PCI ids are.
func findRenderNodes() []renderNode {
	dirs, _ := filepath.Glob(filepath.Join(sysDRM, "renderD*"))
	var nodes []renderNode
	for _, dir := range dirs {
		dev := filepath.Join(dir, "device")
		read := func(name string) string {
			b, _ := os.ReadFile(filepath.Join(dev, name))
			return strings.TrimSpace(string(b))
		}
		n := renderNode{
			Path:    "/dev/dri/" + filepath.Base(dir),
			Vendor:  strings.TrimPrefix(read("vendor"), "0x"),
			Device:  strings.TrimPrefix(read("device"), "0x"),
			BootVGA: read("boot_vga") == "1",
		}
		if drv, err := os.Readlink(filepath.Join(dev, "driver")); err == nil {
			n.Driver = filepath.Base(drv)
		}
		if real, err := filepath.EvalSymlinks(dev); err == nil {
			n.Slot = filepath.Base(real)
		}
		n.VRAM, _ = strconv.ParseInt(read("mem_info_vram_total"), 10, 64)
		nodes = append(nodes, n)
	}
	return nodes
}

// renderNodeNumber orders render nodes as renderD128, renderD129, ...
func renderNodeNumber(path string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "renderD"))
	return n
}

// pickRenderNode chooses the GPU the VM gets: the one named by want (a path
// or renderD129), or else a discrete one before one on the CPU, the
// firmware's display first, then the lowest number. virglrenderer would take
// whichever readdir returns first, which on a machine with two is often the
// integrated one: crosvm is told which (gpuParams).
func pickRenderNode(nodes []renderNode, want string) (renderNode, error) {
	if want != "" {
		for _, n := range nodes {
			if n.Path == want || filepath.Base(n.Path) == want {
				return n, nil
			}
		}
		return renderNode{}, fmt.Errorf("AGENTBOX_VM_RENDER_NODE names %s, which isn't one of this machine's render nodes", want)
	}
	if len(nodes) == 0 {
		return renderNode{}, errors.New("this machine has no GPU render node (/dev/dri/renderD*)")
	}
	nodes = slices.Clone(nodes)
	rank := func(n renderNode) int {
		r := 0
		if n.integrated() {
			r += 2
		}
		if !n.BootVGA {
			r++
		}
		return r
	}
	slices.SortStableFunc(nodes, func(a, b renderNode) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(renderNodeNumber(a.Path), renderNodeNumber(b.Path)))
	})
	return nodes[0], nil
}

// gpuPlan is whether the VM gets the GPU as it starts, and on which node.
type gpuPlan struct {
	node   renderNode
	reason string // why it doesn't, when it doesn't; "" when it does
}

func (p gpuPlan) ok() bool { return p.reason == "" }

// gpuOff is a gpuPlan's reason when the switch is off.
const gpuOff = `"GPU for agents" is off`

// planGPU decides whether the VM is to start with the GPU: wanted, and on a
// render node this user can open with a driver Venus works on. open is how
// the node is checked (os.OpenFile in the supervisor).
func planGPU(want bool, nodes []renderNode, override string, open func(string) error) gpuPlan {
	if !want {
		return gpuPlan{reason: gpuOff}
	}
	node, err := pickRenderNode(nodes, override)
	if err != nil {
		return gpuPlan{reason: err.Error()}
	}
	p := gpuPlan{node: node}
	switch node.Driver {
	case "nvidia":
		// Venus needs the host's Vulkan driver to export and import
		// dma-bufs, which NVIDIA's doesn't.
		p.reason = "NVIDIA's proprietary driver can't share its memory with the VM (no dma-buf export in its Vulkan driver), which the VM's GPU needs: Venus and virgl run on AMD and Intel GPUs, and on NVIDIA's with nouveau"
	case "":
		p.reason = node.Path + " has no kernel driver"
	}
	if p.reason == "" {
		if err := open(node.Path); err != nil {
			var pe *os.PathError
			if errors.As(err, &pe) {
				err = pe.Err
			}
			p.reason = fmt.Sprintf("you can't open %s (%v): add yourself to its group (often render, with sudo usermod -aG render $USER) and log in again", node.Path, err)
		}
	}
	return p
}

// openRenderNode checks that the user can use a render node: a variable for
// tests.
var openRenderNode = openRW

// openRW checks that path can be opened for reading and writing.
func openRW(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// crosvmRuns checks that crosvm starts here at all: it links the host's
// graphics libraries (the tarball's HOST-LIBRARIES), which a machine may lack.
func crosvmRuns(ctx context.Context, l Layout) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, l.Bin(gpuDir+"/bin/crosvm"), "version").CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
		msg = msg[i+1:]
	}
	if strings.Contains(msg, "error while loading shared libraries") {
		return fmt.Errorf("crosvm, the VM's GPU device, can't run here (%s): install your distribution's libepoxy, libgbm, libdrm and Vulkan loader", msg)
	}
	return fmt.Errorf("crosvm, the VM's GPU device, can't run here (%v: %s)", err, cmp.Or(msg, "no output"))
}

// gpuParams is crosvm's `device gpu --params`, as scripts/chv-gpu/try.sh
// runs it on the user's machine:
//
//   - virgl, virgl2 and venus contexts: OpenGL and Vulkan.
//   - render-node (our crosvm patch, with our rutabaga_gfx patch that lets
//     it through): the node virglrenderer renders GL on, rather than the
//     first one readdir finds.
//   - surfaceless off: a surfaceless EGL display is Mesa's default device
//     whatever the fd; off, EGL comes from the GBM device on the node.
//   - external-blob: every blob the guest maps reaches Cloud Hypervisor as
//     an fd (SHMEM_MAP), the only kind its patchset takes; virglrenderer is
//     built with minigbm_allocation, so GL buffers are GBM buffers exported
//     as dma-bufs.
//   - implicit-render-server: crosvm starts virgl_render_server for Venus.
//   - No display: without DISPLAY or WAYLAND_DISPLAY crosvm's is its stub,
//     which still gives the guest a scanout; agents draw on their own.
func gpuParams(node string) string {
	b, _ := json.Marshal(map[string]any{
		"context-types":          "virgl:virgl2:venus",
		"egl":                    true,
		"surfaceless":            false,
		"vulkan":                 true,
		"implicit-render-server": true,
		"external-blob":          true,
		"render-node":            node,
	})
	return string(b)
}

// crosvmArgs is crosvm's GPU device's command line.
func crosvmArgs(l Layout, node string) []string {
	return []string{"--no-syslog", "device", "gpu", "--socket-path", l.GPUSocket(), "--params", gpuParams(node)}
}

// gpuEnv is crosvm's environment: the host's, but for a display, with the
// render server it starts for Venus. Nothing picks a Vulkan device: Mesa's
// device-select layer (MESA_VK_DEVICE_SELECT, DRI_PRIME) in the render
// server breaks Venus (vkEnumeratePhysicalDeviceGroups fails). Venus lists
// every host GPU, and the guest picks: agents get MESA_VK_DEVICE_SELECT for
// the VM's (GPUStatus.Vulkan, package agent).
func gpuEnv(l Layout, environ []string) []string {
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "DISPLAY", "WAYLAND_DISPLAY", "RENDER_SERVER_EXEC_PATH", "MESA_VK_DEVICE_SELECT", "MESA_VK_DEVICE_SELECT_FORCE_DEFAULT_DEVICE", "DRI_PRIME":
			return true
		}
		return false
	})
	return append(env, "RENDER_SERVER_EXEC_PATH="+l.Bin(gpuDir+"/libexec/virgl_render_server"))
}

// guestGPUFile is where the supervisor tells the daemon in the VM about the
// GPU (GPUStatus, as JSON), which it shows beside the switch while the VM
// has no render node: in /run, so a VM that restarts has only what this
// start wrote. agent.FindVMGPU reads it.
const guestGPUFile = "/run/agentbox-vm-gpu.json"

// guestStatusScript writes st to guestGPUFile.
func guestStatusScript(st GPUStatus) string {
	b, _ := json.Marshal(st)
	return "printf '%s\\n' " + shQuote(string(b)) + " | sudo -n tee " + guestGPUFile + ".new >/dev/null && sudo -n mv " + guestGPUFile + ".new " + guestGPUFile
}

// grubEntries is shell that finds GRUB's menu entry for a kernel version
// (entry VERSION) and the submenu they're in, in the guest's grub.cfg, as
// grub-reboot and grub-set-default take them, and the newest generic kernel
// in /boot (newest_generic). SUDO, BOOT, SYS and ETC are for tests.
const grubEntries = `SUDO=${SUDO-sudo -n}
BOOT=${BOOT:-/boot}
SYS=${SYS:-/sys}
ETC=${ETC:-/etc}
cfg=$BOOT/grub/grub.cfg
entry() {
  sub=$($SUDO sed -n "s/^submenu .*menuentry_id_option '\([^']*\)'.*/\1/p" "$cfg" | head -n1)
  id=$($SUDO sed -n "s/.*menuentry_id_option '\(gnulinux-$1-advanced-[^']*\)'.*/\1/p" "$cfg" | head -n1)
  [ -n "$id" ] || { echo "GRUB has no entry for the kernel $1" >&2; exit 1; }
  echo "${sub:+$sub>}$id"
}
newest_generic() { (cd "$BOOT" && ls vmlinuz-*-amd64 2>/dev/null) | sed 's/^vmlinuz-//' | grep -v -- '-cloud-' | sort -V | tail -n1; }
`

// kernelScript readies the guest's kernel for the GPU: nothing, printing
// "driver", when the running kernel has virtio-gpu; else it installs
// Debian's generic kernel beside the cloud one and has GRUB boot it once at
// the next start, printing "trial VERSION". GRUB_DEFAULT=saved is what lets
// grub-reboot make a one-shot boot; the default stays the running kernel.
const kernelScript = `set -eu
` + grubEntries + `if [ -d "$SYS/module/virtio_gpu" ] || modinfo virtio_gpu >/dev/null 2>&1; then echo driver; exit 0; fi
running=$(uname -r)
$SUDO env DEBIAN_FRONTEND=noninteractive apt-get -q -o DPkg::Lock::Timeout=300 update >/dev/null
$SUDO env DEBIAN_FRONTEND=noninteractive apt-get -q -y -o DPkg::Lock::Timeout=300 install --no-install-recommends linux-image-amd64 >/dev/null
generic=$(newest_generic)
[ -n "$generic" ] || { echo "no generic kernel in $BOOT after installing linux-image-amd64" >&2; exit 1; }
echo 'GRUB_DEFAULT=saved' | $SUDO tee "$ETC/default/grub.d/91-agentbox-gpu.cfg" >/dev/null
$SUDO update-grub 2>/dev/null
# Assigned first: set -e doesn't see a failed $(...) in an argument.
default=$(entry "$running")
once=$(entry "$generic")
$SUDO grub-set-default "$default"
$SUDO grub-reboot "$once"
echo "trial $generic"
`

// defaultKernelScript makes the running kernel GRUB's default when it's a
// generic one, the newest installed, since a kernel update adds a newer one
// whose entry the saved default doesn't follow; it prints "default VERSION",
// or "cloud" when the cloud kernel runs, which it leaves as it is.
const defaultKernelScript = `set -eu
` + grubEntries + `running=$(uname -r)
case $running in *-cloud-*) echo cloud; exit 0 ;; esac
generic=$(newest_generic)
default=$(entry "${generic:-$running}")
$SUDO grub-set-default "$default"
echo "default ${generic:-$running}"
`
