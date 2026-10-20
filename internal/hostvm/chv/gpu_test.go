package chv

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	amdDiscrete = renderNode{Path: "/dev/dri/renderD129", Driver: "amdgpu", Vendor: "1002", Device: "73df", Slot: "0000:03:00.0", BootVGA: true, VRAM: 12 << 30}
	amdAPU      = renderNode{Path: "/dev/dri/renderD128", Driver: "amdgpu", Vendor: "1002", Device: "13c0", Slot: "0000:7a:00.0", VRAM: 512 << 20}
	intelIGPU   = renderNode{Path: "/dev/dri/renderD128", Driver: "i915", Vendor: "8086", Device: "a780", Slot: "0000:00:02.0", BootVGA: true}
	nvidia      = renderNode{Path: "/dev/dri/renderD129", Driver: "nvidia", Vendor: "10de", Device: "2684", Slot: "0000:01:00.0"}
)

func TestPickRenderNode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes []renderNode
		want  string
		got   string
		err   string
	}{
		{"none", nil, "", "", "no GPU render node"},
		{"one", []renderNode{amdAPU}, "", amdAPU.Path, ""},
		// virglrenderer alone would take renderD128, the APU.
		{"discrete before the APU", []renderNode{amdAPU, amdDiscrete}, "", amdDiscrete.Path, ""},
		{"discrete before Intel's, though Intel's is the display", []renderNode{intelIGPU, nvidia}, "", nvidia.Path, ""},
		{"named", []renderNode{amdAPU, amdDiscrete}, "renderD128", amdAPU.Path, ""},
		{"named by path", []renderNode{amdAPU, amdDiscrete}, "/dev/dri/renderD128", amdAPU.Path, ""},
		{"named, missing", []renderNode{amdAPU}, "renderD130", "", "AGENTBOX_VM_RENDER_NODE"},
		{"by number, not name", []renderNode{{Path: "/dev/dri/renderD130", Driver: "virtio_gpu"}, {Path: "/dev/dri/renderD99", Driver: "virtio_gpu"}}, "", "/dev/dri/renderD99", ""},
	} {
		n, err := pickRenderNode(tc.nodes, tc.want)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: %v, want an error with %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || n.Path != tc.got {
			t.Errorf("%s: %s, %v; want %s", tc.name, n.Path, err, tc.got)
		}
	}
}

// The VM gets the GPU only when the switch is on and the node is one crosvm
// can use: never NVIDIA's proprietary driver (no Venus), never one the user
// can't open, and each time with the reason Settings shows.
func TestPlanGPU(t *testing.T) {
	ok := func(string) error { return nil }
	denied := func(p string) error { return &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission} }
	for _, tc := range []struct {
		name   string
		want   bool
		nodes  []renderNode
		open   func(string) error
		node   string
		reason string
	}{
		{"off", false, []renderNode{amdDiscrete}, ok, "", gpuOff},
		{"on", true, []renderNode{amdAPU, amdDiscrete}, ok, amdDiscrete.Path, ""},
		{"no GPU", true, nil, ok, "", "no GPU render node"},
		{"NVIDIA", true, []renderNode{intelIGPU, nvidia}, ok, nvidia.Path, "NVIDIA's proprietary driver"},
		{"nouveau", true, []renderNode{{Path: "/dev/dri/renderD128", Driver: "nouveau"}}, ok, "/dev/dri/renderD128", ""},
		{"no driver", true, []renderNode{{Path: "/dev/dri/renderD128"}}, ok, "/dev/dri/renderD128", "no kernel driver"},
		{"not the user's", true, []renderNode{amdDiscrete}, denied, amdDiscrete.Path, "add yourself to its group"},
	} {
		p := planGPU(tc.want, tc.nodes, "", tc.open)
		if p.node.Path != tc.node || !strings.Contains(p.reason, tc.reason) || (tc.reason == "") != p.ok() {
			t.Errorf("%s: %+v", tc.name, p)
		}
	}
	if p := planGPU(true, []renderNode{amdDiscrete}, "", denied); !strings.Contains(p.reason, "permission denied") || strings.Contains(p.reason, "renderD129: permission") {
		t.Errorf("the reason names the error, not the call: %s", p.reason)
	}
}

// crosvm's line is try.sh's, as it ran on the user's machine: EGL on the
// GBM device rather than surfaceless, external blobs, no display, and no
// Vulkan device selection, which breaks Venus in the render server.
func TestCrosvmArgsAndEnv(t *testing.T) {
	l := Layout{Root: "/v", Name: "agentbox"}
	args := crosvmArgs(l, amdDiscrete.Path)
	if !slices.Equal(args[:5], []string{"--no-syslog", "device", "gpu", "--socket-path", "/v/agentbox/run/gpu.sock"}) || args[5] != "--params" {
		t.Fatalf("args: %q", args)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(args[6]), &params); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"context-types": "virgl:virgl2:venus", "egl": true, "surfaceless": false, "vulkan": true, "implicit-render-server": true, "external-blob": true, "render-node": amdDiscrete.Path}
	if len(params) != len(want) {
		t.Errorf("params: %v", params)
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("params[%s] = %v, want %v", k, params[k], v)
		}
	}

	env := gpuEnv(l, []string{"HOME=/h", "DISPLAY=:0", "WAYLAND_DISPLAY=wayland-1", "MESA_VK_DEVICE_SELECT=8086:a780", "DRI_PRIME=1"})
	if !slices.Equal(env, []string{"HOME=/h", "RENDER_SERVER_EXEC_PATH=/v/bin/gpu/libexec/virgl_render_server"}) {
		t.Errorf("env: %q", env)
	}
}

func TestCHArgsGPU(t *testing.T) {
	l := Layout{Root: t.TempDir(), Name: "vm"}
	c := Config{Name: "vm", CPUs: 2, MemoryMin: 2 * GiB, MemoryCap: 8 * GiB}
	without := strings.Join(chArgs(c, l, Room{CPUs: 2, Memory: 8 * GiB}, false), " ")
	with := strings.Join(chArgs(c, l, Room{CPUs: 2, Memory: 8 * GiB}, true), " ")
	if strings.Contains(without, "--gpu") || !strings.Contains(with, "--gpu socket="+l.GPUSocket()+" --vsock") {
		t.Errorf("without: %s\nwith: %s", without, with)
	}
	// The GPU changes nothing else: memory stays shared, which vhost-user
	// needs, and its window isn't memory the VM is given.
	if strings.Replace(with, " --gpu socket="+l.GPUSocket(), "", 1) != without || !strings.Contains(with, "shared=on") {
		t.Errorf("with the GPU, the rest changed:\n%s\n%s", without, with)
	}
}

// findRenderNodes reads what sysfs says of each render node.
func TestFindRenderNodes(t *testing.T) {
	root := t.TempDir()
	old := sysDRM
	sysDRM = filepath.Join(root, "class", "drm")
	t.Cleanup(func() { sysDRM = old })
	pci := filepath.Join(root, "devices", "pci0000:00", "0000:03:00.0")
	write := func(name, value string) {
		if err := os.WriteFile(filepath.Join(pci, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(sysDRM, "renderD128"), 0o755))
	must(os.MkdirAll(pci, 0o755))
	must(os.MkdirAll(filepath.Join(root, "bus", "pci", "drivers", "amdgpu"), 0o755))
	must(os.Symlink(pci, filepath.Join(sysDRM, "renderD128", "device")))
	must(os.Symlink(filepath.Join(root, "bus", "pci", "drivers", "amdgpu"), filepath.Join(pci, "driver")))
	write("vendor", "0x1002")
	write("device", "0x73df")
	write("boot_vga", "1")
	write("mem_info_vram_total", "12868124672")
	got := findRenderNodes()
	want := []renderNode{{Path: "/dev/dri/renderD128", Driver: "amdgpu", Vendor: "1002", Device: "73df", Slot: "0000:03:00.0", BootVGA: true, VRAM: 12868124672}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
	if got[0].name() != "amdgpu, 1002:73df" || got[0].integrated() {
		t.Errorf("%s, integrated %t", got[0].name(), got[0].integrated())
	}
}

// What Settings and `vm gpu` say, for each way the VM can be.
func TestGPUState(t *testing.T) {
	on := gpuPlan{node: amdDiscrete}
	off := gpuPlan{reason: gpuOff}
	nvidiaPlan := planGPU(true, []renderNode{nvidia}, "", func(string) error { return nil })
	crosvmFailed := gpuPlan{node: amdDiscrete, reason: "crosvm's GPU device didn't start (exit status 1)"}
	for _, tc := range []struct {
		name   string
		st     GPUStatus
		f      gpuFacts
		state  string
		reason string
	}{
		{"has it", GPUStatus{Want: true}, gpuFacts{attached: true, driver: true, started: on, now: on}, GPUOn, ""},
		{"has it, kernel not ready", GPUStatus{Want: true}, gpuFacts{attached: true, started: on, now: on}, GPUNone, "no GPU driver"},
		{"has it, turned off", GPUStatus{}, gpuFacts{attached: true, driver: true, started: on, now: on}, GPUOff, ""},
		{"crosvm died", GPUStatus{Want: true}, gpuFacts{attached: true, crosvmGone: true, driver: true, started: on, now: on}, GPUNone, "stopped"},
		{"off, host has one", GPUStatus{}, gpuFacts{started: off, now: on}, GPUOff, ""},
		{"off, host has none", GPUStatus{}, gpuFacts{started: off, now: gpuPlan{reason: "this machine has no GPU render node"}}, GPUNone, "no GPU render node"},
		{"turned on, next start", GPUStatus{Want: true}, gpuFacts{driver: true, started: off, now: on}, GPURestart, "next starts"},
		{"turned on, kernel on trial", GPUStatus{Want: true, Trial: "6.12.43+deb13-amd64"}, gpuFacts{started: off, now: on}, GPURestart, "kernel with the GPU driver"},
		{"kernel failed", GPUStatus{Want: true, KernelFailed: "6.12.43+deb13-amd64"}, gpuFacts{started: off, now: on}, GPUNone, "didn't come up on Debian's generic kernel 6.12.43+deb13-amd64"},
		{"kernel install failed", GPUStatus{Want: true, KernelFailed: kernelInstallFailed + "exit status 100"}, gpuFacts{started: off, now: on}, GPUNone, "installing Debian's generic kernel"},
		{"NVIDIA", GPUStatus{Want: true}, gpuFacts{started: nvidiaPlan, now: nvidiaPlan}, GPUNone, "NVIDIA"},
		{"crosvm didn't start", GPUStatus{Want: true}, gpuFacts{started: crosvmFailed, now: on}, GPUNone, "didn't start"},
	} {
		got := gpuState(tc.st, tc.f)
		if got.State != tc.state || !strings.Contains(got.Reason, tc.reason) || (tc.reason == "") != (got.Reason == "") {
			t.Errorf("%s: %+v", tc.name, got)
		}
		if got.Reason != "" && !strings.HasSuffix(got.Reason, ".") {
			t.Errorf("%s: %q isn't a sentence", tc.name, got.Reason)
		}
		if (got.Vulkan == "1002:73df") != tc.f.attached {
			t.Errorf("%s: vulkan %q", tc.name, got.Vulkan)
		}
		if (got.State == GPUOn || got.State == GPUOff || got.State == GPURestart) && got.Node != amdDiscrete.Path {
			t.Errorf("%s: node %q", tc.name, got.Node)
		}
	}
}

func TestGPUStatusFile(t *testing.T) {
	l := Layout{Root: t.TempDir(), Name: "vm"}
	if st := ReadGPUStatus(l); st != (GPUStatus{}) {
		t.Errorf("none yet: %+v", st)
	}
	_ = os.MkdirAll(l.Dir(), 0o755)
	want := GPUStatus{Want: true, State: GPURestart, Node: "/dev/dri/renderD128", Trial: "6.12.43+deb13-amd64"}
	if err := writeGPUStatus(l, want); err != nil {
		t.Fatal(err)
	}
	if got := ReadGPUStatus(l); got != want {
		t.Errorf("got %+v", got)
	}
	// What the guest is given is the same JSON, quoted for sh.
	out, err := exec.Command("sh", "-c", strings.NewReplacer("sudo -n ", "", guestGPUFile, filepath.Join(l.Dir(), "guest.json")).Replace(guestStatusScript(GPUStatus{Reason: "it's \"off\"."}))).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(l.Dir(), "guest.json"))
	var got GPUStatus
	if err := json.Unmarshal(b, &got); err != nil || got.Reason != `it's "off".` {
		t.Errorf("%s: %v", b, err)
	}
}

// fakeGuest is a guest's /boot, /etc and /sys, and the programs the kernel
// scripts run, faked in a directory; log has each program's arguments.
type fakeGuest struct {
	dir, boot, log string
	env            []string
}

func newFakeGuest(t *testing.T, running string, kernels ...string) *fakeGuest {
	t.Helper()
	g := &fakeGuest{dir: t.TempDir()}
	g.boot = filepath.Join(g.dir, "boot")
	g.log = filepath.Join(g.dir, "log")
	bin := filepath.Join(g.dir, "bin")
	for _, d := range []string{filepath.Join(g.boot, "grub"), bin, filepath.Join(g.dir, "etc", "default", "grub.d"), filepath.Join(g.dir, "sys", "module")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range kernels {
		_ = os.WriteFile(filepath.Join(g.boot, "vmlinuz-"+k), nil, 0o644)
	}
	script := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho \""+name+" $*\" >>\""+g.log+"\"\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("uname", "echo "+running+"\n")
	script("modinfo", "exit 1\n")
	// Installing linux-image-amd64 puts a generic kernel in /boot.
	script("apt-get", `case "$*" in *install*) : >"`+g.boot+`/vmlinuz-6.12.43+deb13-amd64" ;; esac`+"\n")
	// update-grub lists every kernel in /boot, as Debian's does.
	script("update-grub", `{
echo "menuentry 'Debian GNU/Linux' --class debian \$menuentry_id_option 'gnulinux-simple-0b6d' {"
echo "}"
echo "submenu 'Advanced options for Debian GNU/Linux' \$menuentry_id_option 'gnulinux-advanced-0b6d' {"
for k in $(cd "`+g.boot+`" && ls vmlinuz-* | sed 's/^vmlinuz-//'); do
  echo "	menuentry 'Debian GNU/Linux, with Linux $k' --class debian \$menuentry_id_option 'gnulinux-$k-advanced-0b6d' {"
  echo "	}"
done
echo "}"
} >"`+g.boot+`/grub/grub.cfg"
`)
	script("grub-set-default", "")
	script("grub-reboot", "")
	g.env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "SUDO=", "BOOT="+g.boot, "SYS="+filepath.Join(g.dir, "sys"), "ETC="+filepath.Join(g.dir, "etc"))
	return g
}

func (g *fakeGuest) run(t *testing.T, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = g.env
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = errors.New(string(exit.Stderr))
	}
	return string(out), err
}

func (g *fakeGuest) calls(prog string) []string {
	b, _ := os.ReadFile(g.log)
	var calls []string
	for line := range strings.Lines(string(b)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prog+" "); ok {
			calls = append(calls, rest)
		}
	}
	return calls
}

// A VM on the cloud kernel gets the generic one beside it, booted once at
// its next start, with the cloud one staying GRUB's default.
func TestKernelScriptOnCloudKernel(t *testing.T) {
	cloud := "6.12.43+deb13-cloud-amd64"
	g := newFakeGuest(t, cloud, cloud)
	_ = os.WriteFile(filepath.Join(g.boot, "grub", "grub.cfg"), nil, 0o644)
	out, err := g.run(t, kernelScript)
	if err != nil || strings.TrimSpace(out) != "trial 6.12.43+deb13-amd64" {
		t.Fatalf("%q, %v", out, err)
	}
	sub := "gnulinux-advanced-0b6d>"
	if got := g.calls("grub-set-default"); !slices.Equal(got, []string{sub + "gnulinux-" + cloud + "-advanced-0b6d"}) {
		t.Errorf("default: %q", got)
	}
	if got := g.calls("grub-reboot"); !slices.Equal(got, []string{sub + "gnulinux-6.12.43+deb13-amd64-advanced-0b6d"}) {
		t.Errorf("once: %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, "etc", "default", "grub.d", "91-agentbox-gpu.cfg")); string(b) != "GRUB_DEFAULT=saved\n" {
		t.Errorf("grub.d: %q", b)
	}
	if got := g.calls("apt-get"); len(got) != 2 || !strings.Contains(got[1], "install --no-install-recommends linux-image-amd64") {
		t.Errorf("apt-get: %q", got)
	}
	// Nothing is removed: the cloud kernel stays.
	if _, err := os.Stat(filepath.Join(g.boot, "vmlinuz-"+cloud)); err != nil || slices.ContainsFunc(g.calls("apt-get"), func(c string) bool { return strings.Contains(c, "remove") || strings.Contains(c, "purge") }) {
		t.Error("the cloud kernel went")
	}
}

// A kernel with virtio-gpu needs nothing.
func TestKernelScriptWithDriver(t *testing.T) {
	g := newFakeGuest(t, "6.12.43+deb13-amd64")
	_ = os.MkdirAll(filepath.Join(g.dir, "sys", "module", "virtio_gpu"), 0o755)
	if out, err := g.run(t, kernelScript); err != nil || strings.TrimSpace(out) != "driver" || len(g.calls("apt-get")) != 0 {
		t.Errorf("%q, %v, apt-get %q", out, err, g.calls("apt-get"))
	}
}

// GRUB without the entry it was to boot fails the script, rather than
// telling grub-reboot nothing.
func TestKernelScriptNoEntry(t *testing.T) {
	g := newFakeGuest(t, "6.12.43+deb13-cloud-amd64")
	// update-grub that writes nothing.
	_ = os.WriteFile(filepath.Join(g.dir, "bin", "update-grub"), []byte("#!/bin/sh\n: >\""+g.boot+"/grub/grub.cfg\"\n"), 0o755)
	if out, err := g.run(t, kernelScript); err == nil || !strings.Contains(err.Error(), "GRUB has no entry") || len(g.calls("grub-reboot")) != 0 {
		t.Errorf("%q, %v, grub-reboot %q", out, err, g.calls("grub-reboot"))
	}
}

// Once the generic kernel came up, the newest generic one is the default;
// on the cloud kernel nothing changes.
func TestDefaultKernelScript(t *testing.T) {
	g := newFakeGuest(t, "6.12.43+deb13-amd64", "6.12.43+deb13-cloud-amd64", "6.12.43+deb13-amd64", "6.12.48+deb13-amd64")
	if _, err := g.run(t, "update-grub"); err != nil {
		t.Fatal(err)
	}
	out, err := g.run(t, defaultKernelScript)
	if err != nil || strings.TrimSpace(out) != "default 6.12.48+deb13-amd64" {
		t.Fatalf("%q, %v", out, err)
	}
	if got := g.calls("grub-set-default"); !slices.Equal(got, []string{"gnulinux-advanced-0b6d>gnulinux-6.12.48+deb13-amd64-advanced-0b6d"}) {
		t.Errorf("default: %q", got)
	}

	g = newFakeGuest(t, "6.12.43+deb13-cloud-amd64", "6.12.43+deb13-cloud-amd64", "6.12.43+deb13-amd64")
	if out, err := g.run(t, defaultKernelScript); err != nil || strings.TrimSpace(out) != "cloud" || len(g.calls("grub-set-default")) != 0 {
		t.Errorf("%q, %v, %q", out, err, g.calls("grub-set-default"))
	}
}
