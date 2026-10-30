// Package kvm is whether the user who runs AgentBox can use /dev/kvm, which
// AgentBox's Cloud Hypervisor VM needs on Linux, and, as root, giving them it.
//
// Most distributions give /dev/kvm to everyone (systemd's default, 0666:
// Arch, Fedora), but Debian and Ubuntu build systemd with 0660 and the kvm
// group, so a new user there can't open it until they join the group and log
// in again. Grant does it without the new login, with one password prompt
// from the app (pkexec agentbox host kvm):
//
//   - an ACL entry on /dev/kvm for the user's UID, which counts at once, for
//     every process they already run, the app and its daemon included;
//   - membership of the kvm group where there is one, for every later login
//     of any kind (desktop, SSH, a systemd user service), since the ACL lasts
//     only as long as the device node, until the next boot;
//   - where there's no kvm group, a udev rule tagging /dev/kvm uaccess, which
//     has logind give the device to whoever is at the seat on each boot;
//   - when there's no /dev/kvm at all but the CPU can virtualize, the kvm_intel
//     or kvm_amd module, loaded now and at every boot.
//
// Rejected: running the VM under `sg kvm`, which works only once the user is in
// the group, wraps every process that starts the VM, and does nothing for an
// /dev/kvm without a group; and a uaccess rule alone, which is only for a
// local seat's active session, so neither an SSH login nor a second user
// switched to on the same seat would keep it.
package kvm

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
)

// Device is the device the VM needs.
const Device = "/dev/kvm"

// Command is how a terminal grants it: what the app runs through pkexec.
const Command = "sudo agentbox host kvm"

// State is what Check found.
type State string

const (
	// OK: this user can open /dev/kvm.
	OK State = "ok"
	// Denied: /dev/kvm is there and this user can't open it. Grant fixes it.
	Denied State = "denied"
	// NoModule: there's no /dev/kvm, but the CPU can virtualize: KVM's
	// module isn't loaded. Grant loads it.
	NoModule State = "module"
	// NoVirt: there's no /dev/kvm and the CPU doesn't say it can virtualize:
	// it's off in the firmware, or this is a VM without nested
	// virtualization. Only the user can change that.
	NoVirt State = "novirt"
)

// Probe is what Check looks at, so tests can give it a machine of their own.
type Probe struct {
	Open    func() error           // opens /dev/kvm read-write, as this user, and closes it
	Stat    func() error           // stats /dev/kvm
	CPUInfo func() ([]byte, error) // /proc/cpuinfo
}

// System is this machine's Probe.
func System() Probe {
	return Probe{
		Open: func() error {
			f, err := os.OpenFile(Device, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			return f.Close()
		},
		Stat:    func() error { _, err := os.Stat(Device); return err },
		CPUInfo: func() ([]byte, error) { return os.ReadFile("/proc/cpuinfo") },
	}
}

// Check says whether this user can use KVM, and if not, why.
func Check(p Probe) State {
	if p.Open() == nil {
		return OK
	}
	// Anything but a missing device (EACCES, EPERM, a node only root may
	// read) is a permission to give.
	if err := p.Stat(); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return Denied
	}
	if Module(p) != "" {
		return NoModule
	}
	return NoVirt
}

// Module is the KVM module for this CPU, from its flags: kvm_intel for VT-x
// (vmx), kvm_amd for AMD-V (svm), or "" when it shows neither. Since Linux
// 5.6 the kernel hides vmx when the firmware has turned VT-x off.
func Module(p Probe) string {
	info, err := p.CPUInfo()
	if err != nil {
		return ""
	}
	for line := range bytes.Lines(info) {
		key, value, ok := bytes.Cut(line, []byte(":"))
		if !ok || string(bytes.TrimSpace(key)) != "flags" {
			continue
		}
		for _, flag := range bytes.Fields(value) {
			switch string(flag) {
			case "vmx":
				return "kvm_intel"
			case "svm":
				return "kvm_amd"
			}
		}
		return "" // every core has the same flags
	}
	return ""
}

// Error is why this user can't run the VM, and what to do, for a State that
// isn't OK; nil for OK.
func Error(s State, cause error) error {
	var msg string
	switch s {
	case OK:
		return nil
	case Denied:
		msg = "the VM needs /dev/kvm, which this user can't open: " + Command + " gives you access, with no new login (the app's Setup does it with one password prompt)"
	case NoModule:
		msg = "the VM needs /dev/kvm, and KVM's module isn't loaded: " + Command + " loads it and gives you access (the app's Setup does it with one password prompt)"
	default:
		msg = "the VM needs /dev/kvm, and this computer doesn't offer virtualization: turn it on in the firmware's settings (Intel VT-x or AMD-V, often under CPU or Advanced), or, in a VM, turn on nested virtualization"
	}
	if cause != nil {
		return errors.New(msg + " (" + cause.Error() + ")")
	}
	return errors.New(msg)
}
