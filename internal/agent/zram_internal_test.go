package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeZram is a VM's /proc and /sys as zram setup sees them, and what it ran.
type fakeZram struct {
	root    string
	ran     []string
	wrote   map[string]string
	modules bool // the kernel has the zram module
	hotAdd  string
}

func newFakeZram(t *testing.T, swaps string) *fakeZram {
	t.Helper()
	f := &fakeZram{root: t.TempDir(), wrote: map[string]string{}, modules: true, hotAdd: "1\n"}
	writeCgroupFile(t, filepath.Join(f.root, "proc/swaps"), "Filename\tType\tSize\tUsed\tPriority\n"+swaps)
	if err := os.MkdirAll(filepath.Join(f.root, "sys/block/vda"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeZram) host() zramHost {
	return zramHost{
		root: f.root,
		write: func(path, value string) error {
			f.wrote[strings.TrimPrefix(path, f.root)] = value
			return nil
		},
		run: func(name string, args ...string) error {
			f.ran = append(f.ran, strings.Join(append([]string{name}, args...), " "))
			if name == "modprobe" {
				if !f.modules {
					return errors.New("modprobe: FATAL: Module zram not found")
				}
				// Loading it makes zram0, unset.
				_ = os.MkdirAll(filepath.Join(f.root, "sys/class/zram-control"), 0o755)
				_ = os.MkdirAll(filepath.Join(f.root, "sys/block/zram0"), 0o755)
				_ = os.WriteFile(filepath.Join(f.root, "sys/block/zram0/disksize"), []byte("0\n"), 0o644)
			}
			return nil
		},
		output: func(name string, args ...string) ([]byte, error) {
			f.ran = append(f.ran, strings.Join(append([]string{name}, args...), " "))
			return []byte(f.hotAdd), nil
		},
	}
}

func TestEnsureZramLoadsTheModuleAndTurnsSwapOn(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "/swapfile file 2097148 0 -2\n")
	did, err := ensureZram(f.host(), 4*gib)
	if err != nil {
		t.Fatal(err)
	}
	if want := "modprobe zram num_devices=1|mkswap /dev/zram0|swapon -p 100 /dev/zram0"; strings.Join(f.ran, "|") != want {
		t.Errorf("ran %q, want %q", f.ran, want)
	}
	if f.wrote["/sys/block/zram0/comp_algorithm"] != "zstd" || f.wrote["/sys/block/zram0/disksize"] != "4294967296" {
		t.Errorf("wrote %v", f.wrote)
	}
	if !strings.Contains(did, "4.0 GiB of zram swap (/dev/zram0)") {
		t.Errorf("said %q", did)
	}
}

func TestEnsureZramDoesNothingWhenItIsOnAlready(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "/dev/zram0 partition 4194300 0 100\n")
	did, err := ensureZram(f.host(), 4*gib)
	if err != nil || len(f.ran) != 0 || len(f.wrote) != 0 || !strings.Contains(did, "already") {
		t.Errorf("with zram on: %q, %v, ran %v, wrote %v", did, err, f.ran, f.wrote)
	}
}

func TestEnsureZramAddsADeviceWhenTheModulesAreTaken(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "")
	// Loaded already, its one device used for something else.
	writeCgroupFile(t, filepath.Join(f.root, "sys/class/zram-control/hot_add"), "")
	writeCgroupFile(t, filepath.Join(f.root, "sys/block/zram0/disksize"), "1073741824\n")
	if _, err := ensureZram(f.host(), 4*gib); err != nil {
		t.Fatal(err)
	}
	if want := "cat /sys/class/zram-control/hot_add|mkswap /dev/zram1|swapon -p 100 /dev/zram1"; strings.Join(f.ran, "|") != want {
		t.Errorf("ran %q, want %q", f.ran, want)
	}
}

func TestEnsureZramWithoutTheModuleSaysSo(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "")
	f.modules = false
	if _, err := ensureZram(f.host(), 4*gib); err == nil || !strings.Contains(err.Error(), "no zram") {
		t.Errorf("without the module: %v", err)
	}
	if _, err := ensureZram(newFakeZram(t, "").host(), 1<<20); err == nil {
		t.Error("made a 1 MiB zram device")
	}
}
