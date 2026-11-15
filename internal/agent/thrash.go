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

	"agentbox/internal/hostos"
)

// An agent thrashing at its own memory.max is killed from here, because the
// kernel won't do it soon enough. Its OOM killer runs only when reclaim
// fails outright, and an agent at its limit always has something to
// reclaim: its programs' own code, read back from the disk as soon as it
// runs, and its swapped pages, read back from swap. In the load lab
// (scripts/pressurelab.sh) a process holding its machine's memory.max plus a
// GiB thrashed for 60 seconds with no swap at all, reading 22 GiB from the
// VM's disk, and for more than 150 with a quarter of its limit in swap,
// reading 64 GiB, before the kernel killed it; the daemon's requests took up
// to 9 seconds meanwhile, waiting on the same disk.
//
// So, as systemd-oomd does with memory pressure: an agent that has spent
// everything it may have — its memory.max in RAM and its memory.swap.max in
// swap — and stays so look after look, its tasks stalled on memory for at
// least ThrashShare of that time (memory.pressure's full), gets the process
// holding the most memory in it killed (internal/daemon/pressure.go): what
// the kernel would do if its code had nowhere to be read back from. The
// stall is measured between looks from the cumulative total, not avg10,
// which lags by seconds; and it is a share of the whole span, because a
// single-threaded process thrashing runs between its faults: in the load
// lab its stall swung from 20% to 100% from one two-second look to the next,
// while the disk was saturated throughout.

// ThrashShare is the least share of the time an agent that has spent its
// memory and swap spends with every task stalled on memory to be thrashing.
// A build that fits just stalls now and then, and hasn't filled its swap.
const ThrashShare = 0.2

// ThrashSample is one look at whether an agent's machine is thrashing at its
// own limit.
type ThrashSample struct {
	At      time.Time     // when it was read
	Stalled time.Duration // memory.pressure's full total: all its tasks stalled on memory, ever
	// Spent is a machine at its memory.max with its swap full: at its
	// memory.swap.max, or none to be had.
	Spent bool
}

// StalledShare is the share of the time since an earlier sample a machine
// spent with every task stalled on memory; 0 when no time passed.
func (s ThrashSample) StalledShare(since ThrashSample) float64 {
	span := s.At.Sub(since.At)
	if span <= 0 {
		return 0
	}
	return float64(s.Stalled-since.Stalled) / float64(span)
}

// Thrash reads an agent's machine's ThrashSample; ok is false when its cgroup
// isn't there or has no PSI.
func Thrash(instance string) (ThrashSample, bool) {
	return thrash(agentCgroup(cgroupRoot, instance), time.Now())
}

func thrash(dir string, now time.Time) (ThrashSample, bool) {
	out := ThrashSample{At: now}
	b, err := os.ReadFile(filepath.Join(dir, "memory.pressure"))
	if err != nil {
		return out, false
	}
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "full ")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(rest) {
			if v, ok := strings.CutPrefix(f, "total="); ok {
				us, err := strconv.ParseInt(v, 10, 64)
				out.Stalled, found = time.Duration(us)*time.Microsecond, err == nil
			}
		}
	}
	if !found {
		return out, false
	}
	read := func(f string) string { return strings.TrimSpace(readString(filepath.Join(dir, f))) }
	limit, err1 := strconv.ParseInt(read("memory.max"), 10, 64)
	current, err2 := strconv.ParseInt(read("memory.current"), 10, 64)
	if err1 != nil || err2 != nil {
		return out, true // no limit: never spent
	}
	atLimit := current >= limit-max(limit/100, 16<<20)
	swapped, _ := strconv.ParseInt(read("memory.swap.current"), 10, 64)
	var swapFull bool
	switch swapMax := read("memory.swap.max"); swapMax {
	case "max":
		// Set to none only when the VM has no swap (SwapLimit): full when
		// it has nothing in it.
		swapFull = swapped == 0
	case "":
		swapFull = true // no swap accounting: it can't be told apart
	default:
		n, err := strconv.ParseInt(swapMax, 10, 64)
		swapFull = err == nil && swapped >= n-n/20
	}
	out.Spent = atLimit && swapFull
	return out, true
}

// errNoVictim is a machine with no process the kernel would kill for memory.
var errNoVictim = errors.New("no process to kill")

// KillLargest kills the process in an agent's machine that holds the most
// memory, in RAM and in swap, never one whose oom_score_adj is -1000. Not the
// highest oom_score, as the kernel's OOM killer picks: in the load lab that
// was a dbus-daemon holding 12 KiB, its oom_score raised by an oom_score_adj
// its machine's systemd gave it, and killing it freed nothing. It kills
// through sudo when the daemon may not itself (in AgentBox's VM, the
// machine's processes are other users').
func KillLargest(instance string) (OOMVictim, error) {
	return killLargest(agentCgroup(cgroupRoot, instance), "/proc", killProcess)
}

func killLargest(dir, proc string, kill func(pid int) error) (OOMVictim, error) {
	var best OOMVictim
	var bestHeld int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // a cgroup that went meanwhile
		}
		if d.IsDir() || d.Name() != "cgroup.procs" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, f := range strings.Fields(string(b)) {
			pid, err := strconv.Atoi(f)
			if err != nil {
				continue
			}
			status := readString(filepath.Join(proc, f, "status"))
			rss := statusBytes(status, "VmRSS:")
			held := rss + statusBytes(status, "VmSwap:")
			if held <= bestHeld {
				continue
			}
			if adj, err := readInt(filepath.Join(proc, f, "oom_score_adj")); err == nil && adj == -1000 {
				continue
			}
			bestHeld = held
			best = OOMVictim{PID: pid, Name: strings.TrimSpace(readString(filepath.Join(proc, f, "comm"))), RSS: rss}
		}
		return nil
	})
	if err != nil {
		return OOMVictim{}, err
	}
	if bestHeld == 0 {
		return OOMVictim{}, errNoVictim
	}
	if err := kill(best.PID); err != nil {
		return best, fmt.Errorf("killing %s (pid %d): %w", best.Name, best.PID, err)
	}
	return best, nil
}

func killProcess(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err == nil || !errors.Is(err, syscall.EPERM) || !hostos.InVM() {
		return err
	}
	return sudo("kill", "", "-KILL", strconv.Itoa(pid))
}

// statusBytes reads a "Name:   123 kB" line of a /proc/<pid>/status in bytes.
func statusBytes(status, name string) int64 {
	for _, line := range strings.Split(status, "\n") {
		if v, ok := strings.CutPrefix(line, name); ok {
			kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb << 10
		}
	}
	return 0
}
