//go:build !windows

package main

import (
	"os"

	"agentbox/internal/cli"
	"agentbox/internal/hostvm"
	"agentbox/internal/hostwsl"
)

func main() {
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
