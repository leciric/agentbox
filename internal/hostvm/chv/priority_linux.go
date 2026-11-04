package chv

import (
	"context"
	"os"
	"os/exec"
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
	return exec.CommandContext(ctx, "systemd-run", append(scopeArgs("agentbox-probe"), "true")...).Run() == nil
}

// scopeArgs are systemd-run's arguments for a scope named after unit.
func scopeArgs(unit string) []string {
	return []string{
		"--user", "--scope", "--quiet", "--collect",
		"--slice=app.slice",
		"--unit=" + unit + "-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		"--description=AgentBox's VM",
		"-p", "CPUWeight=" + strconv.Itoa(vmCPUWeight),
		"-p", "IOWeight=" + strconv.Itoa(vmIOWeight),
		"--",
	}
}

// supervisorArgv is the command line the VM named name's supervisor starts
// with: self vm run, in a scope of its own where systemd can make one.
// systemd-run execs it in its own place, so the pid Start waits on is the
// supervisor's.
func supervisorArgv(self, name string) []string {
	run := []string{self, "vm", "run"}
	if !systemdScope() {
		return run
	}
	return append(append([]string{"systemd-run"}, scopeArgs("agentbox-vm-"+unitSafe(name))...), run...)
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
