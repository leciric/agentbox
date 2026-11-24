//go:build pressurelab

package agent

// What the pressurelab lab tests (capslab, loadlab) share.

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// labInt is the environment variable name as a number, or def.
func labInt(name string, def int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return def
	}
	return n
}

func mustWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// removeLab kills what's left in the lab's cgroups and removes them, deepest
// first.
func removeLab(root string) {
	_ = os.WriteFile(filepath.Join(root, "cgroup.kill"), []byte("1"), 0)
	time.Sleep(500 * time.Millisecond)
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}
