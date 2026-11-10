package hostload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLinux(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = old })
	if _, err := newReader().read(); err == nil {
		t.Fatal("no PSI read without an error")
	}
	write("pressure/memory", "some avg10=12.50 avg60=1 avg300=0 total=1\nfull avg10=3.00 avg60=0 avg300=0 total=1\n")
	write("pressure/cpu", "some avg10=55.10 avg60=1 avg300=0 total=1\n")
	write("pressure/io", "some avg10=7.00 avg60=1 avg300=0 total=1\nfull avg10=2 avg60=0 avg300=0 total=1\n")
	write("meminfo", "MemTotal:       32000000 kB\nMemFree:  1 kB\nMemAvailable:   8000000 kB\n")
	r, err := newReader().read()
	if err != nil {
		t.Fatal(err)
	}
	if r.Memory != 12.5 || r.CPU != 55.1 || r.IO != 7 || r.CPUShare != -1 || r.MemoryTotal != 32000000<<10 || r.Available != 8000000<<10 {
		t.Fatalf("read %+v", r)
	}
}
