package hostvm

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// A krunkit VM is made from Debian's generic image, whose kernel has the
// virtio-gpu driver, ahead of the template's; every other keeps the template's.
func TestDefinitionKrunkitImage(t *testing.T) {
	vm, _ := newFake(t)
	vm.VMType = krunkitVMType
	def, err := vm.Definition(DefaultSize())
	if err != nil {
		t.Fatal(err)
	}
	want := "\nimages:\n  - location: https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-arm64.qcow2\n    arch: aarch64\n"
	if !strings.Contains(def, want) {
		t.Errorf("no generic image in:\n%s", def)
	}
	if !strings.Contains(def, "\nbase: template:debian-13\n") {
		t.Errorf("the template went:\n%s", def)
	}
	for _, other := range []string{"", "vz", "qemu"} {
		vm.VMType = other
		def, _ := vm.Definition(DefaultSize())
		if strings.Contains(def, "images:") {
			t.Errorf("vmType %q has images of its own:\n%s", other, def)
		}
	}
}

// venusTar carries venus.sh and kernel.sh, executable, and every patch it
// applies.
func TestVenusTar(t *testing.T) {
	b, err := venusTar()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]int64{}
	tr := tar.NewReader(b)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		files[h.Name] = h.Mode
	}
	for _, name := range []string{"venus.sh", "kernel.sh"} {
		if files[name] != 0o755 {
			t.Errorf("%s: mode %o in %v", name, files[name], files)
		}
	}
	patches := 0
	for name, mode := range files {
		if strings.HasSuffix(name, ".patch") {
			patches++
			if mode != 0o644 || strings.Contains(name, "/") {
				t.Errorf("%s: mode %o", name, mode)
			}
		}
	}
	if patches != 3 {
		t.Errorf("%d patches in %v", patches, files)
	}
}

// venus.sh parses, leaves the kernel to kernel.sh, and checks the Mesa it
// downloads.
func TestVenusScript(t *testing.T) {
	script, err := venusFiles.ReadFile("venus/venus.sh")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", "-c", string(script)).CombinedOutput(); err != nil {
		t.Fatalf("venus.sh doesn't parse: %v\n%s", err, out)
	}
	for _, want := range []string{"kernel.sh\" try", "sha256sum -c", "-Dvulkan-drivers=virtio"} {
		if !bytes.Contains(script, []byte(want)) {
			t.Errorf("venus.sh has no %q", want)
		}
	}
	// Where it puts the driver is where package agent mounts it from.
	if !bytes.Contains(script, []byte("dest=${AGENTBOX_VENUS_DIR:-/usr/local/lib/agentbox/venus}")) {
		t.Error("venus.sh puts the driver somewhere agent.VenusDir doesn't look")
	}
}

// Setup puts venus/ in a krunkit VM and doesn't run it: the daemon does,
// when "GPU for agents" is on. A VM of another type gets nothing.
func TestSetupGPU(t *testing.T) {
	for _, tc := range []struct {
		name, vmType string
		fails, sends bool
		wantLog      string
	}{
		{name: "krunkit", vmType: krunkitVMType, sends: true},
		{name: "krunkit, copy fails", vmType: krunkitVMType, fails: true, wantLog: "agents can't get a GPU in this VM, for now"},
		{name: "vz", vmType: "vz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running","vmType":"`+tc.vmType+`"}`+"\n"), 0o644)
			if tc.fails {
				_ = os.WriteFile(filepath.Join(dir, "venus-fails"), nil, 0o644)
			}
			vm.setupGPU(context.Background())
			for _, call := range calls(t, dir) {
				if strings.Contains(call, "venus.sh") || strings.Contains(call, "kernel.sh") {
					t.Errorf("Setup ran %q", call)
				}
			}
			sent, _ := os.ReadFile(filepath.Join(dir, "installed"))
			if h, err := tar.NewReader(bytes.NewReader(sent)).Next(); (err == nil && h.Name != "") != tc.sends {
				t.Errorf("sent a tar of venus/: %v, want %v", err == nil, tc.sends)
			}
			log := vm.Log.(*bytes.Buffer).String()
			if tc.wantLog != "" && !strings.Contains(log, tc.wantLog) {
				t.Errorf("log has no %q:\n%s", tc.wantLog, log)
			}
			if tc.wantLog == "" && log != "" {
				t.Errorf("log: %q", log)
			}
		})
	}
}

// gpuScript finds the render node and its driver from sysfs, and reads the
// renderers from vulkaninfo and eglinfo.
func TestGPUScript(t *testing.T) {
	root := t.TempDir()
	dri := filepath.Join(root, "dev/dri")
	drm := filepath.Join(root, "sys/class/drm/renderD128")
	drivers := filepath.Join(root, "sys/bus/virtio/drivers/virtio_gpu")
	for _, d := range []string{dri, drm, drivers} {
		_ = os.MkdirAll(d, 0o755)
	}
	_ = os.WriteFile(filepath.Join(dri, "renderD128"), nil, 0o644)
	_ = os.MkdirAll(filepath.Join(drm, "device"), 0o755)
	_ = os.Symlink(drivers, filepath.Join(drm, "device", "driver"))
	bin := filepath.Join(root, "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "vulkaninfo"), []byte("#!/bin/sh\necho 'GPU0:'\necho '\tdeviceName         = Virtio-GPU Venus (Apple M3 Pro)'\n"), 0o755)
	_ = os.WriteFile(filepath.Join(bin, "eglinfo"), []byte("#!/bin/sh\necho 'OpenGL core profile renderer: zink Vulkan 1.3(Virtio-GPU Venus (Apple M3 Pro) (MESA_VENUS))'\n"), 0o755)

	script := strings.NewReplacer("/dev/dri/", root+"/dev/dri/", "/sys/class/drm/", root+"/sys/class/drm/").Replace(gpuScript)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "render node: " + root + "/dev/dri/renderD128\nkernel driver: virtio_gpu\nvulkan: Virtio-GPU Venus (Apple M3 Pro)\n" +
		"opengl: zink Vulkan 1.3(Virtio-GPU Venus (Apple M3 Pro) (MESA_VENUS))\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}

	// Nothing there: every line, empty.
	empty := strings.NewReplacer("/dev/dri/", t.TempDir()+"/").Replace(gpuScript)
	cmd = exec.Command("sh", "-c", empty)
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
	if out, _ := cmd.Output(); string(out) != "render node: \nkernel driver: \nvulkan: \nopengl: \n" {
		t.Errorf("with nothing: %q", out)
	}
}

func TestGPUReport(t *testing.T) {
	got := gpuReport("The VM (krunkit)", "render node: /dev/dri/renderD128\nkernel driver: virtio_gpu\nvulkan: Virtio-GPU Venus (Apple M3 Pro)\nopengl: zink Vulkan 1.3(Virtio-GPU Venus (Apple M3 Pro) (MESA_VENUS))\n")
	want := "The VM (krunkit):\n  render node:  /dev/dri/renderD128 (virtio_gpu)\n  Vulkan:       Virtio-GPU Venus (Apple M3 Pro)\n" +
		"  OpenGL:       zink Vulkan 1.3(Virtio-GPU Venus (Apple M3 Pro) (MESA_VENUS))\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	got = gpuReport("p/a", "render node: \nkernel driver: \nvulkan: llvmpipe (LLVM 19.1.7, 128 bits)\nopengl: llvmpipe (LLVM 19.1.7, 128 bits)\n")
	for _, want := range []string{"render node:  none", "llvmpipe (LLVM 19.1.7, 128 bits) (software, on the CPU: not the GPU)"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	got = gpuReport("The VM (krunkit)", "render node: \nkernel driver: \nvulkan: \nopengl: \nrestart: yes\n")
	for _, want := range []string{"none yet: the VM boots the kernel with the GPU driver when it restarts", "Vulkan:       none", "OpenGL:       none"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestNoGPU(t *testing.T) {
	for _, tc := range []struct{ driver, vmType, want string }{
		{api.VMDriverLima, krunkitVMType, ""},
		{api.VMDriverLima, "vz", "Apple's Virtualization framework (vz) gives a Linux VM no 3D"},
		{api.VMDriverLima, "", "Apple's Virtualization framework"},
		{api.VMDriverVZ, "", "Apple's Virtualization framework"},
		{api.VMDriverLima, "qemu", "Lima's qemu VM has no 3D"},
		{api.VMDriverCloudHypervisor, "", "this VM has none"},
	} {
		got := noGPU(tc.driver, tc.vmType)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("noGPU(%q, %q) = %q, want %q", tc.driver, tc.vmType, got, tc.want)
		}
	}
}
