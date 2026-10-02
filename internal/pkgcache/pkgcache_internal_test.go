package pkgcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const kib64 = 64 << 10

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// put writes a 64 KiB file, last used at epoch plus age.
func put(t *testing.T, dir, rel string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, kib64), 0o644); err != nil {
		t.Fatal(err)
	}
	at := epoch.Add(age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func newCache(t *testing.T, maxBytes int64) *Cache {
	t.Helper()
	dir := t.TempDir()
	// Go's read-only module trees, should a test fail before evicting them.
	t.Cleanup(func() { _ = removeAll(dir) })
	c := &Cache{
		Dir: dir,
		Max: func() int64 { return maxBytes },
		now: func() time.Time { return epoch.Add(24 * time.Hour) },
	}
	if err := c.Prepare(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPrepareMakesEveryToolsDirectory(t *testing.T) {
	c := newCache(t, 0)
	for _, dir := range Dirs() {
		if info, err := os.Stat(filepath.Join(c.Dir, dir)); err != nil || !info.IsDir() {
			t.Errorf("%s: %v", dir, err)
		}
	}
	// Twice is fine: the daemon prepares it every time it starts.
	if err := c.Prepare(); err != nil {
		t.Fatal(err)
	}
}

func TestTrimEvictsWhatWasUsedLongestAgoInPiecesItsToolRecoversFrom(t *testing.T) {
	c := newCache(t, 10*kib64)
	// Oldest first: a whole Go module cache, read-only as go leaves it.
	mod := put(t, c.Dir, "go/mod/example.com/m@v1.0.0/m.go", 0)
	put(t, c.Dir, "go/mod/cache/download/example.com/m/@v/v1.0.0.zip", time.Minute)
	if err := os.Chmod(filepath.Dir(mod), 0o555); err != nil {
		t.Fatal(err)
	}
	// A browser, whole.
	put(t, c.Dir, "ms-playwright/chromium-1187/chrome", 2*time.Minute)
	put(t, c.Dir, "ms-playwright/chromium-1187/lib.so", 3*time.Minute)
	// An npx install, whole, though npm's cache around it goes file by file.
	npx := put(t, c.Dir, "npm/_npx/abc/node_modules/x/index.js", 4*time.Minute)
	put(t, c.Dir, "npm/_npx/abc/package.json", time.Hour)
	// pnpm's store, file by file, but never its index.
	index := put(t, c.Dir, "pnpm/store/v11/index.db", 0)
	oldFile := put(t, c.Dir, "pnpm/store/v11/files/aa/one", 5*time.Minute)
	newFile := put(t, c.Dir, "pnpm/store/v11/files/bb/two", 2*time.Hour)
	npm := put(t, c.Dir, "npm/_cacache/content-v2/sha512/aa/three", 3*time.Hour)
	corepack := put(t, c.Dir, "corepack/v1/pnpm/10.1.0/pnpm.cjs", 4*time.Hour)
	stray := put(t, c.Dir, "stray", 0)
	// 12 files of 64 KiB: 120% of the cap.
	if got := c.Size(); got != 12*kib64 {
		t.Fatalf("size %d, want %d", got, 12*kib64)
	}

	freed := c.Trim()

	// Down to nine tenths of the cap or under: the module cache (2 files)
	// and the browser (2) were what it took.
	if freed != 4*kib64 || c.Size() != 8*kib64 {
		t.Errorf("freed %d, size %d", freed, c.Size())
	}
	for _, gone := range []string{"go/mod", "ms-playwright/chromium-1187"} {
		if exists(filepath.Join(c.Dir, gone)) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{npx, index, oldFile, newFile, npm, corepack, stray} {
		if !exists(kept) {
			t.Errorf("%s was evicted", kept)
		}
	}

	// A lower cap, to 4.5 files: the old pnpm file, then the npx install,
	// whole and by its newest file, then the newer pnpm file.
	c.Max = func() int64 { return 5 * kib64 }
	c.Trim()
	for _, gone := range []string{oldFile, filepath.Join(c.Dir, "npm/_npx/abc"), newFile} {
		if exists(gone) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{npm, corepack, index, stray} {
		if !exists(kept) {
			t.Errorf("%s was evicted", kept)
		}
	}
}

func TestTrimLeavesWhatsInUse(t *testing.T) {
	c := newCache(t, kib64)
	c.now = func() time.Time { return epoch.Add(time.Minute) }
	put(t, c.Dir, "pip/http/a", 0)
	put(t, c.Dir, "pip/http/b", 0)
	if freed := c.Trim(); freed != 0 {
		t.Errorf("freed %d of files used a minute ago", freed)
	}
}

func TestTrimGivesTheDiskBackItsFloor(t *testing.T) {
	c := newCache(t, 100*kib64)
	room := int64(-2 * kib64)
	c.Room = func() int64 { return room }
	a := put(t, c.Dir, "go/build/aa/a-d", 0)
	b := put(t, c.Dir, "go/build/bb/b-d", time.Minute)
	keep := put(t, c.Dir, "go/build/cc/c-d", 2*time.Minute)
	if freed := c.Trim(); freed != 2*kib64 {
		t.Errorf("freed %d below the floor, want %d", freed, 2*kib64)
	}
	if exists(a) || exists(b) || !exists(keep) {
		t.Error("the wrong files went")
	}
	room = kib64
	if freed := c.Trim(); freed != 0 {
		t.Errorf("freed %d above the floor and under the cap", freed)
	}
}

func TestTrimDoesNothingWhileOff(t *testing.T) {
	c := newCache(t, 0)
	put(t, c.Dir, "uv/archive-v0/x", 0)
	if freed := c.Trim(); freed != 0 || c.Size() != kib64 {
		t.Errorf("freed %d, size %d", freed, c.Size())
	}
}

func TestClearEmptiesAndPreparesAgain(t *testing.T) {
	c := newCache(t, 0)
	mod := put(t, c.Dir, "go/mod/example.com/m@v1.0.0/m.go", 0)
	if err := os.Chmod(filepath.Dir(mod), 0o555); err != nil {
		t.Fatal(err)
	}
	put(t, c.Dir, "stray", 0)
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if c.Size() != 0 || exists(mod) || exists(filepath.Join(c.Dir, "stray")) {
		t.Errorf("size %d after clearing", c.Size())
	}
	if !exists(filepath.Join(c.Dir, GoMod)) {
		t.Error("go/mod wasn't made again")
	}
}
