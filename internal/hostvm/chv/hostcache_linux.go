package chv

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// directIO reports whether Cloud Hypervisor can open the disk image at path
// with O_DIRECT (direct=on) without the guest seeing its disk change. With
// direct=on it asks the kernel for the file's O_DIRECT alignment
// (statx STATX_DIOALIGN), takes 512 bytes when the kernel doesn't say, and
// makes that the disk's logical block size. The VM's disks were partitioned
// and formatted with 512-byte sectors, so a file that wants 4 KiB (a 4Kn
// drive) would leave the guest looking for its partition table in the wrong
// place and not boot: only where the kernel says 512, or, where it doesn't
// say (btrfs on 6.12), where a 512-byte direct read works. And on btrfs only
// without copy on write (config.go makes the images so): a file with data
// checksums gets checksum errors when the guest changes a buffer while it's
// being written, which it may.
func directIO(path string) bool {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECT|unix.O_CLOEXEC, 0)
	if err != nil {
		return false // tmpfs before 6.6 and some FUSE filesystems refuse it
	}
	defer func() { _ = unix.Close(fd) }()
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) == nil && fs.Type == unix.BTRFS_SUPER_MAGIC {
		if flags, err := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS); err != nil || flags&fsNoCOWFlag == 0 {
			return false
		}
	}
	var st unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_DIOALIGN, &st); err == nil &&
		st.Mask&unix.STATX_DIOALIGN != 0 && st.Dio_mem_align > 0 {
		return max(st.Dio_mem_align, st.Dio_offset_align) == 512
	}
	buf, err := unix.Mmap(-1, 0, os.Getpagesize(), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return false
	}
	defer func() { _ = unix.Munmap(buf) }()
	_, err = unix.Pread(fd, buf[:512], 512)
	return err == nil
}

// imageCached is how much of the file at path is in the host's page cache:
// what the host keeps of the VM's disk on top of the guest's own cache of
// it. It looks only at the file's data, not its holes, a GiB at a time.
func imageCached(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return 0
	}
	fd, size := int(f.Fd()), fi.Size()
	page := int64(os.Getpagesize())
	const chunk = 1 << 30
	vec := make([]byte, chunk/page)
	var cached int64
	for off := int64(0); off < size; {
		data, err := unix.Seek(fd, off, unix.SEEK_DATA)
		if err != nil {
			break // ENXIO: no data past off
		}
		hole, err := unix.Seek(fd, data, unix.SEEK_HOLE)
		if err != nil {
			hole = size
		}
		for at := data / page * page; at < hole; at += chunk {
			n := min(chunk, hole-at)
			m, err := unix.Mmap(fd, at, int(n), unix.PROT_READ, unix.MAP_SHARED)
			if err != nil {
				return cached
			}
			v := vec[:(n+page-1)/page]
			if _, _, errno := unix.Syscall(unix.SYS_MINCORE, uintptr(unsafe.Pointer(&m[0])), uintptr(len(m)), uintptr(unsafe.Pointer(&v[0]))); errno == 0 {
				for _, b := range v {
					cached += int64(b&1) * page
				}
			}
			_ = unix.Munmap(m)
		}
		off = hole
	}
	return cached
}

// dropImageCache asks the host to drop what it caches of the file at path.
// Only clean pages go; what the VM wrote and the host hasn't yet stays until
// it's written back.
func dropImageCache(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}

// hostMemSample is the host's own memory: its MemTotal and MemAvailable,
// and the share of the last 10 s some task stalled on memory (PSI "some
// avg10"), in percent.
func hostMemSample() (total, available int64, pressure float64) {
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for line := range strings.Lines(string(b)) {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			switch f[0] {
			case "MemTotal:":
				total = kb << 10
			case "MemAvailable:":
				available = kb << 10
			}
		}
	}
	if b, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		pressure = psiSome(string(b))
	}
	return total, available, pressure
}

// footprint is what the processes pids hold of the host's memory together:
// their anonymous and shared memory, each page counted once however many of
// them map it. The VM's memory is one shared memfd that Cloud Hypervisor,
// virtiofsd, passt and crosvm all map, so adding up their resident sizes
// counts it up to four times; their proportional sizes (PSS) add up to it
// once. Reading them walks each process's page tables, so it isn't for
// every tick.
func footprint(pids []int) int64 {
	var total int64
	for _, pid := range pids {
		f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
		if err != nil {
			continue
		}
		total += pssOf(f)
		_ = f.Close()
	}
	return total
}

// pssOf reads smaps_rollup's anonymous and shared proportional sizes.
func pssOf(r io.Reader) int64 {
	var n int64
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && (f[0] == "Pss_Anon:" || f[0] == "Pss_Shmem:") {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			n += kb << 10
		}
	}
	return n
}
