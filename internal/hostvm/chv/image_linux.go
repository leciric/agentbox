package chv

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// fsNoCOWFlag is FS_NOCOW_FL (chattr +C), which x/sys doesn't name.
const fsNoCOWFlag = 0x00800000

// setNoCOW turns copy on write off for dir, so every file made in it after
// is made without it: a disk image on btrfs with copy on write fragments into
// a great many extents as the VM writes to it. Other filesystems have no such
// flag, and are left alone.
func setNoCOW(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fd := int(f.Fd())
	flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		if unsupported(err) {
			return nil
		}
		return err
	}
	if flags&fsNoCOWFlag != 0 {
		return nil
	}
	flags |= fsNoCOWFlag
	if err := unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(flags)); err != nil && !unsupported(err) {
		return err
	}
	return nil
}

func unsupported(err error) bool {
	return errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS)
}
