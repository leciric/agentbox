package hostvm

import (
	"archive/tar"
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// GPU for agents on a Mac. A krunkit VM always has a virtio-gpu that speaks
// Venus: the guest's Vulkan calls go to the Mac, where virglrenderer runs them
// on MoltenVK, Vulkan on Metal; OpenGL runs on that through Mesa's Zink.
// krunkit turns it on by itself (there's no flag for it, and Lima passes
// none), so what the VM needs is a kernel with the virtio-gpu driver and a
// Venus driver that works with the Mac's 16 KiB pages: venus/venus.sh, which
// Setup puts in a krunkit VM and the daemon there runs when "GPU for agents"
// is turned on. A new VM is made from Debian's generic image (agentbox.yaml),
// whose kernel has the driver; a VM made before gets that kernel from
// venus/kernel.sh, and has to restart once for it, falling back to its cloud
// kernel if the new one doesn't boot.
//
// Apple's Virtualization framework gives a Linux guest a 2D framebuffer and
// nothing for 3D, so a vz VM, Lima's or AgentBox's own, has no GPU for
// agents, nor has Cloud Hypervisor's on Linux.
//
// Agents get the render node and the driver from the daemon in the VM when
// "GPU for agents" is on (package agent).

//go:embed venus
var venusFiles embed.FS

// venusSrc is where Setup puts venus.sh and its patches in the VM: where
// agent.VenusScript looks for it.
const venusSrc = "/usr/local/share/agentbox/venus"

// venusTar is venus/ as a tar stream, which the VM unpacks into venusSrc.
func venusTar() (*bytes.Buffer, error) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	err := fs.WalkDir(venusFiles, "venus", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := venusFiles.ReadFile(path)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(path, "venus/"), Mode: mode, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &b, tw.Close()
}

// restartMarker is what kernel.sh leaves when the VM has to restart for the
// kernel it installed: in /run, which a restart empties.
const restartMarker = "/run/agentbox-gpu-restart"

// setupGPU puts venus/ in a krunkit VM, for Setup: the daemon there runs
// venus.sh when "GPU for agents" is on (agent.ReadyVenus), and only then, since
// it changes the VM's kernel and builds a driver; that it's there is how the
// daemon knows its VM's GPU can be readied. A failure doesn't stop Setup,
// since agents run without a GPU too: it says what went wrong.
func (v *VM) setupGPU(ctx context.Context) {
	if v.CHV != nil {
		return
	}
	if st, err := v.State(ctx); err != nil || st.VMType != krunkitVMType {
		return
	}
	files, err := venusTar()
	if err == nil {
		_, err = v.exec(ctx, files, "sudo", "sh", "-c", "rm -rf "+venusSrc+" && mkdir -p "+venusSrc+" && tar -x -o -C "+venusSrc)
	}
	if err != nil {
		_, _ = fmt.Fprintf(v.Log, "note: agents can't get a GPU in this VM, for now: copying what readies it failed (%v). agentbox vm init tries again.\n", err)
	}
}

// gpuScript describes the GPU where it runs, the VM or an agent, one
// "key: value" line each, the keys in gpuReport's order: the render node and
// its kernel driver, and the renderers Vulkan and OpenGL get. A value is ""
// for what isn't there; the tools are Debian's vulkan-tools and mesa-utils.
const gpuScript = `node=; driver=
for n in /dev/dri/renderD*; do
  [ -e "$n" ] || continue
  node=$n
  driver=$(basename "$(readlink -f /sys/class/drm/"${n##*/}"/device/driver 2>/dev/null)" 2>/dev/null)
  break
done
echo "render node: $node"
echo "kernel driver: $driver"
vk=
if command -v vulkaninfo >/dev/null; then
  vk=$(vulkaninfo --summary 2>/dev/null | awk -F' = ' '/deviceName/ { print $2; exit }')
fi
echo "vulkan: $vk"
gl=
if command -v eglinfo >/dev/null; then
  gl=$(eglinfo -B -p surfaceless 2>/dev/null | awk -F': ' '/OpenGL core profile renderer/ { print $2; exit }')
fi
if [ -z "$gl" ] && command -v glxinfo >/dev/null; then
  gl=$(DISPLAY=${DISPLAY:-:99} glxinfo -B 2>/dev/null | awk -F': ' '/OpenGL renderer string/ { print $2; exit }')
fi
echo "opengl: $gl"
[ -e ` + restartMarker + ` ] && echo "restart: yes"
true
`

// noGPU says why a VM has no GPU for agents, by its driver and type; "" for a
// krunkit VM, which has one.
func noGPU(driver, vmType string) string {
	switch {
	case driver == api.VMDriverLima && vmType == krunkitVMType:
		return ""
	case driver == api.VMDriverLima && vmType == "qemu":
		return "no GPU: Lima's qemu VM has no 3D for agents."
	case driver == api.VMDriverLima, driver == api.VMDriverVZ:
		return "no GPU: Apple's Virtualization framework (vz) gives a Linux VM no 3D. A VM made with krunkit has a GPU\n" +
			"for agents: install krunkit (" + krunkitInstall + "),\n" +
			"then agentbox vm delete and agentbox vm init make the VM again with it."
	}
	return "no GPU: this VM has none to give agents."
}

// gpuReport turns gpuScript's lines into what `vm gpu` prints about where
// they came from, saying what's missing.
func gpuReport(where, out string) string {
	fields := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if k, val, ok := strings.Cut(line, ":"); ok {
			fields[k] = strings.TrimSpace(val)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", where)
	node, driver := fields["render node"], fields["kernel driver"]
	switch {
	case node == "" && fields["restart"] == "yes":
		b.WriteString("  render node:  none yet: the VM boots the kernel with the GPU driver when it restarts (agentbox vm stop, then agentbox vm start)\n")
	case node == "":
		b.WriteString("  render node:  none (no /dev/dri/renderD*: turning on \"GPU for agents\" in Settings readies one)\n")
	default:
		fmt.Fprintf(&b, "  render node:  %s (%s)\n", node, or(driver, "unknown driver"))
	}
	fmt.Fprintf(&b, "  Vulkan:       %s\n", renderer(fields["vulkan"], "none (vulkaninfo found no device, or isn't installed)"))
	fmt.Fprintf(&b, "  OpenGL:       %s\n", renderer(fields["opengl"], "none (eglinfo and glxinfo found no renderer, or aren't installed)"))
	return b.String()
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// renderer is a renderer's name for gpuReport, or none when there's none,
// saying so when it's Mesa's software one (llvmpipe, which lavapipe's Vulkan
// device is named after too) rather than the GPU.
func renderer(name, none string) string {
	switch {
	case name == "":
		return none
	case strings.Contains(name, "llvmpipe"):
		return name + " (software, on the CPU: not the GPU)"
	}
	return name
}

func newGPUCmd() *cobra.Command {
	var agent string
	cmd := &cobra.Command{
		Use:   "gpu [--agent project/agent]",
		Short: "Say which GPU the VM gives agents, and which renderer Vulkan and OpenGL get",
		Long: `Says whether the VM has a GPU for agents, and which renderers Vulkan and OpenGL get in it:
on a Mac, a VM made with krunkit has one (Venus: Vulkan on the Mac's Metal, and OpenGL on
that through Zink); a vz VM has none. With --agent, it says the same for that agent, which
has the VM's GPU when "GPU for agents" is on.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			driver, vmType := vm.Driver(), ""
			if vm.CHV == nil {
				st, err := vm.State(cmd.Context())
				if err != nil {
					return err
				}
				if !st.Exists {
					return ErrNotCreated
				}
				vmType = st.VMType
			}
			if why := noGPU(driver, vmType); why != "" {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), why)
				return err
			}
			if err := vm.Up(cmd.Context()); err != nil {
				return err
			}
			out, err := vm.exec(cmd.Context(), nil, "sh", "-c", gpuScript)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			_, _ = io.WriteString(w, gpuReport("The VM (krunkit)", out))
			if agent == "" {
				return nil
			}
			out, err = vm.exec(cmd.Context(), nil, vmBinary, "exec", agent, "--", "sh", "-c", gpuScript)
			if err != nil {
				return fmt.Errorf("asking %s: %w", agent, err)
			}
			_, _ = io.WriteString(w, gpuReport(agent, out))
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "an agent to ask too, as project/agent")
	return cmd
}
