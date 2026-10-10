package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/hostos"
)

// An agent's memory fails fast: its machine's cgroup gets a hard memory.max,
// so a process that outgrows it is killed at once (and the agent told,
// internal/daemon/pressure.go) rather than the agent crawling under a throttle
// while the VM swaps. The limit is what one agent may have alone: the VM's
// memory less what the VM keeps for itself and the other agents idling. How
// many agents' heavy commands run at once is the daemon's, from the VM's
// measured pressure (package pressure), not this.
//
// The cgroups are root's, made by Incus as each machine starts. In AgentBox's
// VM the daemon may use sudo (as incuswatch.go does), so a write goes through
// `sudo -n` when the daemon can't make it itself. Incus's limits.memory would
// set memory.max too, but DropOldLimits takes that key off every machine,
// and it doesn't follow the VM as it grows; this is written live.

// MemoryLimit is memory.max for an agent in a VM of total bytes: total less
// an eighth of it, and at least 1 GiB, which the VM keeps. 0 (none) when total
// isn't known.
func MemoryLimit(total int64) int64 {
	if total <= 0 {
		return 0
	}
	return max(total-max(total/8, int64(1)<<30), total/2)
}

// SetMemoryLimit sets an agent's machine's memory.max to limit (0 for none,
// "max"), and takes off any memory.high an earlier release throttled it with.
// It writes only what changes, and does nothing for a machine whose cgroup
// isn't there: stopped, or not on cgroup2.
func SetMemoryLimit(instance string, limit int64) error {
	dir := agentCgroup(cgroupRoot, instance)
	if err := setCgroupLimit(dir, "memory.max", limit, writeCgroup); err != nil {
		return err
	}
	return setCgroupLimit(dir, "memory.high", 0, writeCgroup)
}

func setCgroupLimit(dir, file string, limit int64, write func(path, value string) error) error {
	path := filepath.Join(dir, file)
	have, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	want := "max"
	if limit > 0 {
		// The kernel keeps memory limits in whole pages.
		want = strconv.FormatInt(limit/4096*4096, 10)
	}
	if strings.TrimSpace(string(have)) == want {
		return nil
	}
	return write(path, want)
}

// MemoryCounts is what an agent's cgroup says about running out of memory.
type MemoryCounts struct {
	// Kills is how many of its processes were killed for memory, whoever
	// ran out: its own memory.max, or the VM (memory.events' oom_kill).
	Kills int64
	// OwnLimit is how many times it reached its own memory.max with nothing
	// left to reclaim (memory.events.local's oom): kills since then that
	// were for its limit, not the VM's.
	OwnLimit int64
	// Limit is its memory.max, 0 for none.
	Limit int64
}

// MemoryEvents reads an agent's machine's MemoryCounts; ok is false when its
// cgroup isn't there.
func MemoryEvents(instance string) (MemoryCounts, bool) {
	return memoryEvents(agentCgroup(cgroupRoot, instance))
}

func memoryEvents(dir string) (MemoryCounts, bool) {
	var out MemoryCounts
	b, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		return out, false
	}
	out.Kills = eventCount(b, "oom_kill")
	if b, err := os.ReadFile(filepath.Join(dir, "memory.events.local")); err == nil {
		out.OwnLimit = eventCount(b, "oom")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil {
		out.Limit, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	return out, true
}

// eventCount is one count of a memory.events file.
func eventCount(file []byte, name string) int64 {
	for _, line := range strings.Split(string(file), "\n") {
		if v, found := strings.CutPrefix(line, name+" "); found {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// OOMVictim is a process the kernel killed for its cgroup's memory.max.
type OOMVictim struct {
	Name string
	PID  int
	RSS  int64 // bytes: its anonymous, file and shared memory together
}

// LastOOMVictim finds, in the kernel's log, the last process killed for the
// memory.max of the agent's machine; ok is false when the log can't be read
// or says nothing about it.
func LastOOMVictim(instance string) (OOMVictim, bool) {
	log, err := kernelLog()
	if err != nil {
		return OOMVictim{}, false
	}
	return lastOOMVictim(log, instance)
}

// kernelLog is the end of the kernel's log: dmesg, through sudo when it's
// restricted.
func kernelLog() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dmesg").Output()
	if err == nil {
		return out, nil
	}
	if !hostos.InVM() {
		return nil, err
	}
	return exec.CommandContext(ctx, "sudo", "-n", "dmesg").Output()
}

var (
	oomLine    = regexp.MustCompile(`oom-kill:.*task_memcg=([^,\s]+)`)
	killedLine = regexp.MustCompile(`Killed process (\d+) \(([^)]*)\).*?anon-rss:(\d+)kB, file-rss:(\d+)kB, shmem-rss:(\d+)kB`)
)

// lastOOMVictim reads a kernel log for the last kill of a process in the
// cgroup of the instance (its own, or one under it), whatever ran out: the
// "oom-kill:" line names the process's cgroup, and the "Killed process" line
// after it the process.
func lastOOMVictim(log []byte, instance string) (OOMVictim, bool) {
	var out OOMVictim
	found, ours := false, false
	sc := bufio.NewScanner(bytes.NewReader(log))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := oomLine.FindStringSubmatch(line); m != nil {
			cg := m[1]
			ours = strings.Contains(cg, "/lxc.payload."+instance) && (strings.HasSuffix(cg, "/lxc.payload."+instance) || strings.Contains(cg, "/lxc.payload."+instance+"/"))
			continue
		}
		if m := killedLine.FindStringSubmatch(line); m != nil && ours {
			pid, _ := strconv.Atoi(m[1])
			var kb int64
			for _, s := range m[3:] {
				n, _ := strconv.ParseInt(s, 10, 64)
				kb += n
			}
			out, found, ours = OOMVictim{Name: m[2], PID: pid, RSS: kb << 10}, true, false
		}
	}
	return out, found
}

// Runs: an agent's heavy commands, each in a cgroup of its own under its
// machine's, so the daemon can pause one (freeze it) and push its memory out
// to swap without stopping the agent. The command's shell asks to join its
// run as it starts (`agentbox heavy-join`, from the agent's BASH_ENV, or
// `agentbox heavy` for itself), and everything it starts after is in it.

// runCgroupPrefix names a run's cgroup, beside the machine's own: the
// container's systemd keeps to the cgroups of its units.
const runCgroupPrefix = "agentbox.run."

// runKey is a run's key as a cgroup name may have it.
var runKeyChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// RunCgroup is the directory of a run's cgroup.
func RunCgroup(instance, key string) string {
	return runCgroup(cgroupRoot, instance, key)
}

func runCgroup(root, instance, key string) string {
	return filepath.Join(agentCgroup(root, instance), runCgroupPrefix+runKeyChars.ReplaceAllString(key, "_"))
}

// PlaceRun moves the process the agent knows as pid (in its machine's own
// process numbers) into the cgroup of its run, made first if need be.
func PlaceRun(instance, key string, pid int) error {
	return placeRun(cgroupRoot, "/proc", instance, key, pid, writeCgroup)
}

func placeRun(root, proc, instance, key string, pid int, write func(path, value string) error) error {
	host, err := hostPID(proc, agentCgroup(root, instance), pid)
	if err != nil {
		return err
	}
	dir := runCgroup(root, instance, key)
	if err := makeCgroup(dir); err != nil {
		return err
	}
	return write(filepath.Join(dir, "cgroup.procs"), strconv.Itoa(host))
}

// hostPID is the VM's number for the process the machine whose cgroup is dir
// knows as pid: the one in the machine's cgroups whose NSpid ends in it.
func hostPID(proc, dir string, pid int) (int, error) {
	want := strconv.Itoa(pid)
	var found int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != 0 || !d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return nil
		}
		for _, field := range strings.Fields(string(b)) {
			status, err := os.ReadFile(filepath.Join(proc, field, "status"))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(status), "\n") {
				ids, ok := strings.CutPrefix(line, "NSpid:")
				if !ok {
					continue
				}
				if f := strings.Fields(ids); len(f) > 0 && f[len(f)-1] == want {
					found, _ = strconv.Atoi(field)
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if found == 0 {
		return 0, fmt.Errorf("no process %d in the machine", pid)
	}
	return found, nil
}

// FreezeRun pauses (on) or resumes a run's processes. A run with no cgroup
// has nothing to freeze.
func FreezeRun(instance, key string, on bool) error {
	return freezeRun(cgroupRoot, instance, key, on, writeCgroup)
}

func freezeRun(root, instance, key string, on bool, write func(path, value string) error) error {
	path := filepath.Join(runCgroup(root, instance, key), "cgroup.freeze")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	value := "0"
	if on {
		value = "1"
	}
	return write(path, value)
}

// ReclaimRun asks the kernel to push a paused run's memory out to swap (and
// drop its page cache), so a paused run doesn't hold the VM's RAM. Without
// swap only the cache goes. The kernel reclaims what it can and says when it
// fell short, which isn't an error here.
func ReclaimRun(instance, key string) error {
	return reclaimRun(cgroupRoot, instance, key, writeCgroup)
}

func reclaimRun(root, instance, key string, write func(path, value string) error) error {
	dir := runCgroup(root, instance, key)
	b, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || current <= 0 {
		return err
	}
	if err := write(filepath.Join(dir, "memory.reclaim"), strconv.FormatInt(current, 10)); err != nil && !errors.Is(err, errReclaimShort) {
		return err
	}
	return nil
}

// errReclaimShort is what writing memory.reclaim fails with when the kernel
// couldn't reclaim all that was asked (EAGAIN).
var errReclaimShort = errors.New("reclaimed less than asked")

// RunPopulated reports whether a run's cgroup still has processes in it, or
// any at all once placed: false when it's gone.
func RunPopulated(instance, key string) bool {
	return runPopulated(cgroupRoot, instance, key)
}

func runPopulated(root, instance, key string) bool {
	b, err := os.ReadFile(filepath.Join(runCgroup(root, instance, key), "cgroup.events"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "populated 1")
}

// RemoveRun resumes a run's cgroup and removes it once nothing is left in
// it; a process still running there (a command left in the background) keeps
// it, and it goes with the machine.
func RemoveRun(instance, key string) error {
	if err := FreezeRun(instance, key, false); err != nil {
		return err
	}
	dir := RunCgroup(instance, key)
	if runPopulated(cgroupRoot, instance, key) {
		return nil
	}
	return removeCgroup(dir)
}

// writeCgroup writes a cgroup file, through sudo when it isn't the daemon's
// and the daemon runs in AgentBox's VM.
func writeCgroup(path, value string) error {
	err := os.WriteFile(path, []byte(value), 0)
	if errors.Is(err, syscall.EAGAIN) {
		return errReclaimShort
	}
	if err == nil || !errors.Is(err, fs.ErrPermission) || !hostos.InVM() {
		return err
	}
	return sudo("tee", value, path)
}

func makeCgroup(dir string) error {
	err := os.Mkdir(dir, 0o755)
	switch {
	case err == nil, errors.Is(err, fs.ErrExist):
		return nil
	case !errors.Is(err, fs.ErrPermission) || !hostos.InVM():
		return err
	}
	return sudo("mkdir", "", "-p", dir)
}

func removeCgroup(dir string) error {
	err := os.Remove(dir)
	switch {
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return nil
	case !errors.Is(err, fs.ErrPermission) || !hostos.InVM():
		return err
	}
	return sudo("rmdir", "", dir)
}

// sudo runs a command as root without asking for a password, with stdin.
func sudo(command, stdin string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", command}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "Resource temporarily unavailable") {
			return errReclaimShort
		}
		return fmt.Errorf("sudo %s %s: %w: %s", command, strings.Join(args, " "), err, msg)
	}
	return nil
}
