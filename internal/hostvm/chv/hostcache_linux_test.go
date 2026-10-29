package chv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageCached(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.raw")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// A sparse GiB with 8 MiB written in the middle, just written and so
	// in the page cache.
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(make([]byte, 8<<20), 512<<20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got := imageCached(path); got < 8<<20 || got > 16<<20 {
		t.Errorf("imageCached = %d, want about 8 MiB", got)
	}
	dropImageCache(path) // tmpfs keeps it; only that it doesn't fail
	if got := imageCached(filepath.Join(t.TempDir(), "none")); got != 0 {
		t.Errorf("imageCached of nothing = %d", got)
	}
	if directIO(filepath.Join(t.TempDir(), "none")) {
		t.Error("direct I/O on a file that isn't there")
	}
}

func TestFootprint(t *testing.T) {
	rollup := `55d0-7ffc ---p 00000000 00:00 0 [rollup]
Rss:             2016 kB
Pss:              388 kB
Pss_Anon:         116 kB
Pss_File:         272 kB
Pss_Shmem:       1000 kB
`
	if got := pssOf(strings.NewReader(rollup)); got != 1116<<10 {
		t.Errorf("pssOf = %d", got)
	}
	if got := footprint([]int{os.Getpid()}); got <= 0 {
		t.Errorf("footprint of this test = %d", got)
	}
}
