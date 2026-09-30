package daemon

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"agentbox/internal/hostos"
)

// CheckDataDir refuses a daemon whose state would be the host's, seen through
// the home directory the host shares with AgentBox's VM. That is the host's
// own AgentBox data from before it moved into the VM: a daemon on it runs its
// sweeps over agents that are long gone, and its socket, which only the VM
// can reach, takes the place of the one the host forwards into the VM, so the
// app loses its daemon. Anything in the VM that runs agentbox with the host's
// HOME starts one, a lead's chat among them.
func CheckDataDir(data string) error {
	if !hostos.InVM() {
		return nil
	}
	return checkDataDir(data, onSharedFS)
}

func checkDataDir(data string, shared func(dir string) bool) error {
	if !shared(existingParent(data)) {
		return nil
	}
	// $HOME is what pointed here: the VM user's own home is its passwd entry's.
	home := "the VM user's home directory"
	if u, err := user.Current(); err == nil && u.HomeDir != "" && !shared(existingParent(u.HomeDir)) {
		home = u.HomeDir
	}
	return fmt.Errorf("%s is the host's AgentBox data, shared into AgentBox's VM: the VM's daemon keeps its own under %s, so run agentbox with that HOME", data, home)
}

// existingParent is dir, or the nearest directory above it that exists: a
// daemon about to make its data directory asks about where it would go.
func existingParent(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return dir
		}
		dir = up
	}
}
