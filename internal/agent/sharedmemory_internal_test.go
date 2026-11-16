package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The agents together are throttled with 6 GiB of the VM left and killed
// with 4 GiB left (T3 Code's agent slice), and a small VM gives them half and
// three quarters; the leads get a quarter, at least min(half, 4 GiB).
func TestSharedLimits(t *testing.T) {
	for _, c := range []struct {
		total, high, max, leads int64
	}{
		{4 * gib, 2 * gib, 3 * gib, 2 * gib},
		{8 * gib, 4 * gib, 6 * gib, 4 * gib},
		{12 * gib, 6 * gib, 9 * gib, 4 * gib},
		{16 * gib, 10 * gib, 12 * gib, 4 * gib},
		{32 * gib, 26 * gib, 28 * gib, 8 * gib},
		{64 * gib, 58 * gib, 60 * gib, 16 * gib},
	} {
		a := AgentsLimits(c.total, 120000)
		if a.High != c.high || a.Max != c.max {
			t.Errorf("AgentsLimits(%s) = high %s, max %s; want %s, %s", HumanBytes(c.total), HumanBytes(a.High), HumanBytes(a.Max), HumanBytes(c.high), HumanBytes(c.max))
		}
		if a.High > a.Max || a.Max >= c.total {
			t.Errorf("AgentsLimits(%s) leaves nothing: %+v", HumanBytes(c.total), a)
		}
		if l := LeadsLimits(c.total); l.Max != c.leads || l.High != 0 {
			t.Errorf("LeadsLimits(%s) = %+v, want max %s", HumanBytes(c.total), l, HumanBytes(c.leads))
		}
	}
	a := AgentsLimits(16*gib, 120000)
	if a.CPUWeight != AgentCPUWeight || a.IOWeight != AgentIOWeight || a.PidsMax != 30000 {
		t.Errorf("AgentsLimits' weights and tasks = %+v", a)
	}
	if a := AgentsLimits(0, 0); a.High != 0 || a.Max != 0 || a.PidsMax != 0 {
		t.Errorf("AgentsLimits of an unknown VM = %+v, want no limits", a)
	}
	if l := LeadsLimits(0); l.Max != 0 {
		t.Errorf("LeadsLimits of an unknown VM = %+v", l)
	}
}

func TestVMTasksIsTheSmallerOfPidMaxAndThreadsMax(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "pid_max"), "4194304\n")
	writeTestFile(t, filepath.Join(dir, "threads-max"), "126000\n")
	if n := vmTasks(dir); n != 126000 {
		t.Errorf("vmTasks = %d", n)
	}
	if n := vmTasks(t.TempDir()); n != 0 {
		t.Errorf("vmTasks of nothing = %d", n)
	}
}

// fakeCgroup makes dir as cgroup2 would show it, with each of files.
func fakeCgroupDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range files {
		writeTestFile(t, filepath.Join(dir, name), v)
	}
}

func writeTestFile(t *testing.T, path, v string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordWrites is a cgroup write that writes the file, as the kernel would
// take it, and records it.
func recordWrites(writes *[]string) func(path, value string) error {
	return func(path, value string) error {
		*writes = append(*writes, filepath.Base(path)+"="+value)
		if filepath.Base(path) == "cgroup.subtree_control" {
			value = strings.ReplaceAll(readString(path)+" "+value, "+", "")
		}
		return os.WriteFile(path, []byte(value), 0o644)
	}
}

// A parent is made, its children get the controllers it is offered, and its
// limits are written, each only when it changes.
func TestSetGroupWritesTheParentsLimits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), AgentsCgroup)
	fakeCgroupDir(t, dir, map[string]string{
		"cgroup.controllers": "cpuset cpu io memory pids\n", "cgroup.subtree_control": "\n",
		"memory.high": "max\n", "memory.max": "max\n", "cpu.weight": "100\n", "io.weight": "default 100\n", "pids.max": "max\n",
	})
	var writes []string
	l := AgentsLimits(16*gib, 120000)
	if err := setGroup(dir, l, []string{"cpu", "io", "memory", "pids", "hugetlb"}, recordWrites(&writes)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cgroup.subtree_control=+cpu +io +memory +pids",
		"memory.high=" + strconv.FormatInt(10*gib, 10),
		"memory.max=" + strconv.FormatInt(12*gib, 10),
		"cpu.weight=10",
		"io.weight=default 25",
		"pids.max=30000",
	}
	if !slices.Equal(writes, want) {
		t.Errorf("writes = %q\nwant %q", writes, want)
	}
	writes = nil
	if err := setGroup(dir, l, []string{"cpu", "io", "memory", "pids"}, recordWrites(&writes)); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 0 {
		t.Errorf("a second look rewrote %q", writes)
	}
	// The VM grew: the limits follow.
	if err := setGroup(dir, AgentsLimits(32*gib, 120000), nil, recordWrites(&writes)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(writes, []string{"memory.high=" + strconv.FormatInt(26*gib, 10), "memory.max=" + strconv.FormatInt(28*gib, 10)}) {
		t.Errorf("after the VM grew, writes = %q", writes)
	}
	// One that isn't there is made.
	fresh := filepath.Join(t.TempDir(), LeadsCgroup)
	if err := setGroup(fresh, LeadsLimits(16*gib), []string{"memory"}, recordWrites(&writes)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the parent wasn't made: %v", err)
	}
}

// raw.lxc places a machine in the agents' parent, replacing an earlier
// release's placement and keeping everything else; with the parent off, the
// placement comes off.
func TestPlacedRawLXC(t *testing.T) {
	ours := strings.Join(AgentPlacement("i"), "\n")
	if ours != "lxc.cgroup.dir.container=agentbox.agents/lxc.payload.i\nlxc.cgroup.dir.monitor=lxc.monitor.i" {
		t.Errorf("AgentPlacement = %q", ours)
	}
	for _, c := range []struct{ have, want string }{
		{"", ours},
		{"lxc.apparmor.profile=unconfined", "lxc.apparmor.profile=unconfined\n" + ours},
		{"lxc.cgroup.dir.container=agentbox/i\nlxc.cgroup.dir.monitor=agentbox/i.monitor", ours},
		{ours, ours},
	} {
		if got := placedRawLXC(c.have, "i", true); got != c.want {
			t.Errorf("placedRawLXC(%q) = %q, want %q", c.have, got, c.want)
		}
	}
	if got := placedRawLXC("lxc.apparmor.profile=unconfined\n"+ours, "i", false); got != "lxc.apparmor.profile=unconfined" {
		t.Errorf("unplaced = %q", got)
	}
}

// In AgentBox's VM, a machine is placed in the agents' parent, once: a
// machine placed already needs nothing.
func TestOldLimitStepsPlaceTheMachineInTheVM(t *testing.T) {
	old := SharedCgroups
	SharedCgroups = func() bool { return true }
	t.Cleanup(func() { SharedCgroups = old })
	steps := stepArgs(t, oldLimitSteps("i", map[string]string{"raw.lxc": "lxc.cgroup.dir.container=agentbox/i\nlxc.cgroup.dir.monitor=agentbox/i.monitor"}, nil))
	want := [][]string{{"config", "set", "i", "raw.lxc=" + strings.Join(AgentPlacement("i"), "\n")}}
	if !slices.EqualFunc(steps, want, slices.Equal) {
		t.Errorf("steps = %q, want %q", steps, want)
	}
	if steps := oldLimitSteps("i", map[string]string{"raw.lxc": strings.Join(AgentPlacement("i"), "\n")}, nil); steps != nil {
		t.Errorf("a placed machine got %d steps", len(steps))
	}
}

// An agent's cgroup is found in the agents' parent first, then at the root.
func TestAgentCgroupInTheAgentsParent(t *testing.T) {
	root := t.TempDir()
	if got := agentCgroup(root, "i"); got != filepath.Join(root, "lxc.payload.i") {
		t.Errorf("unplaced: %s", got)
	}
	fakeCgroupDir(t, filepath.Join(root, AgentsCgroup, "lxc.payload.i"), nil)
	if got := agentCgroup(root, "i"); got != filepath.Join(root, AgentsCgroup, "lxc.payload.i") {
		t.Errorf("placed: %s", got)
	}
}

// What a stopped machine left of its cgroups goes before it starts, deepest
// first; nothing is removed outside the VM, or that isn't empty.
func TestClearStaleCgroups(t *testing.T) {
	root := t.TempDir()
	oldAgents, oldShared := AgentsDir, SharedCgroups
	AgentsDir = filepath.Join(root, AgentsCgroup)
	t.Cleanup(func() { AgentsDir, SharedCgroups = oldAgents, oldShared })
	payload := filepath.Join(AgentsDir, "lxc.payload.i")
	monitor := filepath.Join(root, "lxc.monitor.i")
	fakeCgroupDir(t, filepath.Join(payload, "init.scope"), nil)
	fakeCgroupDir(t, monitor, nil)
	SharedCgroups = func() bool { return false }
	ClearStaleCgroups("i")
	if _, err := os.Stat(payload); err != nil {
		t.Fatal("cleared outside the VM")
	}
	SharedCgroups = func() bool { return true }
	ClearStaleCgroups("i")
	for _, dir := range []string{payload, monitor} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s is still there", dir)
		}
	}
	// A directory with something in it (a cgroup with a process can't be
	// removed) stops it.
	fakeCgroupDir(t, payload, map[string]string{"cgroup.procs": "1\n"})
	ClearStaleCgroups("i")
	if _, err := os.Stat(payload); err != nil {
		t.Error("a busy cgroup was removed")
	}
}

// A lead chat's cgroup is made in the leads' parent with memory.oom.group,
// and the adapter goes in first, then what it started.
func TestPlaceLeadChatMovesTheWholeTree(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, LeadsCgroup)
	fakeCgroupDir(t, parent, map[string]string{"cgroup.controllers": "cpu memory pids\n", "cgroup.subtree_control": "\n"})
	proc := filepath.Join(root, "proc")
	// 10 started 11 and 12 (on two threads); 11 started 13.
	for pid, children := range map[string]map[string]string{
		"10": {"10": "11 ", "15": "12"}, "11": {"11": "13"}, "12": {"12": ""}, "13": {"13": ""},
	} {
		for task, list := range children {
			fakeCgroupDir(t, filepath.Join(proc, pid, "task", task), map[string]string{"children": list})
		}
	}
	dir := filepath.Join(parent, "chat.hello_lead.10")
	var writes []string
	write := func(path, value string) error {
		writes = append(writes, filepath.Base(path)+"="+value)
		if filepath.Base(path) == "cgroup.procs" {
			return nil // the kernel's: it holds the last one moved
		}
		return recordWrites(new([]string))(path, value)
	}
	// The kernel makes a new cgroup's files.
	fakeCgroupDir(t, dir, map[string]string{"memory.oom.group": "0\n"})
	got, err := placeLeadChat(dir, proc, 10, write)
	if err != nil || got != dir {
		t.Fatalf("placeLeadChat = %q, %v", got, err)
	}
	want := []string{"cgroup.subtree_control=+memory +pids", "memory.oom.group=1", "cgroup.procs=10", "cgroup.procs=11", "cgroup.procs=12", "cgroup.procs=13"}
	if !slices.Equal(writes, want) {
		t.Errorf("writes = %q\nwant %q", writes, want)
	}
	if d := LeadChatDir("hello/lead", 10); d != filepath.Join(LeadsDir, "chat.hello_lead.10") {
		t.Errorf("LeadChatDir = %s", d)
	}
}

// Outside AgentBox's VM a lead chat is left where it is.
func TestPlaceLeadChatOutsideTheVM(t *testing.T) {
	old := SharedCgroups
	SharedCgroups = func() bool { return false }
	t.Cleanup(func() { SharedCgroups = old })
	if dir, err := PlaceLeadChat("hello/lead", os.Getpid()); dir != "" || err != nil {
		t.Errorf("PlaceLeadChat = %q, %v", dir, err)
	}
}

// cgroup.kill kills a whole cgroup; a kernel without it (before 5.14) has
// each of its processes killed, its children's too.
func TestKillCgroup(t *testing.T) {
	dir := t.TempDir()
	fakeCgroupDir(t, dir, map[string]string{"cgroup.kill": ""})
	var writes []string
	if err := killCgroup(dir, recordWrites(&writes)); err != nil || !slices.Equal(writes, []string{"cgroup.kill=1"}) {
		t.Errorf("killCgroup = %v, writes %q", err, writes)
	}

	old := t.TempDir()
	sleeps := []*exec.Cmd{exec.Command("sleep", "60"), exec.Command("sleep", "60")}
	for _, c := range sleeps {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
	}
	fakeCgroupDir(t, old, map[string]string{"cgroup.procs": strconv.Itoa(sleeps[0].Process.Pid) + "\n"})
	fakeCgroupDir(t, filepath.Join(old, "child"), map[string]string{"cgroup.procs": strconv.Itoa(sleeps[1].Process.Pid) + "\n"})
	noKill := func(path, value string) error {
		if _, err := os.Stat(path); err != nil {
			return err
		}
		return nil
	}
	if err := killCgroup(old, noKill); err != nil {
		t.Fatal(err)
	}
	for _, c := range sleeps {
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "killed") {
				t.Errorf("sleep ended with %v, want killed", err)
			}
		case <-time.After(5 * time.Second):
			_ = c.Process.Kill()
			t.Error("a process in the cgroup wasn't killed")
		}
	}
}

// LeadChatKills reads the chat's oom_kill count.
func TestLeadChatKills(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LeadChatKills(filepath.Join(dir, "gone")); ok {
		t.Error("a cgroup that isn't there read")
	}
	writeTestFile(t, filepath.Join(dir, "memory.events"), "low 0\nhigh 0\nmax 3\noom 1\noom_kill 2\noom_group_kill 1\n")
	if n, ok := LeadChatKills(dir); !ok || n != 2 {
		t.Errorf("LeadChatKills = %d, %v", n, ok)
	}
}
