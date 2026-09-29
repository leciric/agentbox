package hostvm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// krunkit is the Lima VM type that gives the Mac back the memory the VM stops
// using: libkrun's balloon reports the guest's free pages, and krunkit returns
// them to macOS, so a VM only costs the Mac what it uses at the moment rather
// than all it has ever used. Apple's Virtualization framework (vz, Lima's
// default on a Mac) has a balloon too, but releases nothing on the host when
// it's inflated (lima-vm/lima#4220, and #4226, closed for that reason), so a
// vz VM keeps what it has touched until it stops.
//
// krunkit is optional, and experimental in Lima: `agentbox vm init` makes a new
// VM with it when this Mac can run it (krunkitCheck: Lima has its driver,
// lima-driver-krunkit, and krunkit is installed), and with vz otherwise, as
// before; AGENTBOX_VM_TYPE overrides the choice either way. An existing VM
// keeps the type it was made with. What differs from vz for AgentBox:
//
//   - It needs Apple Silicon (Lima only builds the driver there), macOS 13 or
//     later and krunkit 1.2.1 or later. Lima's driver checks the last two when
//     it makes the VM, and a VM it refuses to make with krunkit is made with vz.
//   - Lima's driver runs krunkit from PATH, which an app opened from the Finder
//     doesn't have Homebrew in: krunkitOnPath adds it.
//   - Free page reporting needs libkrun 1.19 or later: init asks the guest
//     whether its balloon negotiated it (freePageReporting), and says so when
//     it didn't.
//   - Reporting only returns free memory, and Linux keeps what it has read
//     cached until it runs short, so the VM drops its caches when it's quiet
//     (the krunkit provision script in agentbox.yaml).
//   - The rest is Lima's and works as with vz: the home share is virtiofs
//     (libkrun's own), the disk a raw file, ssh and the daemon's socket go
//     over Lima's user-mode network, its guest agent over vsock.
const krunkitVMType = "krunkit"

// KrunkitCheck is whether a new VM would be made with krunkit on this Mac, for
// Setup: nil on a machine krunkit doesn't run on.
type KrunkitCheck struct {
	Available bool `json:"available"`
	// Missing is what isn't installed when it's not available: "krunkit", or
	// "driver" for a Lima without lima-driver-krunkit.
	Missing string `json:"missing,omitempty"`
}

// krunkitCheck asks Lima whether it has a krunkit driver, and looks for
// krunkit itself. nil when this isn't a Mac with Apple Silicon, or Lima won't
// say.
func (v *VM) krunkitCheck(ctx context.Context) *KrunkitCheck {
	out, err := v.lima(ctx, nil, "info")
	if err != nil {
		return nil
	}
	var info struct {
		HostOS    string                     `json:"hostOS"`
		HostArch  string                     `json:"hostArch"`
		VMTypesEx map[string]json.RawMessage `json:"vmTypesEx"`
	}
	if json.Unmarshal([]byte(out), &info) != nil || info.HostOS != "darwin" || info.HostArch != "aarch64" {
		return nil
	}
	switch {
	case findKrunkit() == "":
		return &KrunkitCheck{Missing: "krunkit"}
	case info.VMTypesEx[krunkitVMType] == nil:
		return &KrunkitCheck{Missing: "driver"}
	}
	return &KrunkitCheck{Available: true}
}

// krunkitDirs are where Homebrew puts krunkit, looked in after PATH.
var krunkitDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// findKrunkit is the krunkit binary, or "" when it isn't installed.
func findKrunkit() string {
	if p, err := exec.LookPath("krunkit"); err == nil {
		return p
	}
	for _, dir := range krunkitDirs {
		p := filepath.Join(dir, "krunkit")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// krunkitOnPath puts krunkit's directory on this process's PATH, and so on
// the PATH of every limactl it runs: Lima's driver runs krunkit from PATH, and
// an app opened from the Finder has only the system's. Nothing when krunkit is
// on PATH already, or isn't installed.
func krunkitOnPath() {
	if _, err := exec.LookPath("krunkit"); err == nil {
		return
	}
	if p := findKrunkit(); p != "" {
		_ = os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Dir(p))
	}
}

// krunkitInstall is how to install krunkit. Lima's documentation says brew tap
// slp/krun, then brew install krunkit, which Homebrew now refuses from a tap
// it hasn't been told to trust; naming the formula in full is that.
const krunkitInstall = "brew install slp/krun/krunkit"

// krunkitNote is what `vm init` says about krunkit before it makes a VM: that
// it's making it with krunkit, or why not and what that costs. "" when there
// is nothing to say.
func krunkitNote(c *KrunkitCheck) string {
	switch {
	case c == nil:
		return ""
	case c.Available:
		return "==> Making the VM with krunkit, so it gives the memory it stops using back to your Mac\n"
	case c.Missing == "driver":
		return "note: this Lima has no krunkit driver (lima-driver-krunkit), so the VM is made with Apple's Virtualization framework\n" +
			"      and keeps the memory it has used until it stops. Made with krunkit, it gives it back as it stops using it.\n"
	}
	return "note: krunkit isn't installed, so the VM is made with Apple's Virtualization framework and keeps the memory it has\n" +
		"      used until it stops. With krunkit installed first (" + krunkitInstall + "), it gives it back as the VM stops using it.\n"
}

// createVM is Create for `vm init`, choosing the VM type when AGENTBOX_VM_TYPE
// doesn't: krunkit when this Mac can run it, and Lima's default otherwise, or
// when Lima refuses to make it with krunkit (a macOS or krunkit too old).
func (v *VM) createVM(ctx context.Context, size Size) error {
	if v.VMType != "" {
		return v.Create(ctx, size)
	}
	c := v.krunkitCheck(ctx)
	_, _ = fmt.Fprint(v.Log, krunkitNote(c))
	if c == nil || !c.Available {
		return v.Create(ctx, size)
	}
	v.VMType = krunkitVMType
	err := v.Create(ctx, size)
	if err == nil {
		return nil
	}
	// A create that fails leaves no instance behind (Lima removes it), so it
	// can be made again with vz.
	_, _ = fmt.Fprintf(v.Log, "==> Lima wouldn't make the VM with krunkit (%v, see above): making it with Apple's Virtualization framework instead,\n"+
		"    which keeps the memory the VM has used until it stops\n", err)
	v.VMType = ""
	return v.Create(ctx, size)
}

// reportingScript prints, for the guest's virtio balloon (device 0x0005),
// whether it negotiated VIRTIO_BALLOON_F_REPORTING: bit 5 of its features,
// which sysfs lists as one character a bit, from bit 0.
const reportingScript = `for d in /sys/bus/virtio/devices/*; do [ "$(cat "$d/device" 2>/dev/null)" = 0x0005 ] && cut -c6 "$d/features"; done; true`

// freePageReporting reports whether the VM's balloon hands the guest's free
// memory back to the Mac, which krunkit's does from libkrun 1.19.
func (v *VM) freePageReporting(ctx context.Context) (bool, error) {
	out, err := v.exec(ctx, nil, "sh", "-c", reportingScript)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "1"), nil
}

// checkReporting says, after `vm init`, when a krunkit VM doesn't give memory
// back after all: its libkrun is too old to report free pages.
func (v *VM) checkReporting(ctx context.Context, st State) {
	if st.VMType != krunkitVMType {
		return
	}
	ok, err := v.freePageReporting(ctx)
	if err != nil || ok {
		return
	}
	_, _ = fmt.Fprintln(v.Log, "note: the VM runs with krunkit, but its balloon doesn't report free memory, so it can't give memory back to your Mac:\n"+
		"      that needs libkrun 1.19 or later. brew upgrade krunkit, then agentbox vm stop and agentbox vm start.")
}
