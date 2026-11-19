package agent

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeZram is a VM's /proc as taking zram away sees it, and what it ran.
type fakeZram struct {
	root  string
	ran   []string
	wrote map[string]string
	fail  string // a device swapoff fails on
}

func newFakeZram(t *testing.T, swaps string) *fakeZram {
	t.Helper()
	f := &fakeZram{root: t.TempDir(), wrote: map[string]string{}}
	writeCgroupFile(t, filepath.Join(f.root, "proc/swaps"), "Filename\tType\tSize\tUsed\tPriority\n"+swaps)
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
			if len(args) > 0 && args[0] == f.fail {
				return errors.New("swapoff: " + f.fail + ": Cannot allocate memory")
			}
			return nil
		},
	}
}

func TestDropZramTurnsOffEveryZramDeviceAndLeavesASwapfile(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "/dev/zram0 partition 4194300 1024 100\n/swapfile file 2097148 0 -2\n/dev/zram1 partition 4194300 0 100\n")
	did, err := dropZram(f.host())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"swapoff /dev/zram0", "swapoff /dev/zram1"}; !slices.Equal(f.ran, want) {
		t.Errorf("ran %q, want %q: the user's swapfile stays", f.ran, want)
	}
	if f.wrote["/sys/block/zram0/reset"] != "1" || f.wrote["/sys/block/zram1/reset"] != "1" {
		t.Errorf("wrote %v, want both devices reset", f.wrote)
	}
	if !strings.Contains(did, "/dev/zram0, /dev/zram1") {
		t.Errorf("did = %q", did)
	}
}

func TestDropZramWithNoneDoesNothing(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "/swapfile file 2097148 0 -2\n")
	did, err := dropZram(f.host())
	if err != nil || did != "" || len(f.ran) != 0 {
		t.Errorf("dropZram = %q, %v, ran %q; want nothing", did, err, f.ran)
	}
}

func TestDropZramSaysWhichDeviceStaysOn(t *testing.T) {
	t.Parallel()
	f := newFakeZram(t, "/dev/zram0 partition 4194300 4000000 100\n/dev/zram1 partition 4194300 0 100\n")
	f.fail = "/dev/zram0"
	did, err := dropZram(f.host())
	if err == nil || !strings.Contains(err.Error(), "Cannot allocate memory") {
		t.Errorf("err = %v, want swapoff's", err)
	}
	if _, reset := f.wrote["/sys/block/zram0/reset"]; reset {
		t.Error("a device still on was reset")
	}
	if !strings.Contains(did, "/dev/zram1") {
		t.Errorf("did = %q, want the one that went", did)
	}
}
