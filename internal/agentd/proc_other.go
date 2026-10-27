//go:build !unix

package agentd

import (
	"os"
	"os/exec"
)

// ownGroup has nothing to set here: Windows starts a process in a job object
// when it is asked for one, and a pod runs Linux, so a command in this build
// gets no group of its own.
func ownGroup(cmd *exec.Cmd) {}

// killGroup ends the one process, because there is no group to name.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
