package hostvm

import (
	"context"
	"encoding/json"
	"errors"
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
	// Brew is whether Homebrew can install what's missing, which `agentbox vm
	// krunkit` does: krunkit from its tap, and the driver with a newer Lima
	// when Lima is Homebrew's (its formula builds the driver from Lima 2.0).
	Brew bool `json:"brew,omitempty"`
}

// krunkitHost is what this Mac has of what a krunkit VM needs.
type krunkitHost struct {
	krunkit bool   // krunkit is installed
	driver  bool   // Lima has lima-driver-krunkit
	brew    string // Homebrew's brew, "" without it
	// limaBrew is whether the limactl in use is Homebrew's, which brew can
	// upgrade to one with the driver.
	limaBrew bool
}

// krunkitHost asks Lima whether it has a krunkit driver, and looks for
// krunkit and Homebrew. nil when this isn't a Mac with Apple Silicon, or Lima
// won't say.
func (v *VM) krunkitHost(ctx context.Context) *krunkitHost {
	if v.Limactl == "" {
		return nil
	}
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
	h := &krunkitHost{krunkit: findKrunkit() != "", driver: info.VMTypesEx[krunkitVMType] != nil, brew: findBrew()}
	h.limaBrew = h.brew != "" && brewInstalled(h.brew, v.Limactl)
	return h
}

// check is the KrunkitCheck for Setup.
func (h *krunkitHost) check() *KrunkitCheck {
	if h == nil {
		return nil
	}
	c := &KrunkitCheck{Available: h.krunkit && h.driver, Brew: len(h.brewSteps()) > 0}
	switch {
	case !h.krunkit:
		c.Missing = "krunkit"
	case !h.driver:
		c.Missing = "driver"
	}
	return c
}

// brewSteps are the brew commands that install what's missing, nil when
// nothing is or Homebrew can't: krunkit from its tap (Lima's documentation,
// plus brew trust, without which Homebrew now refuses krunkit and the formulae
// it needs from the same tap, gvproxy and libkrun), and a Lima with the
// driver, which only Homebrew's Lima gets that way.
func (h *krunkitHost) brewSteps() [][]string {
	if h.brew == "" || (!h.driver && !h.limaBrew) {
		return nil
	}
	var steps [][]string
	if !h.krunkit {
		steps = append(steps, []string{"tap", "slp/krun"}, []string{"trust", "slp/krun"}, []string{"install", "krunkit"})
	}
	if !h.driver {
		steps = append(steps, []string{"upgrade", "lima"})
	}
	return steps
}

// krunkitCheck is krunkitHost's KrunkitCheck: nil on a machine krunkit
// doesn't run on.
func (v *VM) krunkitCheck(ctx context.Context) *KrunkitCheck {
	return v.krunkitHost(ctx).check()
}

// krunkitDirs are where Homebrew puts krunkit, and itself, looked in after
// PATH.
var krunkitDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// findKrunkit is the krunkit binary, or "" when it isn't installed.
func findKrunkit() string { return findBrewed("krunkit") }

// findBrew is Homebrew's brew, or "" when there's no Homebrew.
func findBrew() string { return findBrewed("brew") }

// findBrewed is name on PATH or where Homebrew puts it: an app opened from the
// Finder has only the system's PATH.
func findBrewed(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range krunkitDirs {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// brewInstalled reports whether bin is a formula of brew's Homebrew: a link
// into its Cellar, which is beside the bin directory brew is in.
func brewInstalled(brew, bin string) bool {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return false
	}
	prefix := filepath.Dir(filepath.Dir(brew))
	if p, err := filepath.EvalSymlinks(prefix); err == nil {
		prefix = p
	}
	return strings.HasPrefix(real, filepath.Join(prefix, "Cellar")+string(filepath.Separator))
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

// krunkitInstall is how to install krunkit: Lima's documentation, plus brew
// trust, without which Homebrew now refuses krunkit and the formulae it needs
// from the same tap (gvproxy, libkrun).
const krunkitInstall = "brew tap slp/krun && brew trust slp/krun && brew install krunkit"

// krunkitNote is what `vm init` says about krunkit before it makes a VM: that
// it's making it with krunkit, or why not and what that costs. "" when there
// is nothing to say.
func krunkitNote(c *KrunkitCheck) string {
	brew := ""
	if c != nil && c.Brew {
		brew = "      agentbox vm krunkit installs it with Homebrew, and a VM made with it has a GPU for agents too.\n"
	}
	switch {
	case c == nil:
		return ""
	case c.Available:
		return "==> Making the VM with krunkit, so it gives the memory it stops using back to your Mac, and has a GPU for agents\n"
	case c.Missing == "driver":
		return "note: this Lima has no krunkit driver (lima-driver-krunkit), so the VM is made with Apple's Virtualization framework\n" +
			"      and keeps the memory it has used until it stops. Made with krunkit, it gives it back as it stops using it.\n" + brew
	}
	return "note: krunkit isn't installed, so the VM is made with Apple's Virtualization framework and keeps the memory it has\n" +
		"      used until it stops. With krunkit installed first (" + krunkitInstall + "), it gives it back as the VM stops using it.\n" + brew
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

// errNoKrunkit is InstallKrunkit's on a Mac krunkit doesn't run on.
var errNoKrunkit = errors.New("krunkit runs only on a Mac with Apple Silicon, with Lima installed")

// InstallKrunkit installs with Homebrew what a krunkit VM needs and this Mac
// lacks (krunkitHost.brewSteps), printing brew's output to the log as it
// goes, and checks that a new VM would be made with krunkit afterwards.
// Nothing when it would already; an error that says what to run when Homebrew
// can't install it.
func (v *VM) InstallKrunkit(ctx context.Context) error {
	h := v.krunkitHost(ctx)
	switch {
	case h == nil:
		return errNoKrunkit
	case h.krunkit && h.driver:
		_, _ = fmt.Fprintln(v.Log, "==> krunkit and Lima's krunkit driver are installed")
		return nil
	}
	steps := h.brewSteps()
	if steps == nil {
		return errors.New(krunkitManual(h))
	}
	for _, args := range steps {
		_, _ = fmt.Fprintf(v.Log, "$ brew %s\n", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, h.brew, args...)
		cmd.Stdout, cmd.Stderr = v.Log, v.Log
		// brew runs what it installs from its own bin, which an app opened
		// from the Finder doesn't have on PATH.
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(h.brew)+string(os.PathListSeparator)+os.Getenv("PATH"), "HOMEBREW_NO_ENV_HINTS=1")
		if err := cmd.Run(); err != nil {
			// A Homebrew from before brew trust has no such command, and
			// installs from the tap without it.
			if args[0] == "trust" {
				_, _ = fmt.Fprintf(v.Log, "==> brew trust failed (%v): installing without it\n", err)
				continue
			}
			return fmt.Errorf("brew %s: %w", strings.Join(args, " "), err)
		}
	}
	krunkitOnPath()
	if h = v.krunkitHost(ctx); h == nil || !h.krunkit || !h.driver {
		return errors.New("brew finished, but " + krunkitManual(h))
	}
	_, _ = fmt.Fprintln(v.Log, "==> krunkit is installed: a VM made now is made with it")
	return nil
}

// krunkitManual says what's still missing for a krunkit VM, and how to
// install it by hand.
func krunkitManual(h *krunkitHost) string {
	if h == nil {
		return errNoKrunkit.Error()
	}
	var missing []string
	switch {
	case !h.krunkit && h.brew == "":
		missing = append(missing, "krunkit isn't installed, and there's no Homebrew to install it with: install Homebrew (https://brew.sh), then "+krunkitInstall)
	case !h.krunkit:
		missing = append(missing, "krunkit isn't installed: "+krunkitInstall)
	}
	switch {
	case h.driver:
	case h.limaBrew:
		missing = append(missing, "this Lima has no krunkit driver (lima-driver-krunkit): brew upgrade lima")
	default:
		missing = append(missing, "this Lima has no krunkit driver (lima-driver-krunkit), and isn't Homebrew's: install Homebrew's (brew install lima), or Lima 2.0 or later with its krunkit driver")
	}
	if len(missing) == 0 {
		return "krunkit is installed"
	}
	return strings.Join(missing, "; ")
}

// remakeWithKrunkit is `agentbox vm krunkit --remake`: krunkit installed,
// then the VM, whichever driver made it, deleted and made again by Lima with
// krunkit, at its size. Nothing is deleted when krunkit can't be installed,
// and nothing is remade when the VM is krunkit's already. newVM is New, which
// the tests replace.
func remakeWithKrunkit(ctx context.Context, newVM func() (*VM, error)) error {
	vm, err := newVM()
	if vm == nil || (vm.CHV == nil && err != nil) {
		return err
	}
	size := DefaultSize()
	if c := vm.CHV; c != nil {
		// The vz driver's VM: Lima installs nothing for it, but has to
		// have the driver for the one that replaces it.
		limactl, err := FindLimactl()
		if err != nil {
			return err
		}
		if err := (&VM{Limactl: limactl, Log: vm.Log}).InstallKrunkit(ctx); err != nil {
			return err
		}
		size = Size{CPUs: max(c.Config.CPUs, 1), Memory: sizeString(c.Config.MemoryCap, size.Memory), Disk: sizeString(c.Config.Disk, size.Disk)}
		_, _ = fmt.Fprintln(vm.Log, "==> Deleting the VM (the vz driver's), and every agent's machine in it")
		if err := vm.Delete(ctx); err != nil {
			return err
		}
		// Lima's from here on: New finds no vz driver's VM any more.
		if vm, err = newVM(); err != nil {
			return err
		}
	} else {
		st, err := vm.State(ctx)
		if err != nil {
			return err
		}
		if st.Exists && st.VMType == krunkitVMType {
			_, _ = fmt.Fprintln(vm.Log, "==> The VM is made with krunkit already")
			return nil
		}
		if err := vm.InstallKrunkit(ctx); err != nil {
			return err
		}
		if st.Exists {
			size = Size{CPUs: max(st.CPUs, 1), Memory: sizeString(st.Memory, size.Memory), Disk: sizeString(st.Disk, size.Disk)}
			_, _ = fmt.Fprintln(vm.Log, "==> Deleting the VM (Apple's Virtualization framework), and every agent's machine in it")
			if err := vm.Delete(ctx); err != nil {
				return err
			}
		}
	}
	if err := vm.initLima(ctx, size); err != nil {
		return err
	}
	if st, err := vm.State(ctx); err == nil && st.VMType != krunkitVMType {
		return errors.New("the VM was made with Apple's Virtualization framework after all (Lima refused krunkit, see above), so its agents have no GPU")
	}
	return nil
}

// sizeString is bytes as whole GiB, like 8GiB, or def when it isn't known.
func sizeString(bytes int64, def string) string {
	if bytes < 1<<30 {
		return def
	}
	return fmt.Sprintf("%dGiB", bytes>>30)
}
