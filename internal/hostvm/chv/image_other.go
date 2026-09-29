//go:build !linux

package chv

// setNoCOW is only for Linux's btrfs; the VM only runs on Linux.
func setNoCOW(string) error { return nil }
