//go:build !linux

package daemon

// sharedFolder says whether dir is in a folder shared from another machine:
// the daemon only runs agents on Linux.
func sharedFolder(string) bool { return false }
