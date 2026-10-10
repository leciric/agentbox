//go:build !unix

package incus

// CanOpenSocket is false elsewhere: Incus, and the daemon asking, only run on
// Linux.
func CanOpenSocket() bool { return false }
