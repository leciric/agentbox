//go:build !windows

package main

import (
	"os"

	"agentbox/internal/cli"
	"agentbox/internal/hostvm"
	"agentbox/internal/hostwsl"
)

func main() {
	// On a Mac, AgentBox runs in a Linux VM, and this binary is its front end.
	if hostvm.Front() {
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
