package pressure

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const swapsFile = `Filename				Type		Size		Used		Priority
/dev/zram0                              partition	2097148		1048574		100
/swapfile                               file		4194300		1048576		-2
`

func TestParseSwapsLeavesZramOut(t *testing.T) {
	total, free := parseSwaps([]byte(swapsFile))
	if total != 4194300<<10 || free != (4194300-1048576)<<10 {
		t.Errorf("disk swap read as %d total, %d free", total, free)
	}
	if total, free := parseSwaps([]byte("Filename Type Size Used Priority\n/dev/zram0 partition 100 50 100\n")); total != 0 || free != 0 {
		t.Errorf("zram alone read as disk swap: %d, %d", total, free)
	}
}

func TestSamplerReadsDiskSwap(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	s, err := OpenSampler(
		write("psi", "some avg10=1.00 avg60=1.00 avg300=1.00 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=1\n"),
		write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 900 kB\nSwapTotal: 6291448 kB\nSwapFree: 4194298 kB\n"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	s.ReadSwaps(write("swaps", swapsFile))
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.Mem.DiskSwapUsed() != 1048576<<10 || !got.Mem.DiskSwapOut() {
		t.Errorf("disk swap used %d, out %v", got.Mem.DiskSwapUsed(), got.Mem.DiskSwapOut())
	}
	if n := testing.AllocsPerRun(100, func() { _, _ = s.Read() }); n != 0 {
		t.Errorf("a reading allocates %v times, want none", n)
	}
}

// zramVM is a VM at its cap with 4 GiB of zram, half used, and a 4 GiB
// swapfile with diskUsed in use.
func zramVM(avail, diskUsed int64) Mem {
	return Mem{
		Total: 16 * gb, Available: avail,
		SwapTotal: 8 * gb, SwapFree: 2*gb + 4*gb - diskUsed,
		DiskSwapTotal: 4 * gb, DiskSwapFree: 4*gb - diskUsed,
	}
}

func TestDiskSwapThresholdMakesZramTheLimit(t *testing.T) {
	// Under the tolerance, the swapfile is ignored: zram is 50% used.
	m := zramVM(gb, DiskSwapTolerance-1)
	if got := m.SwapUsed(); got < 0.49 || got > 0.51 {
		t.Errorf("swap used %.2f under the tolerance, want zram's 0.5", got)
	}
	if m.DiskSwapOut() {
		t.Error("a few pages on disk counted as out")
	}
	// At it, the swap counts as full though the swapfile is nearly empty.
	m = zramVM(gb, DiskSwapTolerance)
	if got := m.SwapUsed(); got != 1 || !m.DiskSwapOut() {
		t.Errorf("swap used %.2f at the tolerance, want 1", got)
	}
	// Without a swapfile nothing changes.
	m = Mem{Total: 16 * gb, SwapTotal: 4 * gb, SwapFree: gb}
	if got := m.SwapUsed(); got != 0.75 {
		t.Errorf("zram alone %.2f used, want 0.75", got)
	}
}

func TestDiskSwapInUseIsOutWhereZramHasRoom(t *testing.T) {
	p := DefaultPolicy
	low := zramVM(int64(p.floor(zramVM(0, 0))/2), 0)
	if h := p.Headroom(low, Trend{}); h != Room {
		t.Fatalf("under the floor with zram half empty is %v, want Room", h)
	}
	low = zramVM(low.Available, 2*gb)
	if h, why := p.Judge(low, Trend{}); h != Out || why != Low {
		t.Errorf("under the floor with 2 GiB paged to disk is %v, %v, want Out, Low", h, why)
	}
	// Plenty available: disk swap alone holds nothing.
	roomy := zramVM(8*gb, 2*gb)
	if h := p.Headroom(roomy, Trend{}); h != Room {
		t.Errorf("8 GiB available with disk swap used is %v, want Room", h)
	}
}

func TestAnAgentDoesntBootWhileTheVMPagesToDisk(t *testing.T) {
	f := NewFleet(DefaultPolicy)
	f.Sync(t0, machines("p/a"))
	f.Ask("p/e", t0)
	// 8 GiB available is plenty for a boot, but 2 GiB is on the disk swap.
	if got := f.Step(t0, Sample{Mem: zramVM(8*gb, 2*gb)}); len(got.Start) != 0 {
		t.Fatalf("started %v with the VM paging to disk", got.Start)
	}
	if !f.SwapFull() {
		t.Error("swap not full with 2 GiB paged to disk")
	}
	// Once the swapfile is drained (below the tolerance) it boots.
	if got := f.Step(t0.Add(5*time.Second), Sample{Mem: zramVM(8*gb, 0)}); !slices.Equal(got.Start, []string{"p/e"}) {
		t.Fatalf("started %v with the swapfile empty", got.Start)
	}
}

func TestSampleSaysWhenMemoryIsOnTheDiskSwap(t *testing.T) {
	if got := (Sample{Mem: zramVM(8*gb, 2*gb)}).String(); !strings.Contains(got, "2.0G paged to the disk swap") {
		t.Errorf("the log says %q, want it to name the disk swap", got)
	}
	if got := (Sample{Mem: zramVM(8*gb, 0)}).String(); strings.Contains(got, "disk swap") {
		t.Errorf("the log says %q with the swapfile empty", got)
	}
}
