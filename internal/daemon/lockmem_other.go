//go:build !linux

package daemon

// lockMemory is Linux's: the daemon runs in a Linux VM.
func lockMemory() error { return nil }
