package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"agentbox/internal/hostos"
)

// In AgentBox's VM on a Mac there is no KVM to run emulators with, whatever
// is installed, and the reason says so rather than pointing at firmware.
func TestAndroidUnsupportedInAMacsVM(t *testing.T) {
	t.Setenv(hostos.Env, "darwin")
	err := AndroidUnsupported()
	if err == nil || !strings.Contains(err.Error(), "VM on a Mac") {
		t.Fatalf("got %v", err)
	}
	t.Setenv(hostos.Env, "")
	if err := AndroidUnsupported(); (err == nil) != (runtime.GOARCH == "amd64") {
		t.Fatalf("on %s, not in a VM: got %v", runtime.GOARCH, err)
	}
}

// The Cloud Hypervisor VM on a Linux host has the host's KVM, nested, so
// emulators aren't refused there; WSL still is.
func TestAndroidUnsupportedInTheLinuxHostsVM(t *testing.T) {
	t.Setenv(hostos.Env, hostos.Linux)
	if err := AndroidUnsupported(); (err == nil) != (runtime.GOARCH == "amd64") {
		t.Fatalf("on %s, in the Cloud Hypervisor VM: got %v", runtime.GOARCH, err)
	}
	t.Setenv(hostos.Env, hostos.Windows)
	if err := AndroidUnsupported(); err == nil || !strings.Contains(err.Error(), "VM on Windows") {
		t.Fatalf("in WSL: got %v", err)
	}
}

// In the Cloud Hypervisor VM, a missing /dev/kvm is the host's nested
// virtualization, not the firmware, and one the user can't open is fixed by
// restarting the VM, not by host setup.
func TestCheckKVMInTheLinuxHostsVM(t *testing.T) {
	dir := t.TempDir()
	old := kvmPath
	t.Cleanup(func() { kvmPath = old })
	kvmPath = filepath.Join(dir, "kvm")
	t.Setenv(hostos.Env, hostos.Linux)
	if err := CheckKVM(); err == nil || !strings.Contains(err.Error(), "nested=1") {
		t.Errorf("no /dev/kvm: got %v", err)
	}
	t.Setenv(hostos.Env, "")
	if err := CheckKVM(); err == nil || !strings.Contains(err.Error(), "firmware") && !strings.Contains(err.Error(), "WSL") {
		t.Errorf("no /dev/kvm on a host: got %v", err)
	}
	if err := os.WriteFile(kvmPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckKVM(); err != nil {
		t.Errorf("a /dev/kvm this user can open: got %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root opens anything")
	}
	_ = os.Chmod(kvmPath, 0)
	t.Setenv(hostos.Env, hostos.Linux)
	if err := CheckKVM(); err == nil || !strings.Contains(err.Error(), "agentbox vm stop, then agentbox vm start") {
		t.Errorf("a /dev/kvm the VM's user can't open: got %v", err)
	}
}

// TestAndroidFindsTheGPUAsTheDesktopDoes keeps the emulator on the GPU
// exactly when the desktop's Xvnc is: on the agent's DRM render node.
func TestAndroidFindsTheGPUAsTheDesktopDoes(t *testing.T) {
	t.Parallel()
	fn := regexp.MustCompile(`(?s)\nrender_node\(\) \{.*?\n\}\n`)
	android, browser := fn.FindString(string(androidScript)), fn.FindString(string(BrowserScript()))
	if android == "" || android != browser {
		t.Errorf("android.sh's render_node differs from browser.sh's:\n%s\n%s", android, browser)
	}
}

// TestAndroidTellsAGPUFailure is what makes the emulator fall back to
// SwiftShader: the lines below are from emulator 37.1 on Venus, the GPU of
// AgentBox's VM.
func TestAndroidTellsAGPUFailure(t *testing.T) {
	t.Parallel()
	fn := regexp.MustCompile(`(?s)\ngpu_failure\(\) \{.*?\n\}\n`).FindString(string(androidScript))
	if fn == "" {
		t.Fatal("android.sh has no gpu_failure")
	}
	ok := "INFO         | Graphics backend: gfxstream\n" +
		"ERROR        | Your GPU cannot be used for hardware rendering. Consider using software rendering.\n" +
		"ERROR        | VulkanAllocateHostMemory is enabled but is not supported, you might encounter errors when using vkMapMemory() due to unaligned host mappings.\n" +
		"INFO         | Boot completed in 31000 ms\n"
	for name, c := range map[string]struct{ log, want string }{
		"booted": {ok, ""},
		"no log": {"", ""},
		"no EGL display": {ok + "GlxEnginegetDefaultDisplay: Failed to open display 0. DISPLAY: [(null)]\n" +
			"ERROR        | Failed to get EGL display.\n" +
			"ERROR        | Failed to initialize GL emulation.\n" +
			"ERROR        | Can't initialize RenderLib with parameters: width=1080, height=2400 sRendererUsesSubWindow=1\n" +
			"ERROR        | Could not start renderer! (Error: -2)\n" +
			"ERROR        | Could not start renderer\n", "Failed to initialize GL emulation."},
		"Vulkan on Venus": {ok + "ERROR        | Format VK_FORMAT_R8G8B8A8_UNORM [37] is not supported.\n" +
			"FATAL        | Failed to find memory type for ColorBuffers.\n", "Failed to find memory type for ColorBuffers."},
	} {
		dir := t.TempDir()
		if c.log != "" {
			if err := os.WriteFile(filepath.Join(dir, "emulator.log"), []byte(c.log), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("sh", "-c", fn+"gpu_failure")
		cmd.Env = append(os.Environ(), "state="+dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("%s: gpu_failure = %q, want %q", name, got, c.want)
		}
	}
}
