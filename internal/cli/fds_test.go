package cli

import (
	"syscall"
	"testing"
)

func TestCloseInheritedFilesMarksDescriptorsCloseOnExec(t *testing.T) {
	// syscall.Pipe, unlike os.Pipe, leaves the descriptors inheritable, like the
	// ones Electron passes to the processes it starts.
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(p[0]) }()
	defer func() { _ = syscall.Close(p[1]) }()

	closeInheritedFiles()
	for _, fd := range p {
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno != 0 {
			t.Fatal(errno)
		}
		if flags&syscall.FD_CLOEXEC == 0 {
			t.Errorf("descriptor %d would be inherited by the daemon", fd)
		}
	}
}
