//go:build !linux

package chv

import (
	"errors"
	"syscall"
)

// RunGPUDevice is Linux's: the GPU device runs only beside Cloud Hypervisor.
func RunGPUDevice(string, []string) error {
	return errors.New("the VM's GPU device runs only on Linux")
}

func gpuDeviceAttr() *syscall.SysProcAttr { return nil }
