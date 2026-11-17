package agent

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// On a Mac, AgentBox's VM runs on krunkit, whose GPU speaks Venus: Vulkan
// forwarded to the Mac and run on Metal. Mesa's Venus driver, as Debian builds
// it, can't work with the Mac's 16 KiB pages, so the VM builds its own with
// the patches that fix that (internal/hostvm/venus/venus.sh) and keeps it in
// VenusDir. An agent with "GPU for agents" on gets the VM's render node
// (agentbox-vgpu) and, beside it, that driver mounted over its own Mesa's
// libvulkan_virtio.so: Mesa's ICD file names the path, so the agent's Vulkan
// loader picks it up with nothing else changed, and Mesa's Zink runs OpenGL
// on it. The VM and the agents' image are both Debian 13, with the same
// libraries the driver links to.
//
// Nowhere else is there a VenusDir: a VM on Linux or Windows has no krunkit,
// and agents keep the driver they have.

// The Incus devices that mount the VM's Venus driver into an agent, and
// venusChromium beside it.
const (
	vgpuDriverDevice   = "agentbox-vgpu-driver"
	vgpuChromiumDevice = "agentbox-vgpu-chromium"
)

// venusChromium gives every Chromium in an agent the flags that run WebGL on
// the Venus driver, from Debian's /etc/chromium.d: an agent that launches its
// own, as a benchmark does, otherwise renders on the CPU.
//
//go:embed venus-chromium.sh
var venusChromium string

// venusDevices are the devices that go beside a render node in a Mac's VM, as
// devices does: none when it built no driver. chromium is the host file
// mounted as venusChromium.
func venusDevices(chromium string) []gpuDevice {
	source, path := venusDriver()
	if source == "" {
		return nil
	}
	return []gpuDevice{
		{vgpuDriverDevice, "disk", []string{"source=" + source, "path=" + path, "readonly=true"}, ""},
		{vgpuChromiumDevice, "disk", []string{"source=" + chromium, "path=/etc/chromium.d/agentbox-gpu", "readonly=true"}, venusChromium},
	}
}

// VenusDir is where the Mac's VM keeps the Venus driver it built: the library
// and, in "multiarch", Debian's multiarch name for the directory agents have
// theirs in. A variable for tests.
var VenusDir = "/usr/local/lib/agentbox/venus"

// venusDriver is the VM's Venus driver and the path it goes to in an agent,
// or "" for both when the VM built none.
func venusDriver() (source, path string) {
	source = filepath.Join(VenusDir, "libvulkan_virtio.so")
	multiarch, err := os.ReadFile(filepath.Join(VenusDir, "multiarch"))
	if err != nil || strings.TrimSpace(string(multiarch)) == "" {
		return "", ""
	}
	if fi, err := os.Stat(source); err != nil || !fi.Mode().IsRegular() {
		return "", ""
	}
	return source, "/usr/lib/" + strings.TrimSpace(string(multiarch)) + "/libvulkan_virtio.so"
}

// VenusScript is venus.sh in the VM, where the Mac's front end puts it in a
// krunkit VM (hostvm's venusSrc): there only, so it's also how the daemon
// knows that its VM's GPU speaks Venus and can be readied. A variable for
// tests.
var VenusScript = "/usr/local/share/agentbox/venus/venus.sh"

// VenusRestartMarker is what venus.sh leaves when the VM has to restart for
// the kernel it installed. A variable for tests.
var VenusRestartMarker = "/run/agentbox-gpu-restart"

// VenusSudo runs venus.sh as root; tests run it as themselves.
var VenusSudo = []string{"sudo", "-n"}

// VenusVM says whether this is a krunkit VM, whose GPU venus.sh readies.
func VenusVM() bool {
	_, err := os.Stat(VenusScript)
	return err == nil
}

// VenusRestart says whether the VM has to restart for its GPU: venus.sh
// installed a kernel with the driver, which it boots next.
func VenusRestart() bool {
	_, err := os.Stat(VenusRestartMarker)
	return err == nil
}

// ReadyVenus runs venus.sh, which readies the VM's GPU for agents: a kernel
// with the virtio-gpu driver and the Venus driver the Mac needs. Minutes the
// first time, milliseconds after. retry lets it try a kernel again that
// didn't boot (kernel.sh); without it, that is an error, the last thing
// venus.sh said. Its progress goes to out.
//
// ctx ending stops the wait, not venus.sh: killed half-way through apt, it
// would leave the VM's packages half-installed. It writes to files rather
// than pipes, which would kill it when they closed, and a venus.sh started
// meanwhile waits for it (flock).
func ReadyVenus(ctx context.Context, retry bool, out io.Writer) error {
	again := "0"
	if retry {
		again = "1"
	}
	stdout, err := os.CreateTemp("", "agentbox-venus-*.out")
	if err != nil {
		return err
	}
	defer func() { _ = stdout.Close(); _ = os.Remove(stdout.Name()) }()
	stderr, err := os.CreateTemp("", "agentbox-venus-*.err")
	if err != nil {
		return err
	}
	defer func() { _ = stderr.Close(); _ = os.Remove(stderr.Name()) }()
	args := append(slices.Clone(VenusSudo), "env", "AGENTBOX_VENUS_RETRY="+again, "bash", VenusScript)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Cancel = func() error { return nil }
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	for _, f := range []*os.File{stdout, stderr} {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			_, _ = io.Copy(out, f)
		}
	}
	if runErr != nil {
		b, _ := os.ReadFile(stderr.Name())
		if last := lastLine(string(b)); last != "" {
			return errors.New(last)
		}
		return runErr
	}
	return nil
}

// lastLine is s's last line that isn't blank.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
