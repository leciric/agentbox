package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetIOWeightWritesWhatChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCgroupFile(t, filepath.Join(dir, "io.weight"), "default 100\n254:16 300\n")
	writeCgroupFile(t, filepath.Join(dir, "io.bfq.weight"), "default 25\n")
	var wrote []string
	write := func(path, value string) error {
		wrote = append(wrote, filepath.Base(path)+"="+value)
		return nil
	}
	if err := setIOWeight(dir, 25, write); err != nil {
		t.Fatal(err)
	}
	if len(wrote) != 1 || wrote[0] != "io.weight=default 25" {
		t.Errorf("wrote %q, want only io.weight's default", wrote)
	}

	// A kernel with neither file (no BFQ, no io.cost) and a stopped machine
	// are left alone.
	wrote = nil
	if err := setIOWeight(filepath.Join(dir, "gone"), 25, write); err != nil || wrote != nil {
		t.Errorf("a missing cgroup: wrote %q, err %v", wrote, err)
	}
}

func TestDefaultWeight(t *testing.T) {
	t.Parallel()
	for file, want := range map[string]int{
		"default 100\n":          100,
		"254:0 50\ndefault 25\n": 25,
		"":                       0,
		"default nonsense\n":     0,
	} {
		if got := defaultWeight(file); got != want {
			t.Errorf("defaultWeight(%q) = %d, want %d", file, got, want)
		}
	}
}

func TestUseBFQ(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sched := func(dev string) string { return filepath.Join(root, "sys/block", dev, "queue/scheduler") }
	// vda without BFQ loaded, vdb on it already; zram isn't a vd disk.
	writeCgroupFile(t, sched("vda"), "none [mq-deadline] \n")
	writeCgroupFile(t, sched("vdb"), "none mq-deadline [bfq] \n")
	writeCgroupFile(t, sched("zram0"), "[none] \n")
	var ran []string
	run := func(name string, args ...string) error {
		ran = append(ran, name)
		return nil
	}
	write := func(path, value string) error {
		return os.WriteFile(path, []byte("none mq-deadline ["+value+"] \n"), 0o644)
	}
	did, err := useBFQ(root, write, run)
	if err != nil || did != "BFQ on vda" {
		t.Fatalf("useBFQ = %q, %v", did, err)
	}
	if len(ran) != 1 || ran[0] != "modprobe" {
		t.Errorf("ran %q, want modprobe once", ran)
	}
	if did, err := useBFQ(root, write, run); err != nil || did != "" {
		t.Errorf("again: %q, %v; want nothing to do", did, err)
	}

	// A kernel without BFQ says so, and changes nothing.
	writeCgroupFile(t, sched("vda"), "none [mq-deadline] \n")
	noModule := func(string, ...string) error { return errors.New("modprobe: FATAL: Module bfq not found") }
	if _, err := useBFQ(root, write, noModule); err == nil {
		t.Error("no BFQ: no error")
	}
}
