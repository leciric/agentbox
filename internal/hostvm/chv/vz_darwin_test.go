//go:build darwin && cgo

package chv

import (
	"os"
	"strings"
	"testing"
)

// signed reports whether this test binary was signed with VZEntitlement, as
// CI's macOS job signs it (AGENTBOX_TEST_SIGNED).
func signed() bool { return os.Getenv("AGENTBOX_TEST_SIGNED") != "" }

// The VM's configuration, as the Virtualization framework is told it: every
// device made, and the framework's own validation passed where it can run
// (a Mac that can virtualize, a binary with the entitlement). It keeps the
// VM's EFI variables and identity for the next boot.
func TestVZConfiguration(t *testing.T) {
	l := Layout{Root: t.TempDir(), Name: "agentbox"}
	if err := os.MkdirAll(l.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, disk := range []string{l.RootDisk(), l.PoolDisk(), l.Seed()} {
		if err := makeSparse(disk, 64<<20); err != nil {
			t.Fatal(err)
		}
	}
	c := Config{Name: "agentbox", CPUs: 2, MemoryMin: 1 * GiB, MemoryCap: 2 * GiB, Home: t.TempDir(), Driver: DriverVZ}
	m, err := newVZMachine(c, l, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	vm := m.(*vzMachine)
	config, err := vm.configuration()
	if err != nil {
		t.Fatalf("making the configuration: %v", err)
	}
	for _, f := range []string{l.EFIVars(), l.MachineID()} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s wasn't kept: %v", f, err)
		}
	}
	// A second boot's configuration reads them back.
	if _, err := vm.configuration(); err != nil {
		t.Fatalf("making it again: %v", err)
	}
	if got := vm.live(c); got.MinCPUs != 2 || got.MaxCPUs != 2 || got.MaxMemory != 2*GiB {
		t.Errorf("live %+v", got)
	}
	ok, err := config.Validate()
	switch {
	case ok && err == nil:
		t.Log("the Virtualization framework validated the VM's configuration")
	case !signed() && err != nil && strings.Contains(err.Error(), VZEntitlement):
		t.Skipf("unsigned, so the framework won't validate it: %v", err)
	case err != nil && (strings.Contains(err.Error(), "not available on this hardware") || strings.Contains(strings.ToLower(err.Error()), "not supported")):
		t.Skipf("this Mac can't virtualize (a CI runner is a VM itself): %v", err)
	default:
		t.Fatalf("the framework refused the VM's configuration: ok %v, %v", ok, err)
	}
}

// CheckVZ passes a binary signed with the entitlement, and says what to do
// about one that isn't.
func TestCheckVZ(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = CheckVZ(exe)
	if signed() {
		if err != nil {
			t.Errorf("a signed binary: %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "scripts/mac-sign.sh") {
		t.Errorf("an unsigned binary: %v", err)
	}
}
