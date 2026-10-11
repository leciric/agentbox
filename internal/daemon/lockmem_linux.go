package daemon

import "golang.org/x/sys/unix"

// lockMemory locks what the daemon has mapped into memory now, and each page
// it maps later as it is first touched (protectDaemon).
func lockMemory() error {
	if err := unix.Mlockall(unix.MCL_CURRENT); err != nil {
		return err
	}
	return unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE | unix.MCL_ONFAULT)
}
