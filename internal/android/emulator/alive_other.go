//go:build !unix

package emulator

// alive is true for any pid: emulators run in agents' Linux machines, and
// elsewhere a stale file only fails the call it leads to.
func alive(pid int) bool { return pid > 0 }
