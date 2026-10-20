package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"agentbox/internal/incus"
	"agentbox/internal/state"
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

// vgpuDriverDevice is the Incus device that mounts the VM's Venus driver into
// an agent.
const vgpuDriverDevice = "agentbox-vgpu-driver"

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

// vgpuDriverSteps are the changes that give an instance the VM's Venus
// driver, from devices, its own: added when on ("GPU for agents" is on) and
// there's a driver to give, replaced when it's mounted from somewhere else,
// and taken off otherwise. Both happen live.
func vgpuDriverSteps(instance string, devices map[string]map[string]string, on bool) []incusStep {
	source, path := venusDriver()
	have, ok := devices[vgpuDriverDevice]
	want := on && source != ""
	if ok && want && have["source"] == source && have["path"] == path {
		return nil
	}
	var steps []incusStep
	if ok {
		steps = append(steps, func(ctx context.Context, c incus.Client) error {
			return c.RemoveDevice(ctx, instance, vgpuDriverDevice)
		})
	}
	if want {
		steps = append(steps, func(ctx context.Context, c incus.Client) error {
			return c.AddDevice(ctx, instance, vgpuDriverDevice, "disk", "source="+source, "path="+path, "readonly=true")
		})
	}
	return steps
}

// ApplyVGPUDriver gives an agent's machine the VM's Venus driver when on is
// set, or takes it off, as "GPU for agents" changes and as a machine is made,
// started or restored, beside its agentbox-vgpu device. Nothing when it
// already is as it should be, or there's no driver and never was.
func (m *Manager) ApplyVGPUDriver(ctx context.Context, a state.Agent, on bool) error {
	devices, err := m.Incus.Devices(ctx, a.Instance)
	if err != nil {
		return err
	}
	for _, step := range vgpuDriverSteps(a.Instance, devices, on) {
		if err := step(ctx, m.Incus); err != nil {
			return err
		}
	}
	return nil
}
