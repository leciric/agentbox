package hostvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/doctor"
	"agentbox/internal/kvm"
)

// `agentbox doctor` on the user's machine checks what the VM's daemon can't
// see from inside it — that this machine can run a VM at all, that the VM
// runs and answers, and that it runs this command's version — and then asks
// that daemon for the rest, over the socket forwarded to its usual path.

// runDoctor is `agentbox doctor` here, with the same flags as in the VM.
func runDoctor(ctx context.Context, args []string) int {
	var fix, asJSON bool
	for _, a := range args {
		switch a {
		case "--fix":
			fix = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(os.Stderr, "error: unknown flag %s: agentbox doctor takes --fix and --json\n", a)
			return 1
		}
	}
	failed, err := doctor.Run(ctx, os.Stdout, os.Stderr, frontChecks, fix, asJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

// frontChecks are this machine's checks, in order: virtualization, the VM,
// then its daemon's version; the daemon is asked only when it answers.
func frontChecks(ctx context.Context) ([]doctor.Local, *api.Client) {
	local := []doctor.Local{{Check: virtualizationCheck()}}
	vm, err := New()
	if err != nil {
		c := api.DoctorCheck{ID: "vm", Title: "AgentBox's VM", Status: api.DoctorFail, Message: firstLineOf(err), Fix: "agentbox vm init"}
		if !errors.Is(err, ErrNotCreated) && local[0].Check.Status == api.DoctorFail {
			c.Fix = "fix virtualization first, then agentbox vm init"
		}
		return append(local, doctor.Local{Check: c}), nil
	}
	c := api.NewClient(vm.Paths.Socket())
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	info, err := c.Version(pctx)
	cancel()
	local = append(local, vmCheck(vm.Name, c.Socket(), err, func(ctx context.Context) error { return vm.Ready(ctx) }))
	if err != nil {
		return local, nil
	}
	version := doctor.Local{Check: doctor.VersionCheck(info.Version, frontVersion, "agentbox vm start: it installs this version in the VM")}
	if version.Check.Status != api.DoctorOK {
		version.Fix = vm.Ready
	}
	return append(local, version), c
}

// vmCheck is whether the VM's daemon answered, and starting the VM (which
// starts its daemon, and installs this version there) when it didn't.
func vmCheck(name, socket string, err error, ready func(context.Context) error) doctor.Local {
	if err == nil {
		return doctor.Local{Check: api.DoctorCheck{ID: "vm", Title: "AgentBox's VM", Status: api.DoctorOK,
			Message: fmt.Sprintf("%s runs, and its daemon answers on %s", name, socket)}}
	}
	return doctor.Local{
		Check: api.DoctorCheck{ID: "vm", Title: "AgentBox's VM", Status: api.DoctorFail,
			Message: fmt.Sprintf("its daemon doesn't answer on %s: %s", socket, firstLineOf(err)), Fix: "agentbox vm start", AutoFix: true},
		Fix: ready,
	}
}

// virtualizationCheck is whether this machine can run AgentBox's VM: KVM on
// Linux, Lima or Apple's own on a Mac.
func virtualizationCheck() api.DoctorCheck {
	c := api.DoctorCheck{ID: "virtualization", Title: "Virtualization", Status: api.DoctorOK}
	switch {
	case runtime.GOOS == "darwin" && !useLima():
		c.Message = "Apple's Virtualization framework"
	case useLima():
		limactl, err := FindLimactl()
		if err != nil {
			c.Status, c.Message, c.Fix = api.DoctorFail, firstLineOf(err), "brew install lima"
			return c
		}
		c.Message = "Lima, at " + limactl
	default:
		return kvmCheck(kvm.System())
	}
	return c
}

// kvmCheck is whether this user can open /dev/kvm, and what to do when not:
// host setup for a permission or a module, the firmware for virtualization.
func kvmCheck(p kvm.Probe) api.DoctorCheck {
	c := api.DoctorCheck{ID: "virtualization", Title: "Virtualization", Status: api.DoctorOK, Message: "KVM: this user can open /dev/kvm"}
	switch kvm.Check(p) {
	case kvm.OK:
		return c
	case kvm.Denied:
		c.Message, c.Fix = "this user can't open /dev/kvm", kvm.Command
	case kvm.NoModule:
		c.Message, c.Fix = "KVM's kernel module isn't loaded", kvm.Command
	default:
		c.Message = "this computer doesn't offer virtualization"
		c.Fix = "turn on Intel VT-x or AMD-V in the firmware's settings (often under CPU or Advanced), or, in a VM, nested virtualization"
	}
	c.Status = api.DoctorFail
	return c
}

func firstLineOf(err error) string { return firstLine(err.Error()) }
