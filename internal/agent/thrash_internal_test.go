package agent

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestThrashReadsTheFullStallAndItsOwnLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCgroupFile(t, filepath.Join(dir, "memory.pressure"),
		"some avg10=91.50 avg60=40.00 avg300=9.00 total=12345678\nfull avg10=78.25 avg60=30.00 avg300=7.00 total=9000000\n")
	writeCgroupFile(t, filepath.Join(dir, "memory.events.local"), "low 0\nhigh 0\nmax 4211\noom 0\noom_kill 0\n")
	at := time.Unix(1000, 0)
	got, ok := thrash(dir, at)
	if want := (ThrashSample{At: at, Stalled: 9 * time.Second, AtLimit: 4211}); !ok || got != want {
		t.Errorf("thrash = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := thrash(filepath.Join(dir, "stopped"), at); ok {
		t.Error("a cgroup that isn't there was read")
	}
}

func TestThrashingIsStalledHalfTheTimeAtItsOwnLimit(t *testing.T) {
	t.Parallel()
	prev := ThrashSample{At: time.Unix(1000, 0), Stalled: 5 * time.Second, AtLimit: 10}
	next := func(stalled time.Duration, hits int64) ThrashSample {
		return ThrashSample{At: prev.At.Add(2 * time.Second), Stalled: prev.Stalled + stalled, AtLimit: prev.AtLimit + hits}
	}
	for _, tc := range []struct {
		s    ThrashSample
		want bool
	}{
		{next(1500*time.Millisecond, 30), true}, // 75% stalled at its limit
		{next(time.Second, 1), true},            // half
		{next(900*time.Millisecond, 30), false}, // under half
		{next(2*time.Second, 0), false},         // stalled, its limit not reached: the VM is short
		{ThrashSample{At: prev.At, Stalled: prev.Stalled + time.Second, AtLimit: 99}, false}, // no time passed
	} {
		if got := tc.s.Thrashing(prev); got != tc.want {
			t.Errorf("Thrashing(%+v) = %v, want %v", tc.s, got, tc.want)
		}
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
