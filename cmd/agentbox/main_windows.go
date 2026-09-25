package main

import (
	"os"

	"agentbox/internal/hostwsl"
)

// version is set by the build, as cli.version is on Linux: the Linux side
// doesn't compile for Windows, so it can't be read from there.
var version = "dev"

// main is AgentBox's front end on Windows: AgentBox runs in a WSL distro, and
// this binary sets it up, relays the app to its daemon, and forwards every
// other command into it (D94).
func main() {
	os.Exit(hostwsl.Main(os.Args[1:], version))
}
