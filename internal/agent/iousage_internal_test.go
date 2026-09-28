package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSys makes a sysfs with /sys/dev/block/<dev> for each device, and a
// device link under the ones that are physical disks.
func fakeSys(t *testing.T, disks, virtual []string) string {
	t.Helper()
	sys := t.TempDir()
	for _, dev := range disks {
		if err := os.MkdirAll(filepath.Join(sys, "dev", "block", dev, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dev := range virtual {
		if err := os.MkdirAll(filepath.Join(sys, "dev", "block", dev), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return sys
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The layout of the machine this was written on: an NVMe disk holding a LUKS
// partition (dm-0), a second disk, and zram swap. Only the two disks count.
func TestHostDiskIOCountsWholeDisksOnly(t *testing.T) {
	sys := fakeSys(t, []string{"259:0", "259:4"}, []string{"259:1", "259:5", "259:6", "253:0", "252:0", "7:0"})
	stats := writeFile(t, filepath.Join(t.TempDir(), "diskstats"), `
 259       0 nvme1n1 14824 0 1000 10 5 0 2000 1 0 0 0 0 0 0 0 0 0
 259       1 nvme1n1p1 2736 0 1000 10 5 0 2000 1 0 0 0 0 0 0 0 0 0
 259       4 nvme0n1 931 0 10000 10 5 0 20000 1 0 0 0 0 0 0 0 0 0
 259       5 nvme0n1p1 24 0 500 10 5 0 500 1 0 0 0 0 0 0 0 0 0
 259       6 nvme0n1p2 931 0 9500 10 5 0 19500 1 0 0 0 0 0 0 0 0 0
 253       0 dm-0 931 0 9500 10 5 0 19500 1 0 0 0 0 0 0 0 0 0
 252       0 zram0 394 0 777777 10 5 0 777777 1 0 0 0 0 0 0 0 0 0
   7       0 loop0 3 0 66 10 5 0 66 1 0 0 0 0 0 0 0 0 0
`)
	got, err := hostDiskIO(stats, sys)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ioBytes{read: (1000 + 10000) * 512, write: (2000 + 20000) * 512}); got != want {
		t.Errorf("hostDiskIO = %+v, want %+v", got, want)
	}
	if _, err := hostDiskIO(filepath.Join(t.TempDir(), "missing"), sys); err == nil {
		t.Error("hostDiskIO of a missing file: want an error")
	}
}

func TestCgroupIOLeavesOutDeviceMapperAndFallsBackWithoutADisk(t *testing.T) {
	sys := fakeSys(t, []string{"259:4"}, []string{"253:0", "252:0", "7:3"})

	// The same bytes on dm-0 and the disk under it: counted once.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "io.stat"), `252:0 rbytes=0 wbytes=45056 rios=0 wios=11 dbytes=0 dios=0
259:4 rbytes=787185664 wbytes=1039511552 rios=12495 wios=62590 dbytes=0 dios=0
253:0 rbytes=787185664 wbytes=1039544320 rios=12495 wios=62593 dbytes=0 dios=0
`)
	got, ok := cgroupIO(dir, sys)
	if want := (ioBytes{read: 787185664, write: 1039511552}); !ok || got != want {
		t.Errorf("cgroupIO = %+v, %v; want %+v", got, ok, want)
	}

	// A pool on a loop device: no disk among its lines, so they all count.
	loop := t.TempDir()
	writeFile(t, filepath.Join(loop, "io.stat"), "7:3 rbytes=100 wbytes=200 rios=1 wios=2 dbytes=0 dios=0\n")
	if got, ok := cgroupIO(loop, sys); !ok || got != (ioBytes{read: 100, write: 200}) {
		t.Errorf("cgroupIO on loop = %+v, %v", got, ok)
	}

	if _, ok := cgroupIO(t.TempDir(), sys); ok {
		t.Error("cgroupIO without io.stat: want ok false")
	}
}

func TestIORateIsBytesPerSecondAndNeverNegative(t *testing.T) {
	read, write := ioRate(ioBytes{read: 1000, write: 5000}, ioBytes{read: 3000, write: 5000}, 0.5)
	if read != 4000 || write != 0 {
		t.Errorf("ioRate = %d, %d; want 4000, 0", read, write)
	}
	if read, write := ioRate(ioBytes{read: 9, write: 9}, ioBytes{read: 1, write: 1}, 1); read != 0 || write != 0 {
		t.Errorf("ioRate of counters that went backwards = %d, %d; want 0, 0", read, write)
	}
	if read, _ := ioRate(ioBytes{}, ioBytes{read: 10}, 0); read != 0 {
		t.Errorf("ioRate over no time = %d, want 0", read)
	}
}

func TestHostPressureReadsAvg10AndFlagsAStall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "io"), `some avg10=73.46 avg60=60.34 avg300=33.04 total=525334082
full avg10=35.10 avg60=55.43 avg300=29.68 total=417403370
`)
	writeFile(t, filepath.Join(dir, "memory"), `some avg10=25.00 avg60=0.00 avg300=0.00 total=0
full avg10=19.00 avg60=0.00 avg300=0.00 total=0
`)
	p, ok := hostPressure(dir)
	if want := (Pressure{IOSome: 73.46, IOFull: 35.1, MemorySome: 25, MemoryFull: 19}); !ok || p != want {
		t.Fatalf("hostPressure = %+v, %v; want %+v", p, ok, want)
	}
	if !p.Stalling() {
		t.Error("io full 35%, memory full 19%: want Stalling")
	}
	// A busy disk alone isn't a stall: some is high, full isn't.
	if (Pressure{IOSome: 80, IOFull: 9, MemorySome: 50, MemoryFull: 10}).Stalling() {
		t.Error("full at or under 10%: want no Stalling")
	}
	if _, ok := hostPressure(t.TempDir()); ok {
		t.Error("hostPressure without PSI: want ok false")
	}
}
