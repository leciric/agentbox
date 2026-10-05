//go:build !windows

package main

import (
	"os"

	"agentbox/internal/cli"
	"agentbox/internal/hostvm"
	"agentbox/internal/hostwsl"
)

func main() {
	// `agentbox machines` is this machine's own: its containers, for the AI
	// tools running here, with no daemon or VM involved.
	if len(os.Args) > 1 && os.Args[1] == "machines" {
		os.Exit(cli.Execute())
	}
	// `agentbox snap` and `agentbox browser-cookies` are the user's machine's
	// too, VM or not: the window they capture and the export they read are
	// here. They reach the daemon in the VM through its socket, forwarded to
	// its usual path, and never start one here.
	if len(os.Args) > 1 && (os.Args[1] == "snap" || os.Args[1] == "browser-cookies") && hostvm.Front() {
		_ = os.Setenv("AGENTBOX_NO_AUTOSTART", "1")
		os.Exit(cli.Execute())
	}
	// On a Mac, AgentBox runs in a Linux VM, and this binary is its front end;
	// on Linux too once `agentbox vm init` made one (Cloud Hypervisor). On a
	// Linux machine running AgentBox itself, `agentbox vm …` is still the
	// front end's: vm init is how the machine becomes one, and vm status says
	// which it is.
	// Help is this binary's own, the VM's being the same build: asking for it
	// never starts the VM.
	if hostvm.Handles(os.Args[1:]) && !hostvm.Help(os.Args[1:]) {
		os.Exit(hostvm.Main(os.Args[1:], cli.Version()))
	}
	// On Windows it runs in a WSL distro, and this is that front end.
	// AGENTBOX_FRONT_END=wsl makes it the Windows front end on Linux too: how
	// it is tested without Windows (internal/hostwsl/fakewsl).
	if hostwsl.Front() {
		os.Exit(hostwsl.Main(os.Args[1:], cli.Version()))
	}
	os.Exit(cli.Execute())
}
