package tunnel

import (
	"os/exec"
	"syscall"
)

// setDeathSignal ends cloudflared if the daemon dies without stopping it, so
// a crashed daemon doesn't leave the chat reachable from the internet.
func setDeathSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
