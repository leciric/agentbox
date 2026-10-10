package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryLimitLeavesTheVMItsShare(t *testing.T) {
	t.Parallel()
	for total, want := range map[int64]int64{
		16 * gib: 14 * gib, // an eighth
		4 * gib:  3 * gib,  // at least 1 GiB
		2 * gib:  1 * gib,  // never below half
		0:        0,        // unknown: none
	} {
		if got := MemoryLimit(total); got != want {
			t.Errorf("MemoryLimit(%d GiB) = %d MiB, want %d MiB", total/gib, got>>20, want>>20)
		}
	}
}

// writeCgroupFile writes a fake cgroup's file, making its directory.
func writeCgroupFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, text)
}

func TestSetCgroupLimitWritesOnlyWhatChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCgroupFile(t, filepath.Join(dir, "memory.max"), "max\n")
	var writes []string
	write := func(path, value string) error {
		writes = append(writes, filepath.Base(path)+"="+value)
		return os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
	if err := setCgroupLimit(dir, "memory.max", 14*gib+5, write); err != nil {
		t.Fatal(err)
	}
	if err := setCgroupLimit(dir, "memory.max", 14*gib, write); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 || writes[0] != "memory.max=15032385536" {
		t.Errorf("writes = %v, want one, in whole pages", writes)
	}
	if err := setCgroupLimit(dir, "memory.max", 0, write); err != nil || writes[len(writes)-1] != "memory.max=max" {
		t.Errorf("no limit: %v, %v", writes, err)
	}
	if err := setCgroupLimit(filepath.Join(dir, "gone"), "memory.max", gib, write); err != nil {
		t.Errorf("a machine with no cgroup: %v", err)
	}
}

func TestMemoryEventsReadsKillsAndTheLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCgroupFile(t, filepath.Join(dir, "memory.events"), "low 0\nhigh 0\nmax 12\noom 3\noom_kill 5\noom_group_kill 0\n")
	writeCgroupFile(t, filepath.Join(dir, "memory.events.local"), "low 0\nhigh 0\nmax 12\noom 2\noom_kill 0\noom_group_kill 0\n")
	writeCgroupFile(t, filepath.Join(dir, "memory.max"), "15032385536\n")
	got, ok := memoryEvents(dir)
	if want := (MemoryCounts{Kills: 5, OwnLimit: 2, Limit: 15032385536}); !ok || got != want {
		t.Errorf("memoryEvents = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := memoryEvents(filepath.Join(dir, "gone")); ok {
		t.Error("a cgroup that isn't there was read")
	}
}

func TestLastOOMVictimFindsTheMachinesLastKill(t *testing.T) {
	t.Parallel()
	log := []byte(`[ 100.1] oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=lxc.payload.ab-p-agent-01,mems_allowed=0,oom_memcg=/lxc.payload.ab-p-agent-01,task_memcg=/lxc.payload.ab-p-agent-01/agentbox.run.bash-1,task=go,pid=4100,uid=1000
[ 100.2] Memory cgroup out of memory: Killed process 4100 (go) total-vm:9000000kB, anon-rss:2000000kB, file-rss:1000kB, shmem-rss:0kB, UID:1000 pgtables:100kB oom_score_adj:0
[ 200.1] oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=x,mems_allowed=0,oom_memcg=/lxc.payload.ab-p-agent-010,task_memcg=/lxc.payload.ab-p-agent-010,task=node,pid=5000,uid=1000
[ 200.2] Memory cgroup out of memory: Killed process 5000 (node) total-vm:1kB, anon-rss:1kB, file-rss:0kB, shmem-rss:0kB, UID:1000 pgtables:1kB oom_score_adj:0
[ 300.1] oom-kill:constraint=CONSTRAINT_NONE,nodemask=(null),cpuset=x,mems_allowed=0,global_oom,task_memcg=/lxc.payload.ab-p-agent-01/user.slice,task=vitest,pid=4200,uid=1000
[ 300.2] Memory cgroup out of memory: Killed process 4200 (vitest) total-vm:9000000kB, anon-rss:3145728kB, file-rss:0kB, shmem-rss:1024kB, UID:1000 pgtables:100kB oom_score_adj:0
`)
	v, ok := lastOOMVictim(log, "ab-p-agent-01")
	if !ok || v.Name != "vitest" || v.PID != 4200 || v.RSS != (3145728+1024)<<10 {
		t.Errorf("victim = %+v, %v; want vitest, the last of agent-01's (the VM's OOM) and not agent-010's", v, ok)
	}
	if _, ok := lastOOMVictim(log, "ab-p-agent-02"); ok {
		t.Error("found a victim for a machine with none")
	}
}

// fakeMachine is an agent's cgroup with a process in it, as the VM sees it:
// its cgroup.procs and the process's /proc status.
func fakeMachine(t *testing.T, instance string) (root, proc string) {
	t.Helper()
	root, proc = t.TempDir(), t.TempDir()
	cg := filepath.Join(root, "lxc.payload."+instance)
	writeCgroupFile(t, filepath.Join(cg, "cgroup.procs"), "")
	writeCgroupFile(t, filepath.Join(cg, "user.slice", "session-1.scope", "cgroup.procs"), "9001\n9002\n")
	writeCgroupFile(t, filepath.Join(proc, "9001", "status"), "Name:\tclaude\nNSpid:\t9001\t310\n")
	writeCgroupFile(t, filepath.Join(proc, "9002", "status"), "Name:\tbash\nNSpid:\t9002\t412\n")
	return root, proc
}

func TestPlaceRunMovesTheMachinesProcessByItsOwnNumber(t *testing.T) {
	t.Parallel()
	root, proc := fakeMachine(t, "m")
	var wrote map[string]string
	write := func(path, value string) error {
		if wrote == nil {
			wrote = map[string]string{}
		}
		wrote[path] = value
		return nil
	}
	if err := placeRun(root, proc, "m", "bash-toolu/01", 412, write); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "lxc.payload.m", "agentbox.run.bash-toolu_01")
	if wrote[filepath.Join(dir, "cgroup.procs")] != "9002" {
		t.Errorf("wrote %v, want the VM's pid 9002 into the run's cgroup", wrote)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("the run's cgroup wasn't made: %v", err)
	}
	if err := placeRun(root, proc, "m", "k", 999, write); err == nil {
		t.Error("placed a process the machine hasn't got")
	}
}

func TestFreezeReclaimAndPopulated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var wrote []string
	write := func(path, value string) error {
		wrote = append(wrote, filepath.Base(path)+"="+value)
		return nil
	}
	// No cgroup yet: nothing to freeze or reclaim, and nothing in it.
	if err := freezeRun(root, "m", "k", true, write); err != nil || len(wrote) != 0 {
		t.Fatalf("freezing a run with no cgroup: %v, %v", wrote, err)
	}
	if err := reclaimRun(root, "m", "k", write); err != nil || len(wrote) != 0 {
		t.Fatalf("reclaiming a run with no cgroup: %v, %v", wrote, err)
	}
	if runPopulated(root, "m", "k") {
		t.Fatal("a run with no cgroup has processes")
	}
	dir := runCgroup(root, "m", "k")
	writeCgroupFile(t, filepath.Join(dir, "cgroup.freeze"), "0\n")
	writeCgroupFile(t, filepath.Join(dir, "memory.current"), "3221225472\n")
	writeCgroupFile(t, filepath.Join(dir, "cgroup.events"), "populated 1\nfrozen 0\n")
	if err := freezeRun(root, "m", "k", true, write); err != nil {
		t.Fatal(err)
	}
	if err := reclaimRun(root, "m", "k", func(path, value string) error {
		wrote = append(wrote, filepath.Base(path)+"="+value)
		return errReclaimShort // the kernel got what it could
	}); err != nil {
		t.Fatalf("a short reclaim is an error: %v", err)
	}
	if len(wrote) != 2 || wrote[0] != "cgroup.freeze=1" || wrote[1] != "memory.reclaim=3221225472" {
		t.Errorf("wrote %v", wrote)
	}
	if !runPopulated(root, "m", "k") {
		t.Error("a run with processes isn't populated")
	}
}
