package nodemods

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// errNoClone is the file system can't clone a file (ext4): copyTree copies
// instead.
var errNoClone = errors.New("cloning isn't supported here")

// cloneFile makes dst a clone of src, sharing its data until either is
// written: btrfs and XFS can.
func cloneFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	err = unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	cerr := out.Close()
	if err != nil {
		_ = os.Remove(dst)
		if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
			return errNoClone
		}
		return err
	}
	if cerr != nil {
		return cerr
	}
	// A new file has the umask's mode, and the clone the source's times.
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

func inode(info fs.FileInfo) ([2]uint64, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return [2]uint64{uint64(st.Dev), st.Ino}, true
	}
	return [2]uint64{}, false
}

func diskSize(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return info.Size()
}
