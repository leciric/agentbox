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
// So, as systemd-oomd does with memory pressure: an agent whose cgroup has
// spent ThrashFull percent of the last ten seconds with every task stalled
// on memory (memory.pressure's full avg10), while it keeps reaching its own
// memory.max (memory.events.local's max count going up, so it is its own
// limit it is up against, not the VM running short), for ThrashFor, gets the
// process the kernel's OOM killer would pick killed: the highest oom_score
// in its machine (internal/daemon/pressure.go).

// ThrashFull is the memory.pressure full avg10, in percent, from which an
// agent at its own limit is thrashing. A build at its limit that is just
// short of memory stalls now and then; at 40% its tasks wait on the disk
// for most of every second.
const ThrashFull = 40.0

// ThrashSample is one look at whether an agent's machine is thrashing at its
// own limit.
type ThrashSample struct {
	Full    float64 // memory.pressure's full avg10, in percent
	AtLimit int64   // memory.events.local's max: times it reached its own memory.max
}

// Thrash reads an agent's machine's ThrashSample; ok is false when its cgroup
// isn't there or has no PSI.
func Thrash(instance string) (ThrashSample, bool) {
	return thrash(agentCgroup(cgroupRoot, instance))
}

func thrash(dir string) (ThrashSample, bool) {
	var out ThrashSample
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
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				out.Full, err = strconv.ParseFloat(v, 64)
				found = err == nil
			}
		}
	}
	if !found {
		return out, false
	}
	if b, err := os.ReadFile(filepath.Join(dir, "memory.events.local")); err == nil {
		out.AtLimit = eventCount(b, "max")
	}
	return out, true
}

// errNoVictim is a machine with no process the kernel would kill for memory.
var errNoVictim = errors.New("no process to kill")

// KillLargest kills the process in an agent's machine that the kernel's OOM
// killer would: the highest /proc/<pid>/oom_score, never one whose
// oom_score_adj is -1000. It kills through sudo when the daemon may not
// itself (in AgentBox's VM, the machine's processes are other users').
func KillLargest(instance string) (OOMVictim, error) {
	return killLargest(agentCgroup(cgroupRoot, instance), "/proc", killProcess)
}

func killLargest(dir, proc string, kill func(pid int) error) (OOMVictim, error) {
	var best OOMVictim
	bestScore := int64(-1)
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
			score, err := readInt(filepath.Join(proc, f, "oom_score"))
			if err != nil || score <= bestScore {
				continue
			}
			if adj, err := readInt(filepath.Join(proc, f, "oom_score_adj")); err == nil && adj == -1000 {
				continue
			}
			bestScore = score
			best = OOMVictim{PID: pid, Name: strings.TrimSpace(readString(filepath.Join(proc, f, "comm"))), RSS: statusBytes(filepath.Join(proc, f, "status"), "VmRSS:")}
		}
		return nil
	})
	if err != nil {
		return OOMVictim{}, err
	}
	if bestScore < 0 {
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

// statusBytes reads a "Name:   123 kB" line of /proc/<pid>/status in bytes.
func statusBytes(path, name string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, name); ok {
			kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb << 10
		}
	}
	return 0
}
