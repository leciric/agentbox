package pressure

import (
	"os"
	"path/filepath"
	"testing"
)

const meminfo = `MemTotal:       18664412 kB
MemFree:          401220 kB
MemAvailable:    6186772 kB
Buffers:            2048 kB
SwapCached:        10240 kB
SwapTotal:       4718588 kB
SwapFree:              0 kB
`

func TestSamplerReadsPressureAndHeadroom(t *testing.T) {
	dir := t.TempDir()
	psiFile, memFile := filepath.Join(dir, "memory"), filepath.Join(dir, "meminfo")
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(psiFile, "some avg10=11.20 avg60=3.10 avg300=0.80 total=46356041310\nfull avg10=10.70 avg60=2.90 avg300=0.71 total=40694621386\n")
	write(memFile, meminfo)
	s, err := OpenSampler(psiFile, memFile, 18<<30)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.Some.Avg10 != 11.2 || got.Full.Avg10 != 10.7 || got.Full.Avg60 != 2.9 || got.Some.Total != 46356041310 {
		t.Errorf("PSI read as %+v", got.PSI)
	}
	if got.Mem.Total != 18664412<<10 || got.Mem.Available != 6186772<<10 || got.Mem.SwapTotal != 4718588<<10 || got.Mem.SwapFree != 0 || got.Mem.Cap != 18<<30 {
		t.Errorf("meminfo read as %+v", got.Mem)
	}
	if !got.Mem.AtCap() || DefaultPolicy.Headroom(got.Mem) != Short {
		t.Errorf("33%% available at its cap with swap full is %d, want Short", DefaultPolicy.Headroom(got.Mem))
	}
	// The file changes; the sampler reads it afresh, from the same open file.
	write(memFile, "MemTotal: 1000 kB\nMemAvailable: 900 kB\n")
	if got, _ := s.Read(); got.Mem.Available != 900<<10 {
		t.Errorf("read again: %+v", got.Mem)
	}
	if n := testing.AllocsPerRun(100, func() { _, _ = s.Read() }); n != 0 {
		t.Errorf("a reading allocates %v times, want none", n)
	}
}

func TestSamplerReadsTheKernelsFiles(t *testing.T) {
	s, err := OpenSampler(File, MemInfo, 0)
	if err != nil {
		t.Skipf("no PSI here: %v", err)
	}
	defer func() { _ = s.Close() }()
	for range 2 {
		got, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if !got.Mem.Known() || got.Mem.Available <= 0 || !got.Mem.AtCap() {
			t.Fatalf("read %+v", got)
		}
	}
	if n := testing.AllocsPerRun(20, func() { _, _ = s.Read() }); n != 0 {
		t.Errorf("a reading allocates %v times, want none", n)
	}
}
