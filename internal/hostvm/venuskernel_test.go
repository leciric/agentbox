package hostvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// kernelStubs stand in for what kernel.sh runs, on a fake root ($R): a VM
// booted on $FAKE_KERNEL whose /boot has the kernels, update-grub writing a
// menu entry for each, grubenv in a file, and apt installing the generic
// kernel and purging the cloud one. Each call goes to $R/calls.
var kernelStubs = map[string]string{
	"dpkg":  `echo arm64`,
	"uname": `echo "$FAKE_KERNEL"`,
	"update-grub": `echo update-grub >>"$R/calls"
: >"$R/boot/grub/grub.cfg"
for k in "$R"/boot/vmlinuz-*; do v=${k##*/vmlinuz-}
  echo "menuentry 'Debian GNU/Linux, with Linux $v' --class debian \$menuentry_id_option 'gnulinux-$v-advanced-1234' {" >>"$R/boot/grub/grub.cfg"
  echo "menuentry 'Debian GNU/Linux, with Linux $v (recovery mode)' \$menuentry_id_option 'gnulinux-$v-recovery-1234' {" >>"$R/boot/grub/grub.cfg"
done`,
	"grub-set-default": `echo "grub-set-default $1" >>"$R/calls"; grep -v '^saved_entry=' "$R/grubenv" >"$R/grubenv.new"; echo "saved_entry=$1" >>"$R/grubenv.new"; mv "$R/grubenv.new" "$R/grubenv"`,
	"grub-reboot":      `echo "grub-reboot $1" >>"$R/calls"; grep -v '^next_entry=' "$R/grubenv" >"$R/grubenv.new"; echo "next_entry=$1" >>"$R/grubenv.new"; mv "$R/grubenv.new" "$R/grubenv"`,
	"grub-editenv": `shift; cmd=$1; shift
case $cmd in
list) cat "$R/grubenv" ;;
unset) for k in "$@"; do grep -v "^$k=" "$R/grubenv" >"$R/grubenv.new"; mv "$R/grubenv.new" "$R/grubenv"; done ;;
esac`,
	"apt-get": `while [ "${1#-}" != "$1" ]; do [ "$1" = -o ] && shift; shift; done
echo "apt-get $*" >>"$R/calls"
case $1 in
install) [ ! -e "$R/apt-fails" ] || exit 100
  echo CONFIG_DRM_VIRTIO_GPU=m >"$R/boot/config-6.12.48+deb13-arm64"; : >"$R/boot/vmlinuz-6.12.48+deb13-arm64"; update-grub ;;
purge) shift; for p in "$@"; do rm -f "$R/boot/vmlinuz-${p#linux-image-}" "$R/boot/config-${p#linux-image-}"; done ;;
esac`,
	"dpkg-query": `for k in "$R"/boot/vmlinuz-*-cloud-arm64; do [ -e "$k" ] && echo "installed linux-image-${k##*/vmlinuz-}"; done; true`,
	"systemctl":  `echo "systemctl $*" >>"$R/calls"`,
}

const (
	cloudKernel   = "6.12.38+deb13-cloud-arm64"
	genericKernel = "6.12.48+deb13-arm64"
)

// kernelVM is a VM booted on its cloud kernel, and how to run kernel.sh in it.
type kernelVM struct {
	t      *testing.T
	root   string
	bin    string
	env    []string
	script string
}

func newKernelVM(t *testing.T) *kernelVM {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("kernel.sh runs in the VM's Debian")
	}
	root, bin := t.TempDir(), t.TempDir()
	for name, body := range kernelStubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"boot/grub", "run", "var/log", "etc/systemd/system"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(root, "boot", "config-"+cloudKernel), []byte("# CONFIG_DRM is not set\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "boot", "vmlinuz-"+cloudKernel), nil, 0o644)
	_ = os.WriteFile(filepath.Join(root, "grubenv"), nil, 0o644)
	script, err := filepath.Abs("venus/kernel.sh")
	if err != nil {
		t.Fatal(err)
	}
	vm := &kernelVM{t: t, root: root, bin: bin, script: script,
		env: append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "R="+root, "AGENTBOX_VENUS_ROOT="+root)}
	if out, err := vm.run("", "update-grub"); err != nil {
		t.Fatalf("update-grub: %v\n%s", err, out)
	}
	_ = os.Remove(filepath.Join(root, "calls"))
	vm.boot(cloudKernel)
	return vm
}

// boot is the VM starting again on kernel: GRUB takes next_entry as it boots
// it, and /run starts empty.
func (vm *kernelVM) boot(kernel string) {
	vm.env = append(vm.env, "FAKE_KERNEL="+kernel)
	if out, err := vm.run("", "grub-editenv", "-", "unset", "next_entry"); err != nil {
		vm.t.Fatalf("grub-editenv: %v\n%s", err, out)
	}
	_ = os.Remove(filepath.Join(vm.root, "run", "agentbox-gpu-restart"))
}

// run runs a command, a stub's from its name alone.
func (vm *kernelVM) run(retry string, args ...string) (string, error) {
	name := args[0]
	if _, ok := kernelStubs[name]; ok {
		name = filepath.Join(vm.bin, name)
	}
	cmd := exec.Command(name, args[1:]...)
	cmd.Env = append(vm.env, "AGENTBOX_VENUS_RETRY="+retry)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (vm *kernelVM) kernel(retry, step string) (string, error) {
	return vm.run(retry, "bash", vm.script, step)
}

func (vm *kernelVM) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(vm.root, name))
	return string(b)
}

func (vm *kernelVM) has(name string) bool {
	_, err := os.Stat(filepath.Join(vm.root, name))
	return err == nil
}

// takeCalls is what was run since the last time.
func (vm *kernelVM) takeCalls() string {
	calls := vm.read("calls")
	_ = os.Remove(filepath.Join(vm.root, "calls"))
	return calls
}

func entryOf(kernel string) string { return "gnulinux-" + kernel + "-advanced-1234" }

// kernel.sh installs the generic kernel beside the cloud one, which stays
// GRUB's default until the generic one has booted once: then that's the
// default and the cloud kernel goes.
func TestKernelBootsOnceThenStays(t *testing.T) {
	vm := newKernelVM(t)
	if out, err := vm.kernel("", "try"); err != nil {
		t.Fatalf("try: %v\n%s", err, out)
	}
	calls := vm.takeCalls()
	// The cloud kernel is pinned before the generic one is installed.
	pin, install := strings.Index(calls, "grub-set-default "+entryOf(cloudKernel)), strings.Index(calls, "apt-get install linux-image-arm64")
	if pin < 0 || install < 0 || pin > install || !strings.Contains(calls, "grub-reboot "+entryOf(genericKernel)) || strings.Contains(calls, "purge") {
		t.Errorf("try ran:\n%s", calls)
	}
	env := vm.read("grubenv")
	if !strings.Contains(env, "saved_entry="+entryOf(cloudKernel)) || !strings.Contains(env, "next_entry="+entryOf(genericKernel)) {
		t.Errorf("grubenv:\n%s", env)
	}
	if cfg := vm.read("etc/default/grub.d/agentbox-gpu.cfg"); !strings.Contains(cfg, "GRUB_DEFAULT=saved") || !strings.Contains(cfg, "GRUB_DISABLE_SUBMENU=y") {
		t.Errorf("grub.d: %q", cfg)
	}
	if !vm.has("run/agentbox-gpu-restart") || vm.read("var/lib/agentbox/gpu-kernel") != "trying "+genericKernel+"\n" {
		t.Errorf("no restart marker, or state %q", vm.read("var/lib/agentbox/gpu-kernel"))
	}
	if unit := vm.read("etc/systemd/system/agentbox-gpu-kernel.service"); !strings.Contains(unit, "kernel.sh boot") {
		t.Errorf("unit: %q", unit)
	}

	// Until the VM restarts, trying again changes nothing.
	if out, err := vm.kernel("", "try"); err != nil || vm.takeCalls() != "" {
		t.Errorf("try again: %v\n%s", err, out)
	}

	vm.boot(genericKernel)
	if out, err := vm.kernel("", "boot"); err != nil {
		t.Fatalf("boot: %v\n%s", err, out)
	}
	calls = vm.takeCalls()
	if !strings.Contains(calls, "grub-set-default "+entryOf(genericKernel)) || !strings.Contains(calls, "apt-get purge linux-image-"+cloudKernel) {
		t.Errorf("boot on the generic kernel ran:\n%s", calls)
	}
	if vm.has("boot/vmlinuz-"+cloudKernel) || vm.has("etc/default/grub.d/agentbox-gpu.cfg") || vm.has("var/lib/agentbox/gpu-kernel") {
		t.Error("the cloud kernel, the grub.d file or the state is still there")
	}
	if env := vm.read("grubenv"); strings.Contains(env, "_entry=") {
		t.Errorf("grubenv: %q", env)
	}
	// Nothing more to do, at boot or from venus.sh.
	if _, err := vm.kernel("", "boot"); err != nil || vm.takeCalls() != "" {
		t.Errorf("boot again: %v", err)
	}
	if _, err := vm.kernel("", "try"); err != nil || vm.takeCalls() != "" {
		t.Errorf("try on the generic kernel: %v", err)
	}
}

// When the generic kernel doesn't boot, the restart after it is on the cloud
// kernel: kernel.sh says so, and tries again only when asked to.
func TestKernelFallsBack(t *testing.T) {
	vm := newKernelVM(t)
	if out, err := vm.kernel("", "try"); err != nil {
		t.Fatalf("try: %v\n%s", err, out)
	}
	vm.takeCalls()
	vm.boot(cloudKernel)
	if out, err := vm.kernel("", "boot"); err != nil {
		t.Fatalf("boot: %v\n%s", err, out)
	}
	if state := vm.read("var/lib/agentbox/gpu-kernel"); state != "failed "+genericKernel+"\n" {
		t.Errorf("state %q", state)
	}
	if calls := vm.takeCalls(); strings.Contains(calls, "purge") || strings.Contains(calls, "grub-set-default") {
		t.Errorf("boot on the cloud kernel ran:\n%s", calls)
	}
	if env := vm.read("grubenv"); !strings.Contains(env, "saved_entry="+entryOf(cloudKernel)) {
		t.Errorf("the cloud kernel isn't the default any more:\n%s", env)
	}

	out, err := vm.kernel("", "try")
	if code := exitCode(err); code != 3 || !strings.Contains(out, "didn't boot") || vm.takeCalls() != "" {
		t.Errorf("try after the fallback: exit %d, ran something, or said %q", code, out)
	}
	if out, err := vm.kernel("1", "try"); err != nil || !strings.Contains(vm.takeCalls(), "grub-reboot "+entryOf(genericKernel)) {
		t.Errorf("retry: %v\n%s", err, out)
	}
}

// A boot that isn't GRUB's leaves next_entry, and the try still to come.
func TestKernelBootWithoutGRUB(t *testing.T) {
	vm := newKernelVM(t)
	if out, err := vm.kernel("", "try"); err != nil {
		t.Fatalf("try: %v\n%s", err, out)
	}
	if _, err := vm.kernel("", "boot"); err != nil || vm.read("var/lib/agentbox/gpu-kernel") != "trying "+genericKernel+"\n" {
		t.Errorf("boot: %v, state %q", err, vm.read("var/lib/agentbox/gpu-kernel"))
	}
}

// With no GRUB, or when apt fails, the VM keeps booting its cloud kernel.
func TestKernelKeepsTheCloudKernel(t *testing.T) {
	vm := newKernelVM(t)
	_ = os.Remove(filepath.Join(vm.root, "boot/grub/grub.cfg"))
	if out, err := vm.kernel("", "try"); err == nil || !strings.Contains(out, "doesn't boot through GRUB") || strings.Contains(vm.takeCalls(), "apt-get") {
		t.Errorf("no GRUB: %v\n%s", err, out)
	}

	vm = newKernelVM(t)
	_ = os.WriteFile(filepath.Join(vm.root, "apt-fails"), nil, 0o644)
	if _, err := vm.kernel("", "try"); err == nil {
		t.Error("apt failed, and try didn't")
	}
	if env := vm.read("grubenv"); !strings.Contains(env, "saved_entry="+entryOf(cloudKernel)) || strings.Contains(env, "next_entry") {
		t.Errorf("grubenv after apt failed:\n%s", env)
	}
	if vm.has("var/lib/agentbox/gpu-kernel") || vm.has("run/agentbox-gpu-restart") {
		t.Error("state or restart marker after apt failed")
	}
}

func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}
