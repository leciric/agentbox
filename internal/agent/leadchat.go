package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A lead chat's process tree — its ACP adapter, the AI tool under it, and
// whatever the tool's shell runs — goes in a cgroup of its own in
// agentbox.leads (sharedmemory.go) as it starts, so the leads' memory.max
// covers it, and its memory.oom.group has the kernel kill the whole tree when
// one of its processes is chosen: an adapter whose tool was killed under it
// would otherwise hang on a turn nobody is running. The daemon also kills the
// cgroup itself (cgroup.kill) when it sees a kill there, and when the chat
// ends, so nothing it started stays behind.
//
// The daemon runs in its ssh session's cgroup, and moving a process from there
// takes root (the two cgroups' common parent is the root's), so the move goes
// through sudo in AgentBox's VM; killing the cgroup doesn't: its cgroup.kill
// is handed to the daemon's user as it is made, as a run's freezer is
// (makeCgroup), for a VM short of memory, which is when it's needed.

// LeadChatDir is the directory of the cgroup a lead chat started as pid runs
// in, under agentbox.leads: a new one for each start, so one still being
// cleared doesn't stand in the next one's way.
func LeadChatDir(ref string, pid int) string {
	return filepath.Join(LeadsDir, "chat."+runKeyChars.ReplaceAllString(ref, "_")+"."+strconv.Itoa(pid))
}

// PlaceLeadChat puts the lead chat started as pid, and every process it has
// started since, in its own cgroup in agentbox.leads, and returns that
// cgroup's directory. In AgentBox's VM only (SharedCgroups); "" elsewhere.
func PlaceLeadChat(ref string, pid int) (string, error) {
	if !SharedCgroups() {
		return "", nil
	}
	return placeLeadChat(LeadChatDir(ref, pid), "/proc", pid, writeCgroup)
}

func placeLeadChat(dir, proc string, pid int, write func(path, value string) error) (string, error) {
	parent := filepath.Dir(dir)
	if err := mkdirCgroup(parent); err != nil {
		return "", err
	}
	if err := enableControllers(parent, []string{"memory", "pids"}, write); err != nil {
		return "", err
	}
	sudoed, err := mkdirCgroupSudo(dir)
	if err != nil {
		return "", err
	}
	if sudoed {
		_ = sudo("chown", "", strconv.Itoa(os.Getuid()), filepath.Join(dir, "cgroup.kill"))
	}
	if err := setCgroupValue(dir, "memory.oom.group", "1", write); err != nil {
		return dir, err
	}
	// The adapter first, so what it starts from now on is born inside; then
	// what it started before it was moved.
	for _, p := range processTree(proc, pid) {
		if err := write(filepath.Join(dir, "cgroup.procs"), strconv.Itoa(p)); err != nil && p == pid {
			return dir, err
		}
	}
	return dir, nil
}

// processTree is pid and its descendants, parents before their children, from
// /proc's children lists.
func processTree(proc string, pid int) []int {
	out := []int{pid}
	seen := map[int]bool{pid: true}
	for i := 0; i < len(out); i++ {
		tasks, _ := os.ReadDir(filepath.Join(proc, strconv.Itoa(out[i]), "task"))
		for _, task := range tasks {
			b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(out[i]), "task", task.Name(), "children"))
			if err != nil {
				continue
			}
			for _, f := range strings.Fields(string(b)) {
				if child, err := strconv.Atoi(f); err == nil && !seen[child] {
					seen[child] = true
					out = append(out, child)
				}
			}
		}
	}
	return out
}

// LeadChatKills is how many of a lead chat's processes the kernel killed for
// memory (its memory.events' oom_kill); ok is false when its cgroup isn't
// there.
func LeadChatKills(dir string) (kills int64, ok bool) {
	c, ok := memoryEvents(dir)
	return c.Kills, ok
}

// KillCgroup kills every process in a cgroup and below it at once
// (cgroup.kill, Linux 5.14), or one by one on an older kernel.
func KillCgroup(dir string) error {
	return killCgroup(dir, writeCgroup)
}

func killCgroup(dir string, write func(path, value string) error) error {
	err := write(filepath.Join(dir, "cgroup.kill"), "1")
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var errs []error
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return nil
		}
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					errs = append(errs, fmt.Errorf("kill %d: %w", pid, err))
				}
			}
		}
		return nil
	})
	return errors.Join(errs...)
}

// RemoveLeadChat kills what a lead chat that has ended left in its cgroup
// and removes the cgroup once it is empty, waiting up to wait for that.
func RemoveLeadChat(dir string, wait time.Duration) error {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := KillCgroup(dir); err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	for {
		err := removeCgroup(dir)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
