package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostGPUFindsWhicheverDeviceIsThere points HostGPU's device paths at a
// temporary directory rather than /dev, so the test doesn't depend on
// whatever GPU (or lack of one) runs it. NVIDIA is checked first: a host
// with both an NVIDIA device and Mesa render nodes (its own, from the
// proprietary driver's own GL stack) is still an NVIDIA host.
func TestHostGPUFindsWhicheverDeviceIsThere(t *testing.T) {
	dir := t.TempDir()
	oldNvidia, oldGlob := nvidiaDevice, renderNodeGlob
	t.Cleanup(func() { nvidiaDevice, renderNodeGlob = oldNvidia, oldGlob })

	nvidiaDevice = filepath.Join(dir, "nvidia0")
	renderNodeGlob = filepath.Join(dir, "dri", "renderD*")

	if got := HostGPU(); got.Kind != GPUNone {
		t.Fatalf("an empty host: got %+v", got)
	}

	if err := os.MkdirAll(filepath.Join(dir, "dri"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"renderD129", "renderD128"} {
		if err := os.WriteFile(filepath.Join(dir, "dri", n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := HostGPU(); got.Kind != GPUAMD || got.Node != filepath.Join(dir, "dri", "renderD128") {
		t.Fatalf("a host with render nodes: got %+v", got)
	}

	if err := os.WriteFile(nvidiaDevice, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := HostGPU(); got.Kind != GPUNvidia || got.Node != nvidiaDevice {
		t.Fatalf("a host with both: got %+v, want NVIDIA to win", got)
	}
}

// TestGPUDeviceArgsUsesAModeNotNvidiaRuntime checks gpuDeviceArgs is the same
// regardless of GPU kind, and never carries nvidia.runtime: that key belongs
// to instance config (ApplyGPU and Create's build set it with `config set`),
// not to the gpu device itself, which Incus would silently ignore it on.
func TestGPUDeviceArgsUsesAModeNotNvidiaRuntime(t *testing.T) {
	want := []string{"gpu", "mode=0666"}
	got := gpuDeviceArgs()
	if len(got) != len(want) {
		t.Fatalf("gpuDeviceArgs() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("gpuDeviceArgs() = %v, want %v", got, want)
		}
	}
}

// TestGPUCreateStepsSetsNvidiaRuntimeBeforeStart checks Create's own steps
// builder: the device add always carries mode=0666, nvidia.runtime is a
// separate "config set" step (not folded into the device args, which Incus
// would silently ignore it on), and an instance copied from another agent
// that already has the device only gets the runtime step, not a second add.
func TestGPUCreateStepsSetsNvidiaRuntimeBeforeStart(t *testing.T) {
	amd := GPUStatus{Kind: GPUAMD, Node: "/dev/dri/renderD128"}
	nvidia := GPUStatus{Kind: GPUNvidia, Node: "/dev/nvidia0"}

	steps := gpuCreateSteps("ab-p-a1", amd, false)
	if len(steps) != 1 || strings.Join(steps[0], " ") != "config device add ab-p-a1 agentbox-gpu gpu mode=0666" {
		t.Fatalf("gpuCreateSteps(amd, false) = %v", steps)
	}

	steps = gpuCreateSteps("ab-p-a1", nvidia, false)
	if len(steps) != 2 {
		t.Fatalf("gpuCreateSteps(nvidia, false) = %v, want 2 steps", steps)
	}
	if strings.Join(steps[0], " ") != "config device add ab-p-a1 agentbox-gpu gpu mode=0666" {
		t.Fatalf("gpuCreateSteps(nvidia, false)[0] = %v", steps[0])
	}
	if strings.Join(steps[1], " ") != "config set ab-p-a1 nvidia.runtime=true" {
		t.Fatalf("gpuCreateSteps(nvidia, false)[1] = %v", steps[1])
	}

	steps = gpuCreateSteps("ab-p-a1", nvidia, true)
	if len(steps) != 1 || strings.Join(steps[0], " ") != "config set ab-p-a1 nvidia.runtime=true" {
		t.Fatalf("gpuCreateSteps(nvidia, true) = %v, want only the runtime step", steps)
	}
}

func TestAndroidGPUModeFallsBackToSoftwareWithoutTheDevice(t *testing.T) {
	if got := androidGPUMode(false); got != "swiftshader_indirect" {
		t.Errorf("without a GPU device: got %q", got)
	}
	if got := androidGPUMode(true); got != "host" {
		t.Errorf("with a GPU device: got %q", got)
	}
}

func TestRecordingEncoderFallsBackToLibx264WithoutTheDevice(t *testing.T) {
	cases := []struct {
		name   string
		status GPUStatus
		hasGPU bool
		codec  string
		input  string
		filter string
		pixfmt string
	}{
		{"no device at all", GPUStatus{Kind: GPUAMD}, false, "libx264", "", "", "-pix_fmt yuv420p "},
		{"AMD/Intel: VAAPI", GPUStatus{Kind: GPUAMD, Node: "/dev/dri/renderD128"}, true,
			"h264_vaapi", "-hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi ", ",format=nv12,hwupload", ""},
		{"NVIDIA: NVENC", GPUStatus{Kind: GPUNvidia, Node: "/dev/nvidia0"}, true, "h264_nvenc", "", "", "-pix_fmt yuv420p "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enc := recordingEncoder(c.status, c.hasGPU)
			if enc.Codec != c.codec || enc.Input != c.input || enc.Filter != c.filter || enc.pixelFormat() != c.pixfmt {
				t.Errorf("recordingEncoder(%+v, %v) = %+v, pixelFormat %q", c.status, c.hasGPU, enc, enc.pixelFormat())
			}
		})
	}
}

// TestStartRecordingScriptUsesTheHardwareEncoder checks the ffmpeg command
// startRecordingScript builds actually names the accelerated codec and its
// hwaccel input, not only that recordingEncoder picked one.
func TestStartRecordingScriptUsesTheHardwareEncoder(t *testing.T) {
	st := recordingState{Target: "display", Limit: 30}
	enc := recordingEncoder(GPUStatus{Kind: GPUAMD, Node: "/dev/dri/renderD128"}, true)
	script, err := startRecordingScript(st, enc)
	if err != nil {
		t.Fatal(err)
	}
	// burn_overlay, the second pass that draws the desktop input log onto a
	// finished recording, always re-encodes with libx264 regardless of what
	// recorded the first pass, so it isn't checked here.
	for _, want := range []string{"-hwaccel vaapi -hwaccel_device /dev/dri/renderD128", "-c:v h264_vaapi", "format=nv12,hwupload"} {
		if !strings.Contains(script, want) {
			t.Errorf("script doesn't contain %q:\n%s", want, script)
		}
	}
}
