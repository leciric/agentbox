package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// StallPressure is the share of the last ten seconds, in percent, past which
// the host counts as stalling: when every runnable task was waiting on disk
// ("io full") or on memory ("memory full") for more than a tenth of the time.
// The user's desktop froze at io full ~35% and memory full ~19%.
const StallPressure = 10

// Pressure is the kernel's pressure stall information (PSI) for the host:
// the share of the last ten seconds, in percent, that some task ("some"), or
// every non-idle task at once ("full"), was stalled waiting on disk or memory.
// Full is what makes the desktop freeze: nothing could run at all.
type Pressure struct {
	IOSome     float64
	IOFull     float64
	MemorySome float64
	MemoryFull float64
}

// Stalling is whether the host is stalled for long enough to be felt.
func (p Pressure) Stalling() bool {
	return p.IOFull > StallPressure || p.MemoryFull > StallPressure
}

// ioBytes is bytes read and written, as counters to take deltas of.
type ioBytes struct{ read, write uint64 }

// ioRate is the rate, in bytes a second, between two readings taken seconds
// apart. A counter that went backwards (a disk removed, a cgroup recreated)
// gives 0 rather than a huge number.
func ioRate(before, after ioBytes, seconds float64) (read, write int64) {
	if seconds <= 0 {
		return 0, 0
	}
	if after.read >= before.read {
		read = int64(float64(after.read-before.read) / seconds)
	}
	if after.write >= before.write {
		write = int64(float64(after.write-before.write) / seconds)
	}
	return read, write
}

// sysRoot is where sysfs is mounted.
const sysRoot = "/sys"

// isPhysicalDisk is whether the block device major:minor is a physical disk:
// one with a device behind it. That leaves out partitions (already counted
// in their disk), device-mapper and md (which pass every byte on to a disk
// that counts it again, so LUKS or LVM would double the figure), and loop,
// zram and ram disks, which aren't the disk the desktop waits on. Named
// differently from budgetdisk.go's wholeDisk, which resolves a partition to
// the whole disk's name rather than testing one.
func isPhysicalDisk(sys, dev string) bool {
	_, err := os.Stat(filepath.Join(sys, "dev", "block", dev, "device"))
	return err == nil
}

// hostDiskIO sums what the host's physical disks have read and written since
// boot, from /proc/diskstats, whose sector counts are always 512 bytes.
func hostDiskIO(procDiskstats, sys string) (ioBytes, error) {
	b, err := os.ReadFile(procDiskstats)
	if err != nil {
		return ioBytes{}, err
	}
	var total ioBytes
	for _, line := range strings.Split(string(b), "\n") {
		// major minor name reads merged sectors-read ms writes merged sectors-written ...
		f := strings.Fields(line)
		if len(f) < 10 || !isPhysicalDisk(sys, f[0]+":"+f[1]) {
			continue
		}
		read, err1 := strconv.ParseUint(f[5], 10, 64)
		write, err2 := strconv.ParseUint(f[9], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		total.read += read * 512
		total.write += write * 512
	}
	return total, nil
}

// cgroupIO sums what a cgroup has read and written, from its io.stat:
//
//	259:4 rbytes=787185664 wbytes=1039511552 rios=12495 wios=62590 dbytes=0 dios=0
//	253:0 rbytes=787185664 wbytes=1039544320 rios=12495 wios=62593 dbytes=0 dios=0
//
// one line per device it has touched. The same bytes show up on a
// device-mapper device and on the disk under it, so only physical disks are
// counted, as on the host (isPhysicalDisk); when none of its lines is one (a pool
// on a loop device, say) every line is, since that's all there is to go on.
// ok is false when io.stat can't be read.
func cgroupIO(dir, sys string) (io ioBytes, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, "io.stat"))
	if err != nil {
		return ioBytes{}, false
	}
	var disks, all ioBytes
	sawDisk := false
	for _, line := range strings.Split(string(b), "\n") {
		dev, rest, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		var v ioBytes
		for _, kv := range strings.Fields(rest) {
			key, value, _ := strings.Cut(kv, "=")
			n, _ := strconv.ParseUint(value, 10, 64)
			switch key {
			case "rbytes":
				v.read = n
			case "wbytes":
				v.write = n
			}
		}
		all.read += v.read
		all.write += v.write
		if isPhysicalDisk(sys, dev) {
			sawDisk = true
			disks.read += v.read
			disks.write += v.write
		}
	}
	if sawDisk {
		return disks, true
	}
	return all, true
}

// hostPressure reads the host's io and memory PSI from /proc/pressure. ok is
// false when the kernel doesn't keep it (built without PSI, or psi=0).
func hostPressure(procPressure string) (p Pressure, ok bool) {
	io, err := os.ReadFile(filepath.Join(procPressure, "io"))
	if err != nil {
		return Pressure{}, false
	}
	memory, err := os.ReadFile(filepath.Join(procPressure, "memory"))
	if err != nil {
		return Pressure{}, false
	}
	p.IOSome, p.IOFull = psiAvg10(string(io))
	p.MemorySome, p.MemoryFull = psiAvg10(string(memory))
	return p, true
}

// psiAvg10 reads avg10 of both lines of a PSI file:
//
//	some avg10=73.46 avg60=60.34 avg300=33.04 total=525334082
//	full avg10=67.69 avg60=55.43 avg300=29.68 total=417403370
func psiAvg10(psi string) (some, full float64) {
	for _, line := range strings.Split(psi, "\n") {
		kind, rest, _ := strings.Cut(line, " ")
		for _, kv := range strings.Fields(rest) {
			if value, found := strings.CutPrefix(kv, "avg10="); found {
				v, _ := strconv.ParseFloat(value, 64)
				switch kind {
				case "some":
					some = v
				case "full":
					full = v
				}
			}
		}
	}
	return some, full
}
