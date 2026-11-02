package opencode

import (
	"fmt"
	"runtime"
	"strconv"
)

// Release is which OpenCode an installation runs.
//
// OpenCode 1 and OpenCode 2 both install a command called "opencode" and cannot
// sit side by side under it, so the base image keeps OpenCode 1 where it always
// was and each agent installs OpenCode 2 beside it as Command2. An installation chooses
// between them with state.SettingOpenCodeVersion, which is the transition: an
// installation that has not moved runs exactly what it ran before.
type Release int

const (
	// V1 is OpenCode 1, the `opencode` command the base image pins.
	V1 Release = 1
	// V2 is OpenCode 2, installed beside it as Command2 by each agent at its
	// first start on it (ensureOpenCode2InAgent, pinned by image.OpenCode2Version).
	V2 Release = 2
)

// Command2 is the name OpenCode 2 runs as beside OpenCode 1. OpenCode 2's npm
// platform package declares no `bin` entry, so ensureOpenCode2InAgent links its
// binary under this name; the lead's copy is linked the same way from
// AgentBox's tools directory.
const Command2 = "opencode2"

// Command is the command line this release runs as.
func (r Release) Command() string {
	if r == V2 {
		return Command2
	}
	return Command
}

// Autonomous is what an autonomous agent's terminal starts: the release's
// command with its "the machine is the sandbox" flag. OpenCode 1's is the TUI
// with --auto; OpenCode 2 keeps both the TUI and the flag.
func (r Release) Autonomous() string {
	return r.Command() + " --auto"
}

// String is "1" or "2", the form state.SettingOpenCodeVersion stores.
func (r Release) String() string { return strconv.Itoa(int(r)) }

// ReleaseOf reads a stored setting. Empty, or anything that is not "2", is V1:
// the release every installation ran before the setting existed.
func ReleaseOf(raw string) Release {
	if raw == V2.String() {
		return V2
	}
	return V1
}

// Package2 is the npm package that carries OpenCode 2's binary for this
// platform. The full @opencode/cli package installs a launcher and every
// platform's binary; this one is the binary itself, and declares no `bin`
// entry, so installing it beside OpenCode 1 does not replace OpenCode 1's
// `opencode` command.
func Package2() (string, error) {
	var os, arch string
	switch runtime.GOOS {
	case "linux":
		os = "linux"
	case "darwin":
		os = "darwin"
	case "windows":
		os = "windows"
	default:
		return "", fmt.Errorf("OpenCode 2 has no build for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("OpenCode 2 has no build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return "@opencode/cli-" + os + "-" + arch, nil
}
