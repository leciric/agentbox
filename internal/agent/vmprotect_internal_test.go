package agent

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestRecursiveProtOptsKeepsEveryFlag(t *testing.T) {
	t.Parallel()
	const without = "24 1 0:22 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:9 - cgroup2 cgroup2 rw,nsdelegate,memory_localevents\n" +
		"25 1 0:23 / /sys/fs/pstore rw,nosuid,nodev,noexec,relatime shared:10 - pstore pstore rw\n"
	opts, ok := recursiveProtOpts(without)
	// nsdelegate and memory_localevents are cleared by a remount that
	// doesn't name them.
	if want := "remount,nosuid,nodev,noexec,relatime,nsdelegate,memory_localevents,memory_recursiveprot"; !ok || opts != want {
		t.Errorf("recursiveProtOpts = %q, %v; want %q", opts, ok, want)
	}
	with := strings.Replace(without, "memory_localevents", "memory_recursiveprot", 1)
	if opts, ok := recursiveProtOpts(with); ok || opts != "" {
		t.Errorf("already on: %q, %v; want nothing to do", opts, ok)
	}
	if opts, ok := recursiveProtOpts("25 1 0:23 / /sys/fs/pstore rw - pstore pstore rw\n"); !ok || opts != "" {
		t.Errorf("no cgroup2: %q, %v; want an error", opts, ok)
	}
}

func TestAdoptArgsMoveThePidIntoAProtectedScope(t *testing.T) {
	t.Parallel()
	args := strings.Join(adoptArgs(4242), " ")
	for _, want := range []string{
		"StartTransientUnit ssa(sv)a(sa(sv)) agentbox-daemon-4242.scope fail 8 PIDs au 1 4242",
		"Slice s system.slice",
		"MemoryMin t " + strconv.FormatInt(DaemonMin, 10),
		"MemoryLow t " + strconv.FormatInt(DaemonLow, 10),
		"MemorySwapMax t 0",
		"CPUWeight t 200 IOWeight t 200 0",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("busctl %s\nlacks %q", args, want)
		}
	}
}

func TestServiceProtectionsProtectTheSliceAndIncus(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	for _, u := range serviceProtections() {
		got[u.unit] = strings.Join(u.props, " ")
	}
	if !strings.Contains(got["system.slice"], "MemoryMin=1610612736 MemoryLow=3221225472 MemorySwapMax=0") {
		t.Errorf("system.slice: %q", got["system.slice"])
	}
	if !strings.Contains(got["incus.service"], "MemoryMin=536870912 MemorySwapMax=0") {
		t.Errorf("incus.service: %q", got["incus.service"])
	}
}

// fakeProcs is a cgroup tree and /proc with an oom_score_adj per pid.
func fakeProcs(t *testing.T, procs map[string][]int, adj map[int]int) (cgroup, proc string) {
	t.Helper()
	root := t.TempDir()
	cgroup, proc = filepath.Join(root, "cg"), filepath.Join(root, "proc")
	for dir, pids := range procs {
		var b strings.Builder
		for _, p := range pids {
			b.WriteString(strconv.Itoa(p) + "\n")
		}
		writeCgroupFile(t, filepath.Join(cgroup, dir, "cgroup.procs"), b.String())
	}
	for pid, a := range adj {
		writeCgroupFile(t, filepath.Join(proc, strconv.Itoa(pid), "oom_score_adj"), strconv.Itoa(a)+"\n")
	}
	return cgroup, proc
}

type helperLog struct {
	bytes.Buffer
	starts int
	fail   bool
}

func (h *helperLog) Close() error { return nil }

func (h *helperLog) Write(b []byte) (int, error) {
	if h.fail {
		return 0, errors.New("broken pipe")
	}
	return h.Buffer.Write(b)
}

func TestOOMScorerRaisesEachProcessOnce(t *testing.T) {
	t.Parallel()
	cg, proc := fakeProcs(t, map[string][]int{
		".":                  {10},
		"init.scope":         {11, 12},
		"system.slice/dockr": {13},
	}, map[int]int{10: 0, 11: 0, 12: AgentOOMScoreAdj, 13: 300})
	log := &helperLog{}
	o := &OOMScorer{Proc: proc, Start: func(adj int) (io.WriteCloser, error) {
		if adj != AgentOOMScoreAdj {
			t.Errorf("helper started for %d", adj)
		}
		log.starts++
		return log, nil
	}}
	n, err := o.raise("i", cg)
	if err != nil || n != 3 {
		t.Fatalf("raise = %d, %v; want the 3 under %d", n, err, AgentOOMScoreAdj)
	}
	pids := strings.Fields(log.String())
	slices.Sort(pids)
	if !slices.Equal(pids, []string{"10", "11", "13"}) {
		t.Errorf("helper was given %q", log.String())
	}
	// The next look: nothing new, nothing written, and one helper still.
	log.Reset()
	writeCgroupFile(t, filepath.Join(proc, "10", "oom_score_adj"), "0\n") // its write is the helper's
	if n, err := o.raise("i", cg); n != 0 || err != nil || log.Len() != 0 {
		t.Errorf("second look raised %d (%q), %v", n, log.String(), err)
	}
	// A new process is raised; one that's gone is forgotten.
	writeCgroupFile(t, filepath.Join(cg, "init.scope", "cgroup.procs"), "11\n14\n")
	writeCgroupFile(t, filepath.Join(proc, "14", "oom_score_adj"), "0\n")
	if n, _ := o.raise("i", cg); n != 1 || strings.TrimSpace(log.String()) != "14" || log.starts != 1 {
		t.Errorf("new process: raised %d, %q, %d helpers", n, log.String(), log.starts)
	}
	if o.seen["i"][12] {
		t.Error("an ended process is still remembered")
	}
}

func TestOOMScorerStartsAnotherHelperWhenOneDies(t *testing.T) {
	t.Parallel()
	cg, proc := fakeProcs(t, map[string][]int{".": {10}}, map[int]int{10: 0})
	dead := &helperLog{fail: true}
	live := &helperLog{}
	o := &OOMScorer{Proc: proc, Start: func(int) (io.WriteCloser, error) {
		if dead.starts == 0 {
			dead.starts++
			return dead, nil
		}
		return live, nil
	}}
	if _, err := o.raise("i", cg); err == nil {
		t.Fatal("a dead helper wasn't noticed")
	}
	if n, err := o.raise("i", cg); n != 1 || err != nil || strings.TrimSpace(live.String()) != "10" {
		t.Errorf("after the helper died: %d, %v, %q", n, err, live.String())
	}
}

func TestOOMScorerForgetsAStoppedMachine(t *testing.T) {
	t.Parallel()
	o := &OOMScorer{Proc: t.TempDir(), Start: func(int) (io.WriteCloser, error) { return &helperLog{}, nil }}
	o.seen = map[string]map[int]bool{"i": {1: true}}
	if n, err := o.raise("i", filepath.Join(t.TempDir(), "gone")); n != 0 || err != nil {
		t.Errorf("raise = %d, %v", n, err)
	}
	if _, ok := o.seen["i"]; ok {
		t.Error("a stopped machine's processes are still remembered")
	}
	_ = os.Remove(o.Proc)
}
