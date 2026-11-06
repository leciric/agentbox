package daemon

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// lockMemory locks the daemon's own code and data into memory: every mapping
// of its executable, read in whole now (protectDaemon). Not its heap.
//
// It was mlockall(MCL_CURRENT|MCL_FUTURE|MCL_ONFAULT), which locked every
// page the daemon ever touched. The kernel refuses madvise(MADV_DONTNEED) on
// a locked page (EINVAL), and that is how Go gives the heap's free pages
// back: the runtime ignores the error, believes them returned, and the
// daemon's RSS only ever went up, to the highest its heap had been. A day's
// bursts (a long transcript read, a big diff) left it at 700 MB with a
// 256 MiB soft limit, and none of it could even go to zram.
func lockMemory() error {
	exe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}
	ranges, err := mappingsOf("/proc/self/maps", exe)
	if err != nil {
		return err
	}
	if len(ranges) == 0 {
		return fmt.Errorf("no mapping of %s in /proc/self/maps", exe)
	}
	for _, r := range ranges {
		// mlock(2) by address: the ranges are the kernel's, not Go memory.
		if _, _, errno := unix.Syscall(unix.SYS_MLOCK, r.start, r.end-r.start, 0); errno != 0 {
			return fmt.Errorf("locking %x-%x of %s: %w", r.start, r.end, exe, errno)
		}
	}
	return nil
}

type addrRange struct{ start, end uintptr }

// mappingsOf reads the address ranges a maps file (proc(5)) gives path.
func mappingsOf(maps, path string) ([]addrRange, error) {
	f, err := os.Open(maps)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []addrRange
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// address perms offset dev inode pathname
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 || strings.Join(fields[5:], " ") != path {
			continue
		}
		lo, hi, ok := strings.Cut(fields[0], "-")
		if !ok {
			continue
		}
		start, err1 := strconv.ParseUint(lo, 16, 64)
		end, err2 := strconv.ParseUint(hi, 16, 64)
		if err1 != nil || err2 != nil || end <= start {
			continue
		}
		out = append(out, addrRange{uintptr(start), uintptr(end)})
	}
	return out, sc.Err()
}
