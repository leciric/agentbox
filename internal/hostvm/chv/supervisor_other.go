//go:build !linux

package chv

import (
	"errors"
	"os"
	"syscall"
)

// The VM only runs on a Linux host; elsewhere this package only has to build.

var errLinuxOnly = errors.New("AgentBox's Cloud Hypervisor VM only runs on Linux")

func childAttr() *syscall.SysProcAttr    { return nil }
func detachedAttr() *syscall.SysProcAttr { return nil }
func lockFile(string) (func(), error)    { return nil, errLinuxOnly }
func supervisorAlive(int) bool           { return false }
func terminate(int) error                { return errLinuxOnly }
func allocated(fi os.FileInfo) int64     { return fi.Size() }
func hostMemory() int64                  { return 0 }
