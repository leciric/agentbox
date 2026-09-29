//go:build !linux

package tunnel

import "os/exec"

// setDeathSignal does nothing where there's no parent-death signal: the
// daemon runs on Linux, in a VM elsewhere.
func setDeathSignal(*exec.Cmd) {}
