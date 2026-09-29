package agent

import (
	"os"
	"path/filepath"
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
