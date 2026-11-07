package agent

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
)

func TestThrashReadsFullPressureAndHitsOnItsOwnLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCgroupFile(t, filepath.Join(dir, "memory.pressure"),
		"some avg10=91.50 avg60=40.00 avg300=9.00 total=123\nfull avg10=78.25 avg60=30.00 avg300=7.00 total=99\n")
	writeCgroupFile(t, filepath.Join(dir, "memory.events.local"), "low 0\nhigh 0\nmax 4211\noom 0\noom_kill 0\n")
	got, ok := thrash(dir)
	if want := (ThrashSample{Full: 78.25, AtLimit: 4211}); !ok || got != want {
		t.Errorf("thrash = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := thrash(filepath.Join(dir, "stopped")); ok {
		t.Error("a cgroup that isn't there was read")
	}
}

// TestKillLargestPicksTheMostMemory: what holds the most RAM and swap
// anywhere in the machine's cgroup tree, whatever its oom_score, never one
// the kernel may not kill.
func TestKillLargestPicksTheMostMemory(t *testing.T) {
	t.Parallel()
	root, proc := t.TempDir(), t.TempDir()
	machine := filepath.Join(root, "lxc.payload.ab-x")
	writeCgroupFile(t, filepath.Join(machine, "cgroup.procs"), "")
	writeCgroupFile(t, filepath.Join(machine, "init.scope", "cgroup.procs"), "10\n")
	writeCgroupFile(t, filepath.Join(machine, "user.slice", "cgroup.procs"), "20\n30\n35\n40\n")
	for _, p := range []struct {
		pid, score, adj int
		name, rss, swap string
	}{
		{10, 2, 0, "systemd", "12000 kB", "0 kB"},
		{20, 950, 900, "dbus-daemon", "12 kB", "0 kB"}, // the highest oom_score, nothing to free
		{30, 600, 0, "node", "2500000 kB", "1000000 kB"},
		{35, 610, 0, "go", "3000000 kB", "0 kB"},        // more RAM, less in all
		{40, 1000, -1000, "sshd", "9000000 kB", "0 kB"}, // the kernel never kills it
	} {
		dir := filepath.Join(proc, strconv.Itoa(p.pid))
		writeCgroupFile(t, filepath.Join(dir, "oom_score"), strconv.Itoa(p.score)+"\n")
		writeCgroupFile(t, filepath.Join(dir, "oom_score_adj"), strconv.Itoa(p.adj)+"\n")
		writeCgroupFile(t, filepath.Join(dir, "comm"), p.name+"\n")
		writeCgroupFile(t, filepath.Join(dir, "status"), "Name:\t"+p.name+"\nVmRSS:\t  "+p.rss+"\nVmSwap:\t  "+p.swap+"\n")
	}
	var killed []int
	v, err := killLargest(machine, proc, func(pid int) error { killed = append(killed, pid); return nil })
	if err != nil || v.PID != 30 || v.Name != "node" || v.RSS != 2500000<<10 || len(killed) != 1 || killed[0] != 30 {
		t.Errorf("killLargest = %+v, %v; killed %v; want node (30) alone", v, err, killed)
	}
	empty := filepath.Join(root, "lxc.payload.ab-empty")
	writeCgroupFile(t, filepath.Join(empty, "cgroup.procs"), "")
	if _, err := killLargest(empty, proc, func(int) error { t.Error("killed in an empty machine"); return nil }); !errors.Is(err, errNoVictim) {
		t.Errorf("an empty machine: %v", err)
	}
}
