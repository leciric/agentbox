package chv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The VM runs below the desktop: its supervisor, and so Cloud Hypervisor,
// virtiofsd, passt and crosvm, which it starts, get the CPU and the disk
// after the user's own programs when both want them. Run from the app, the
// supervisor was in the app's own scope at nice 0, level with the desktop, and
// a build in the VM held the compositor up.
//
//   - The supervisor starts in a systemd user scope of its own
//     (supervisorArgv), in app.slice beside the user's apps, with
//     vmCPUWeight and vmIOWeight against their 100. Weights only count
//     against siblings, so not a slice of its own, where it would have none.
//     IOWeight needs the io controller delegated to the user's manager and
//     a scheduler that honours it (BFQ, or io.cost); systemd delegates only
//     cpu, memory and pids by default, and NVMe drives default to none, so
//     it's often a no-op: the disk is spared mostly by not reading it again
//     (memory.go's cache floor).
//   - The scope also bounds the VM's memory on the host's side, the last
//     resort beneath memory.go's sizing (scopeMemory, limitScope):
//     MemoryHigh at the cap and memScopeSlack for what runs it, past which
//     the host's kernel reclaims from the VM's scope rather than the
//     desktop (the guest's memory is shared memory, which only swap can take
//     back, so the VM stalls in its stead), and MemoryMax a little above,
//     past which the kernel ends the VM rather than the desktop's programs.
//     It needs the memory controller delegated to the user's manager, which
//     systemd does by default; without it, or outside a scope of its own,
//     the VM's log says so.
//   - Without systemd (or its user manager), and inside the scope anyway,
//     the supervisor lowers its own priority as it starts (lowerPriority):
//     nice vmNice, and best-effort I/O at its lowest level. Not the idle
//     I/O class: under BFQ that starves the guest's disk outright while the
//     desktop reads, and the guest's ext4 journal and hung-task watchdog then
//     time out.
const (
	vmCPUWeight = 20
	vmIOWeight  = 20
	vmNice      = 10
	// memScopeSlack is what the scope may hold beyond the VM's cap before
	// the kernel reclaims from it: Cloud Hypervisor, virtiofsd, passt and
	// crosvm themselves, and what virtiofsd caches of the home.
	memScopeSlack = 512 << 20
	// vmScopePrefix starts the name of the scope the supervisor runs in.
	vmScopePrefix = "agentbox-vm-"
	// ioprioBestEffortLowest is IOPRIO_PRIO_VALUE(IOPRIO_CLASS_BE, 7).
	ioprioBestEffortLowest = 2<<13 | 7
	ioprioWhoProcess       = 1
)

// systemdScope reports whether a transient systemd user scope with the VM's
// weights can be made here, by making one for `true`; tests replace it.
var systemdScope = func() bool {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemd-run", append(scopeArgs("agentbox-probe", 0), "true")...).Run() == nil
}

// scopeArgs are systemd-run's arguments for a scope named after unit, its
// memory bounded for a VM of memCap (scopeMemory) unless that's 0.
func scopeArgs(unit string, memCap int64) []string {
	args := []string{
		"--user", "--scope", "--quiet", "--collect",
		"--slice=app.slice",
		"--unit=" + unit + "-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		"--description=AgentBox's VM",
		"-p", "CPUWeight=" + strconv.Itoa(vmCPUWeight),
		"-p", "IOWeight=" + strconv.Itoa(vmIOWeight),
	}
	if memCap > 0 {
		high, hard := scopeMemory(memCap)
		args = append(args, "-p", "MemoryHigh="+strconv.FormatInt(high, 10), "-p", "MemoryMax="+strconv.FormatInt(hard, 10))
	}
	return append(args, "--")
}

// supervisorArgv is the command line the VM c's supervisor starts with:
// self vm run, in a scope of its own where systemd can make one, bounded for
// its cap as the supervisor will fit it to the host. systemd-run execs it in
// its own place, so the pid Start waits on is the supervisor's.
func supervisorArgv(self string, c Config) []string {
	run := []string{self, "vm", "run"}
	if !systemdScope() {
		return run
	}
	fit, _ := FitHost(c, hostTotal())
	return append(append([]string{"systemd-run"}, scopeArgs(vmScopePrefix+unitSafe(c.Name), CapFor(fit))...), run...)
}

// scopeMemory is the VM's scope's MemoryHigh and MemoryMax for a VM whose
// memory may grow to memCap: the cap and memScopeSlack, and a sixteenth more,
// a GiB at least.
func scopeMemory(memCap int64) (high, hard int64) {
	high = memCap + memScopeSlack
	return high, high + max(GiB, memCap/16)
}

// setScopeMemory sets a running scope's MemoryHigh and MemoryMax; tests
// replace it.
var setScopeMemory = func(unit string, high, hard int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "set-property", "--runtime", unit,
		"MemoryHigh="+strconv.FormatInt(high, 10), "MemoryMax="+strconv.FormatInt(hard, 10)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// limitScope bounds the VM's scope for a cap of memCap, as the supervisor
// starts (a cap FitHost lowered, or a scope from before this) and when the
// cap changes, through systemd, or on the scope's own cgroup when systemctl
// can't. Never below what the scope holds now and some, which would stall
// the VM at once or end it. Only a scope of the VM's own: the app's, which
// the supervisor runs in without systemd-run, isn't its to bound. It says
// what holds, for the VM's log.
func limitScope(memCap int64) string {
	dir := ownCgroup()
	unit := filepath.Base(dir)
	if dir == "" || !strings.HasPrefix(unit, vmScopePrefix) || !strings.HasSuffix(unit, ".scope") {
		return "not in a scope of its own: nothing on this computer's side bounds the VM's memory but its own sizing"
	}
	cg := filepath.Join(cgroupRoot, dir)
	if _, err := os.Stat(filepath.Join(cg, "memory.high")); err != nil {
		return "no memory controller (not delegated to systemd's user manager): nothing on this computer's side bounds the VM's memory but its own sizing"
	}
	high, hard := scopeMemory(memCap)
	if cur, err := strconv.ParseInt(readTrim(filepath.Join(cg, "memory.current")), 10, 64); err == nil && cur > 0 {
		high = max(high, cur+memScopeSlack)
		hard = max(hard, high+GiB)
	}
	if err := setScopeMemory(unit, high, hard); err != nil {
		// The cgroup's own files: the higher first, so they never cross.
		werr := os.WriteFile(filepath.Join(cg, "memory.max"), []byte(strconv.FormatInt(hard, 10)), 0o644)
		if werr == nil {
			werr = os.WriteFile(filepath.Join(cg, "memory.high"), []byte(strconv.FormatInt(high, 10)), 0o644)
		}
		if werr != nil {
			return fmt.Sprintf("bounding the VM's scope failed (systemctl: %v; its cgroup: %v)", err, werr)
		}
	}
	return fmt.Sprintf("on this computer the VM's scope may hold %s before the kernel reclaims from it (MemoryHigh), and %s at most (MemoryMax)", gib(high), gib(hard))
}

// unitSafe is s with what a unit name can't hold replaced.
func unitSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		}
		return '-'
	}, s)
}

// lowerPriority lowers the supervisor's CPU and I/O priority, which every
// program it starts inherits. Linux keeps both per thread, and a thread
// inherits them from the thread that made it, so it sets them on each of
// the threads the Go runtime has made so far; any later one comes from one
// of those. With the scheduler's autogroups on (no cpu cgroup), its session
// (it runs in one of its own) is lowered too. It returns what it couldn't do.
func lowerPriority() []string {
	var failed []string
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return []string{err.Error()}
	}
	for _, t := range tasks {
		tid, err := strconv.Atoi(t.Name())
		if err != nil {
			continue
		}
		if cur, err := unix.Getpriority(unix.PRIO_PROCESS, tid); err == nil && 20-cur < vmNice {
			if err := unix.Setpriority(unix.PRIO_PROCESS, tid, vmNice); err != nil && len(failed) == 0 {
				failed = append(failed, "nice: "+err.Error())
			}
		}
		if _, _, errno := unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), ioprioBestEffortLowest); errno != 0 && len(failed) < 2 {
			failed = append(failed, "ionice: "+errno.Error())
		}
	}
	// Absent without CONFIG_SCHED_AUTOGROUP; ignored while in a cpu cgroup.
	_ = os.WriteFile("/proc/self/autogroup", []byte(strconv.Itoa(vmNice)), 0)
	return failed
}

// cgroupRoot is where cgroup v2 is mounted, and sysRoot /sys; tests point
// them at fixtures.
var (
	cgroupRoot = "/sys/fs/cgroup"
	sysRoot    = "/sys"
)

// priorityGaps says which of the ways the VM yields to the desktop don't
// hold here, for the VM's log, so a desktop that still stalls under a build
// can be explained from it: the scope's CPU weight not applied, its I/O
// weight without the io controller (systemd delegates cpu, memory and pids
// to a user's manager by default), and a disk scheduler that ignores both
// I/O weights and priorities ("none", NVMe's default), under which the VM's
// disk reads and writes compete with the desktop's evenly. disk is a file on
// the disk the VM's images are on. Empty when every one holds.
func priorityGaps(disk string) []string {
	var gaps []string
	dir := ownCgroup()
	if dir == "" {
		gaps = append(gaps, "not in a cgroup of its own: CPU and I/O weights don't apply")
	} else {
		if w := readTrim(filepath.Join(cgroupRoot, dir, "cpu.weight")); w != "" && w != strconv.Itoa(vmCPUWeight) {
			gaps = append(gaps, "CPU weight "+w+", not "+strconv.Itoa(vmCPUWeight))
		} else if w == "" {
			gaps = append(gaps, "no cpu controller: only nice lowers the VM's CPU")
		}
		if _, err := os.Stat(filepath.Join(cgroupRoot, dir, "io.weight")); err != nil {
			gaps = append(gaps, "no io controller (not delegated to systemd's user manager): the VM's I/O weight does nothing")
		}
	}
	if s := diskScheduler(disk); s == "none" {
		gaps = append(gaps, "the disk's I/O scheduler is none: I/O weights and priorities do nothing, and the VM's disk competes with the desktop's evenly")
	}
	return gaps
}

// ownCgroup is this process's cgroup v2 path, "" when it's the root or
// unknown; tests replace it.
var ownCgroup = func() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(b)) {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok && p != "/" {
			return p
		}
	}
	return ""
}

// diskScheduler is the active I/O scheduler of the disk path is on, "" when
// it can't be read (a network or virtual filesystem).
func diskScheduler(path string) string {
	var st unix.Stat_t
	if unix.Stat(path, &st) != nil {
		return ""
	}
	link := filepath.Join(sysRoot, "dev", "block", fmt.Sprintf("%d:%d", unix.Major(st.Dev), unix.Minor(st.Dev)))
	// A link into /sys/devices; a partition's queue is its disk's, one
	// directory up, which filepath.Join's cleaning can't find from the link.
	dev, err := filepath.EvalSymlinks(link)
	if err != nil {
		return ""
	}
	for _, q := range []string{filepath.Join(dev, "queue", "scheduler"), filepath.Join(dev, "..", "queue", "scheduler")} {
		if s := readTrim(q); s != "" {
			return activeScheduler(s)
		}
	}
	return ""
}

// activeScheduler is the bracketed one of a queue/scheduler file's
// "mq-deadline kyber [bfq] none".
func activeScheduler(s string) string {
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.IndexByte(s[i:], ']'); j > 0 {
			return s[i+1 : i+j]
		}
	}
	return strings.TrimSpace(s)
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
