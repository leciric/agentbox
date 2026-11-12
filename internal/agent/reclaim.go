package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentbox/internal/hostos"
)

// Pushing a cgroup's memory out (memory.reclaim) is bounded, killable and
// one at a time. On 2026-11-06 two `sudo tee …/memory.reclaim` writers,
// orphaned when the 30-second sudo timeout killed sudo and not tee, spun for
// 15 minutes each on cgroups holding 58 MB and 12 MB: asked for "all of
// memory.current", the kernel kept swapping pages out that the machine
// faulted straight back in, and the VM read 474 MiB/s from the host's disk
// until the PC froze. So:
//
//   - a cgroup holding less than reclaimFloor above what it keeps is left
//     alone, and no more than reclaimMax is asked for, in reclaimChunk
//     writes, stopping as soon as the kernel falls short or memory.current
//     stops going down: a cgroup with nothing cold left isn't churned;
//   - every write is a child process (tee) with a deadline, killed with its
//     process group when it passes or the caller gives up, and through sudo
//     wrapped in timeout(1) as root, so it dies even if the daemon does;
//   - one reclaim at a time in the VM (reclaimSlot), and one waiting for
//     another gives up after reclaimWait rather than pile up;
//   - the daemon kills writers a daemon before it left (KillReclaimWriters).
const (
	// reclaimFloor is what a cgroup keeps: below it, reclaim pushes pages
	// out that its processes need and fault straight back.
	reclaimFloor int64 = 256 << 20
	// reclaimMinAsk is the least worth asking for: less is a cgroup with
	// next to nothing to give.
	reclaimMinAsk int64 = 32 << 20
	// reclaimMax is the most one reclaim asks for, and reclaimChunk the most
	// one write does.
	reclaimMax   int64 = 4 << 30
	reclaimChunk int64 = 128 << 20
	// reclaimTimeout bounds a whole reclaim, reclaimWriteTimeout one write
	// (its writer killed after), and reclaimWait the wait for another.
	reclaimTimeout      = 30 * time.Second
	reclaimWriteTimeout = 10 * time.Second
)

// reclaimWait is how long a reclaim waits for another before giving up; a
// variable for tests.
var reclaimWait = 30 * time.Second

// reclaimSlot is held through a reclaim: one at a time in the VM.
var reclaimSlot = make(chan struct{}, 1)

// reclaiming is the cgroups being reclaimed now: one asked again meanwhile
// is skipped at once, not queued. On 2026-11-06 six writers piled up on two
// machines, one more every 90 seconds.
var (
	reclaimingMu sync.Mutex
	reclaiming   = map[string]bool{}
)

// errReclaimBusy is a reclaim that didn't start because another went on
// for longer than reclaimWait.
var errReclaimBusy = errors.New("another reclaim is in progress")

// errReclaimTimeout is a write to memory.reclaim killed at its deadline.
var errReclaimTimeout = errors.New("reclaim write timed out and was killed")

// reclaimWriter writes value to a memory.reclaim file within timeout, or
// kills what writes it; errReclaimShort when the kernel fell short.
type reclaimWriter func(ctx context.Context, path, value string, timeout time.Duration) error

// reclaimCgroup pushes out what the cgroup at dir holds above reclaimFloor,
// at most reclaimMax, and says what it held before and after. A cgroup that
// is gone, empty or near its floor is left alone.
func reclaimCgroup(ctx context.Context, dir string, write reclaimWriter) (before, after int64, err error) {
	before, err = cgroupCurrent(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if !cgroupPopulated(dir) || before-reclaimFloor < reclaimMinAsk {
		return before, before, nil
	}
	reclaimingMu.Lock()
	if reclaiming[dir] {
		reclaimingMu.Unlock()
		return before, before, errReclaimBusy
	}
	reclaiming[dir] = true
	reclaimingMu.Unlock()
	defer func() {
		reclaimingMu.Lock()
		delete(reclaiming, dir)
		reclaimingMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, reclaimTimeout)
	defer cancel()
	wait := time.NewTimer(reclaimWait)
	defer wait.Stop()
	select {
	case reclaimSlot <- struct{}{}:
	case <-wait.C:
		return before, before, errReclaimBusy
	case <-ctx.Done():
		return before, before, ctx.Err()
	}
	defer func() { <-reclaimSlot }()

	left := min(before-reclaimFloor, reclaimMax)
	current := before
	for left >= reclaimMinAsk && ctx.Err() == nil {
		ask := min(left, reclaimChunk)
		err := write(ctx, filepath.Join(dir, "memory.reclaim"), strconv.FormatInt(ask, 10), reclaimWriteTimeout)
		short := errors.Is(err, errReclaimShort)
		if err != nil && !short {
			after, _ = cgroupCurrent(dir)
			return before, after, err
		}
		left -= ask
		now, rerr := cgroupCurrent(dir)
		// Fell short, gone, or no lower than before the write: what it
		// pushes out comes straight back, and asking again only churns swap.
		if short || rerr != nil || now >= current || now-reclaimFloor < reclaimMinAsk {
			current = now
			break
		}
		current = now
	}
	return before, current, nil
}

// writeReclaim is the real reclaimWriter: tee in a process group of its
// own, killed with it at the deadline or when ctx ends, and through sudo
// (as root, inside timeout(1), which kills it even if the daemon dies
// first) when the file isn't the daemon's.
func writeReclaim(ctx context.Context, path, value string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, args := "tee", []string{path}
	viaSudo := syscall.Access(path, 2 /* W_OK */) != nil && hostos.InVM()
	if viaSudo {
		secs := strconv.Itoa(max(int(timeout/time.Second), 1))
		name, args = "sudo", []string{"-n", "timeout", "--foreground", "-s", "KILL", secs, "tee", path}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(value)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Its own process group, ended with it: tee directly (ours), or sudo,
	// which passes SIGTERM on to timeout and tee (root's) before it goes.
	// Pdeathsig ends tee with the daemon (the kernel drops it for sudo,
	// set-user-ID, where timeout's own deadline does it).
	cmd.SysProcAttr = reclaimProcAttr()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		sig := syscall.SIGKILL
		if viaSudo {
			sig = syscall.SIGTERM // relayed; Wait kills sudo after WaitDelay
		}
		return syscall.Kill(-cmd.Process.Pid, sig)
	}
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", path, errReclaimTimeout)
	}
	msg := strings.TrimSpace(stderr.String())
	if strings.Contains(msg, "Resource temporarily unavailable") {
		return errReclaimShort
	}
	var exit *exec.ExitError
	if viaSudo && errors.As(err, &exit) && (exit.ExitCode() == 124 || exit.ExitCode() == 137) { // timed out, or killed by its KILL
		return fmt.Errorf("%s: %w", path, errReclaimTimeout)
	}
	return fmt.Errorf("writing %s: %w: %s", path, err, msg)
}

// KillReclaimWriters kills the memory.reclaim writers left in this machine,
// by a daemon before this one or anyone else, and says how many: nothing
// should be pushing memory out while the daemon starts, and one left over
// can spin for as long as the machine runs.
func KillReclaimWriters() int {
	pids := reclaimWriters("/proc")
	var theirs []string
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			theirs = append(theirs, strconv.Itoa(pid))
		}
	}
	if len(theirs) > 0 && hostos.InVM() {
		_ = sudo("kill", "", append([]string{"-KILL"}, theirs...)...)
	}
	return len(pids)
}

// reclaimWriters are the processes under proc writing a memory.reclaim
// file: tee (or timeout or sudo running it) with one as an argument.
func reclaimWriters(proc string) []int {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		b, err := os.ReadFile(filepath.Join(proc, e.Name(), "cmdline"))
		if err != nil || len(b) == 0 {
			continue
		}
		if isReclaimWriter(strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")) {
			out = append(out, pid)
		}
	}
	return out
}

func isReclaimWriter(argv []string) bool {
	switch filepath.Base(argv[0]) {
	case "tee", "timeout", "sudo":
	default:
		return false
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "/sys/fs/cgroup/") && strings.HasSuffix(a, "/memory.reclaim") {
			return true
		}
	}
	return false
}
