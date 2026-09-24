package daemon

import (
	"fmt"
	"regexp"
)

// windowsDrive matches where WSL mounts Windows's drives.
var windowsDrive = regexp.MustCompile(`^/mnt/[a-zA-Z](/|$)`)

// checkProjectDisk refuses, in WSL, a project on a Windows drive (D94). Every
// agent's worktree is made from it, and git through WSL's 9P mount of NTFS is
// many times slower than on the distro's own disk, with file modes and
// symlinks that don't survive the trip: the repository has to live in Linux.
// AddProjectRequest.CopyToLinux gets past it by adding a clone in ~/src.
func checkProjectDisk(root string, wsl bool) error {
	if !wsl || !windowsDrive.MatchString(root) {
		return nil
	}
	return fmt.Errorf(`%s is on a Windows drive, and AgentBox's projects have to be on WSL's own disk, where git is fast and file modes and symlinks work. Copy it there: agentbox add --copy <path> (or Add project in the app), which clones what's committed into ~/src/<name>; or clone it yourself with git clone <its URL> ~/src/<name> and agentbox add ~/src/<name>. The folder is \\wsl.localhost\<distro>\home\<you>\src in Windows's Explorer`, root)
}
