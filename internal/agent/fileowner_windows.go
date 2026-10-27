//go:build windows

package agent

import "io/fs"

// ownedByUser has no owner to read on Windows, and the cgroup it is asked about
// is a Linux one: a Windows host never reaches this code.
func ownedByUser(fs.FileInfo, int) bool { return false }
