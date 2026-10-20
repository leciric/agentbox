package chv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// RunGPUDevice is `agentbox vm gpu-device NODE -- COMMAND...`, crosvm's GPU
// device as the supervisor starts it when the host has more than one render
// node: in a user and mount namespace of its own (gpuDeviceAttr), as root of
// it, it puts a /dev/dri holding only NODE over the host's and runs COMMAND,
// which stays root of the namespace and so the user outside it.
//
// virglrenderer takes the first render node it finds for GL, and can't be
// told another (rutabaga 0.1.75's get_drm_fd drops every render node it's
// given: Path::starts_with("/dev/dri/renderD") compares whole components),
// and Venus's render server lists every GPU it can open: with one node
// there, GL and Vulkan both run on the GPU the VM is to have. try.sh does the
// same with lib/gpu-ns.sh.
func RunGPUDevice(node string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command to run")
	}
	dri := filepath.Dir(node)
	// What's mounted here stays here.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("making the mounts private (needs a mount namespace): %w", err)
	}
	dir, err := os.MkdirTemp("", "agentbox-dri-")
	if err != nil {
		return err
	}
	if err := unix.Mount("tmpfs", dir, "tmpfs", 0, "mode=755"); err != nil {
		return fmt.Errorf("mounting a tmpfs for %s: %w", dri, err)
	}
	only := filepath.Join(dir, filepath.Base(node))
	if err := os.WriteFile(only, nil, 0o600); err != nil {
		return err
	}
	if err := unix.Mount(node, only, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("binding %s: %w", node, err)
	}
	if err := unix.Mount(dir, dri, "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("putting it over %s: %w", dri, err)
	}
	_ = os.Remove(dir)
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}

// gpuDeviceAttr starts `agentbox vm gpu-device` in a user and mount namespace
// of its own, as root of it: the user, outside.
func gpuDeviceAttr() *syscall.SysProcAttr {
	attr := childAttr()
	attr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS
	attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}
	attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}
	attr.GidMappingsEnableSetgroups = false
	return attr
}
