// Package hostos tells AgentBox's Linux side whether it runs on a machine of
// its own or in a Linux VM on another OS, and what it needs to know about that
// OS. It is its own package so the daemon's packages can ask without importing
// the front ends that make those VMs (package hostwsl on Windows).
//
// This is the part of package hostos the macOS port (package hostvm, D92)
// and the Windows port share: Env and HomeEnv are its, and OS adds WSL2, which
// a machine can be found to be without anyone setting Env.
package hostos

import (
	"os"
	"strings"
)

const (
	// Env is set on every command a front end runs in its VM, to the host's
	// OS: the Linux side knows it isn't on a machine of its own.
	Env = "AGENTBOX_HOST_OS"
	// HomeEnv is the host user's home directory, when the VM shares it at the
	// same path. It isn't the VM user's home: that is the VM's own.
	HomeEnv = "AGENTBOX_HOST_HOME"

	// Windows is OS's answer inside WSL2 (package hostwsl, D94).
	Windows = "windows"
	// Linux is Env's value in the Cloud Hypervisor VM a Linux host runs
	// AgentBox in (package hostvm/chv): a Linux front end, which is still a
	// VM whose agents the host can't reach at their own addresses.
	Linux = "linux"
)

// Forwarded reports whether a variable of the front end's environment goes to
// the Linux side's agentbox: AgentBox's own settings, and DO_NOT_TRACK, which
// the daemon's update check honours (internal/update) and which would
// otherwise be set on the Mac or in Windows and never reach it.
func Forwarded(name string) bool {
	return strings.HasPrefix(name, "AGENTBOX_") || name == "DO_NOT_TRACK"
}

// osrelease is where the kernel says what it is: WSL2's kernel is Microsoft's,
// and says so ("6.6.87.2-microsoft-standard-WSL2"). A variable for tests.
var osrelease = "/proc/sys/kernel/osrelease"

// markers are the files that say this is AgentBox's own VM, to an agentbox
// run in it without the front end's Env: by hand, as root, from a unit. The
// first holds the host's OS: cloud-init writes it in a Cloud Hypervisor VM
// (internal/hostvm/chv), Lima's provisioning in a Mac's. The second is only
// in a Cloud Hypervisor VM, and in every one, made before the first was.
// Variables for tests.
var markers = []struct{ file, os string }{
	{"/etc/agentbox/vm", ""},
	{"/etc/agentbox/host-guard.nft", Linux},
}

// OS is the OS of the computer this Linux runs in a VM on: what Env says, or
// "windows" in WSL2 whether or not anyone said so, since a shell opened in the
// distro runs agentbox without the front end, or what AgentBox's VM says it
// is on (markers), whoever runs agentbox in it. "" on a Linux machine of its
// own.
func OS() string {
	if v := os.Getenv(Env); v != "" {
		return v
	}
	if WSL() {
		return Windows
	}
	for _, m := range markers {
		b, err := os.ReadFile(m.file)
		if err != nil {
			continue
		}
		if m.os != "" {
			return m.os
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
		return Linux
	}
	return ""
}

// WSL reports whether this is WSL2's kernel.
func WSL() bool {
	b, err := os.ReadFile(osrelease)
	if err != nil {
		return false
	}
	release := strings.ToLower(string(b))
	return strings.Contains(release, "microsoft") || strings.Contains(release, "wsl")
}

// InVM reports whether this process runs in a VM on another OS. The daemon
// uses it to say what such a machine can't do, such as reach an agent at its
// own address, which is inside the VM.
func InVM() bool { return OS() != "" }

// Home is the host user's home directory, shared into the VM, or "" when this
// isn't a VM or the front end didn't say.
func Home() string {
	if !InVM() {
		return ""
	}
	return os.Getenv(HomeEnv)
}

// Name is how a brief or an error names the user's computer: "Windows", "a
// Mac" or "a Linux host", or "" on a Linux machine of its own.
func Name() string {
	switch OS() {
	case "":
		return ""
	case Windows:
		return "Windows"
	case "darwin":
		return "a Mac"
	case Linux:
		return "a Linux host"
	default:
		return OS()
	}
}
