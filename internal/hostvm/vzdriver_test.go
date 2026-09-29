package hostvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/hostvm/chv"
)

// fakeVZ is fakeCHV's VM made by the vz driver instead.
func fakeVZ(t *testing.T, state *string, steps *[]string) (*VM, string) {
	t.Helper()
	vm, dir := fakeCHV(t, state, steps)
	vm.CHV.Config.Driver = chv.DriverVZ
	vm.CHV.Config.MemoryCap = 8 * chv.GiB
	if err := vm.CHV.Config.Save(vm.Paths); err != nil {
		t.Fatal(err)
	}
	return vm, dir
}

// Setting a vz VM up fetches nothing (the framework is macOS's), and tells
// its agentbox the host is the Mac it runs on, with its cap, and no Android.
func TestVZSetUp(t *testing.T) {
	state, steps := api.VMOff, []string{}
	vm, dir := fakeVZ(t, &state, &steps)
	if vm.Driver() != api.VMDriverVZ {
		t.Errorf("driver %q", vm.Driver())
	}
	if err := vm.CHV.setUp(context.Background(), vm, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disks", "start", "provisioned"}; !slices.Equal(steps, want) {
		t.Errorf("steps %q, want %q", steps, want)
	}
	profile, _ := os.ReadFile(filepath.Join(dir, "profile"))
	for _, want := range []string{"export " + hostos.Env + "='" + runtime.GOOS + "'\n", "export " + vmMemoryCapEnv + "='8589934592'\n"} {
		if !strings.Contains(string(profile), want) {
			t.Errorf("profile has no %q:\n%s", want, profile)
		}
	}
	if strings.Contains(string(profile), androidSDKEnvName) {
		t.Errorf("a vz VM was told of an Android SDK:\n%s", profile)
	}

	// Starting it again doesn't fetch anything either.
	steps, state = steps[:0], api.VMOff
	if err := vm.CHV.start(context.Background(), vm); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disks", "start"}; !slices.Equal(steps, want) {
		t.Errorf("starting: steps %q, want %q", steps, want)
	}
}

// vm init --driver vz writes nothing until the Mac can run the VM: the
// framework's checks pass, there's a Linux agentbox for it, and there's no
// Lima VM of AgentBox's, which it doesn't take over.
func TestInitVZRefusesFirst(t *testing.T) {
	vm, dir := newFake(t)
	p := clearEnv(t)
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("AGENTBOX_LIMACTL", vm.Limactl)
	t.Setenv("AGENTBOX_LINUX_BINARY", vm.Binary)
	want := DefaultConfig(chv.DefaultName, "alice", 501, 20, vm.Home, 8, 32*chv.GiB)
	want.Driver = chv.DriverVZ
	want.MemoryCap = 8 * chv.GiB

	stub(t, &chvCheckVZ, func(string) error { return errors.New("not signed") })
	if err := initCHV(t.Context(), p, want, false, &bytes.Buffer{}); err == nil || err.Error() != "not signed" {
		t.Errorf("unsigned: %v", err)
	}
	stub(t, &chvCheckVZ, func(string) error { return nil })
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Stopped"}`+"\n"), 0o644)
	if err := initCHV(t.Context(), p, want, false, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "Lima VM already") {
		t.Errorf("with Lima's VM: %v", err)
	}
	t.Setenv("AGENTBOX_LINUX_BINARY", filepath.Join(dir, "missing"))
	_ = os.Remove(filepath.Join(dir, "list"))
	if err := initCHV(t.Context(), p, want, false, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no Linux agentbox") {
		t.Errorf("no Linux binary: %v", err)
	}
	if chv.Exists(p, want.Name) {
		t.Error("a refused init saved the VM's Config")
	}

	// A VM of another driver isn't made over.
	c := want
	c.Driver = ""
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := initCHV(t.Context(), p, want, false, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "cloud-hypervisor's already") {
		t.Errorf("over Cloud Hypervisor's: %v", err)
	}
}

// The Mac's app reads vm status --json in Lima's shape: a vz VM says so in
// it, and maps its state onto Lima's words.
func TestVZStatusJSON(t *testing.T) {
	state, steps := api.VMPaused, []string{}
	vm, _ := fakeVZ(t, &state, &steps)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetContext(t.Context())
	if err := vzStatus(cmd, true); err != nil {
		t.Fatal(err)
	}
	var st Status
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Driver != api.VMDriverVZ || !st.Exists || st.Status != "Running" || st.CPUs != vm.CHV.Config.CPUs || st.Dir != vm.CHV.Layout.Dir() || st.Limits.MaxCPUs == 0 {
		t.Errorf("paused: %+v", st)
	}
	state = api.VMOff
	out.Reset()
	if err := vzStatus(cmd, true); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || st.Status != "Stopped" {
		t.Errorf("off: %+v, %v", st, err)
	}
	out.Reset()
	if err := vzStatus(cmd, false); err != nil || !strings.Contains(out.String(), "experimental") {
		t.Errorf("for a person: %q, %v", out.String(), err)
	}
}

// Lima's status keeps its shape: no driver in it.
func TestLimaStatusHasNoDriver(t *testing.T) {
	b, _ := json.Marshal(Status{Name: "agentbox"})
	if strings.Contains(string(b), "driver") {
		t.Errorf("Lima's status: %s", b)
	}
}
