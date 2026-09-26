package agent

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"agentbox/internal/state"
)

// gpuDevice is the Incus device every agent's GPU passthrough is kept under,
// the way kvmDevice and androidSDKDevice are for the Android emulator.
const gpuDevice = "agentbox-gpu"

// Kinds HostGPU can find. GPUAMD covers Intel too: both go through Mesa and
// the same DRM render node, and AgentBox doesn't need to tell them apart to
// pass one through.
const (
	GPUNone   = ""
	GPUAMD    = "amd"
	GPUNvidia = "nvidia"
)

// GPUStatus is what this host offers agents for "GPU for agents": whether it
// has one, and which device found it.
type GPUStatus struct {
	Kind string // GPUAMD, GPUNvidia, or GPUNone if this host has none
	Node string // the device HostGPU found; "" for GPUNone
}

// nvidiaDevice is where the proprietary NVIDIA driver's character device
// shows up; Mesa never creates it, so its presence alone tells the two apart
// without reading either driver's userspace.
var nvidiaDevice = "/dev/nvidia0"

// renderNodeGlob matches Mesa's DRM render nodes: one per GPU the kernel's
// direct rendering manager knows about, AMD's and Intel's alike.
var renderNodeGlob = "/dev/dri/renderD*"

// HostGPU finds the host's GPU, the way android.go's CheckKVM finds
// /dev/kvm: a device file, not a working driver stack or a size of memory.
// Checked at every read rather than cached, since a GPU is not expected to
// come or go, but Setup and Settings both call this on every request and
// a stat call is cheap.
func HostGPU() GPUStatus {
	if _, err := os.Stat(nvidiaDevice); err == nil {
		return GPUStatus{Kind: GPUNvidia, Node: nvidiaDevice}
	}
	nodes, _ := filepath.Glob(renderNodeGlob)
	if len(nodes) == 0 {
		return GPUStatus{}
	}
	sort.Strings(nodes)
	return GPUStatus{Kind: GPUAMD, Node: nodes[0]}
}

// GPUForAgents reads the installation's "GPU for agents" setting: the raw
// choice, not gated on whether this host still has a GPU to give — Settings
// shows the switch only when HostGPU finds one, but the choice itself is
// independent of that check, the same way NeverFreezeCPU's flag is read apart
// from HostCores.
func (m *Manager) GPUForAgents(ctx context.Context) (bool, error) {
	return m.Store.Flag(ctx, state.SettingGPUForAgents)
}

// gpuOn is whether an agent should actually get the device right now: chosen,
// and there is one to give. A host that loses its GPU, or never had one,
// isn't asked to pass through a device that doesn't exist.
func gpuOn(ctx context.Context, m *Manager) (bool, GPUStatus, error) {
	status := HostGPU()
	if status.Kind == GPUNone {
		return false, status, nil
	}
	on, err := m.GPUForAgents(ctx)
	return on, status, err
}

// gpuDeviceArgs is the tail of `incus config device add <instance>
// agentbox-gpu ...` for status: Incus's own gpu device type, which already
// knows how to find and pass through a GPU, unlike kvm's or the Android SDK's
// unix-char/disk devices. nvidia.runtime=true on an NVIDIA host asks Incus to
// run the container through the NVIDIA container runtime, so its driver
// libraries and CUDA/EGL/Vulkan loaders land inside the agent alongside the
// device node — without it, the agent would get /dev/nvidia0 but none of the
// userspace that talks to it.
func gpuDeviceArgs(status GPUStatus) []string {
	args := []string{"gpu"}
	if status.Kind == GPUNvidia {
		args = append(args, "nvidia.runtime=true")
	}
	return args
}

// ApplyGPU adds or removes instance's agentbox-gpu device to match on, the
// same idempotent add-if-missing/remove-if-present shape prepareAndroid uses
// for /dev/kvm. Incus applies a device change to a running container as well
// as a stopped one, but some drivers only hand the device to processes
// started after the change, so an agent already using its GPU (an emulator,
// a Chromium already running) may need to be restarted to pick up a change
// made while it runs.
func (m *Manager) ApplyGPU(ctx context.Context, instance string, on bool, status GPUStatus) error {
	devices, err := m.Incus.Devices(ctx, instance)
	if err != nil {
		return err
	}
	_, has := devices[gpuDevice]
	switch {
	case on && !has:
		args := append([]string{"config", "device", "add", instance, gpuDevice}, gpuDeviceArgs(status)...)
		_, err = m.Incus.Run(ctx, args...)
	case !on && has:
		_, err = m.Incus.Run(ctx, "config", "device", "remove", instance, gpuDevice)
	}
	return err
}

// videoEncoder is the ffmpeg codec, and the extra arguments around it, for
// recording a display: VAAPI or NVENC when the agent's own container has a
// GPU device, libx264 (as before this feature) otherwise.
type videoEncoder struct {
	Codec  string // -c:v value
	Input  string // extra args before -f x11grab, "" for none
	Filter string // appended to the -vf filter graph, after the scale
}

// recordingEncoder picks videoEncoder for status, probed at record time by
// StartRecording checking whether this agent's own container has the
// agentbox-gpu device (hasGPU) — not a value fixed when the container was
// made, since the setting, and so the device, can change under a running
// agent. VAAPI's hwaccel needs the render node's path; NVENC has no
// equivalent input-side flag, since ffmpeg's nvenc encoder takes ordinary
// frames and does the upload itself.
func recordingEncoder(status GPUStatus, hasGPU bool) videoEncoder {
	if !hasGPU {
		return videoEncoder{Codec: "libx264"}
	}
	switch status.Kind {
	case GPUNvidia:
		return videoEncoder{Codec: "h264_nvenc"}
	case GPUAMD:
		return videoEncoder{
			Codec:  "h264_vaapi",
			Input:  "-hwaccel vaapi -hwaccel_device " + status.Node + " -hwaccel_output_format vaapi ",
			Filter: ",format=nv12,hwupload",
		}
	default:
		return videoEncoder{Codec: "libx264"}
	}
}

// codecArgs is videoEncoder's quality/speed arguments for a recording: fast
// is the desktop-input first pass, which trades quality for speed since it is
// encoded again when the overlay is burned onto it. Unlike libx264's crf,
// VAAPI and NVENC each price quality differently, and neither has had a real
// recording compared against libx264's output to tune these against — they
// are ffmpeg's own documented ranges for "similar to a fast x264 preset",
// untested here.
func (e videoEncoder) codecArgs(fast bool) string {
	switch e.Codec {
	case "h264_vaapi":
		if fast {
			return "-qp 16"
		}
		return "-qp 24"
	case "h264_nvenc":
		if fast {
			return "-preset p1 -cq 16"
		}
		return "-preset p4 -cq 24"
	default:
		if fast {
			return "-preset ultrafast -crf 16"
		}
		return "-preset veryfast -crf 28"
	}
}

// pixelFormat is the -pix_fmt flag videoEncoder needs, in addition to Filter:
// libx264 and NVENC both encode ordinary frames and need it spelled out;
// VAAPI's frames are already in the format hwupload put them in.
func (e videoEncoder) pixelFormat() string {
	if e.Codec == "h264_vaapi" {
		return ""
	}
	return "-pix_fmt yuv420p "
}

// RecomputeGPU applies the installation's "GPU for agents" choice to every
// agent this daemon knows about, the way RecomputeCPUCaps applies its own
// setting: called wherever a change has to reach agents that already exist,
// not only the next one created.
func (m *Manager) RecomputeGPU(ctx context.Context) error {
	on, status, err := gpuOn(ctx, m)
	if err != nil {
		return err
	}
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return err
	}
	for _, a := range agents {
		if err := m.ApplyGPU(ctx, a.Instance, on, status); err != nil {
			return err
		}
	}
	return nil
}
