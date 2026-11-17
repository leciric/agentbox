package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const mib = 1 << 20

// fakeCgroup is a machine's cgroup in a temporary directory, whose
// memory.reclaim a test's writer answers.
func fakeCgroup(t *testing.T, current int64) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lxc.payload.m")
	writeCgroupFile(t, filepath.Join(dir, "memory.current"), strconv.FormatInt(current, 10)+"\n")
	writeCgroupFile(t, filepath.Join(dir, "cgroup.events"), "populated 1\nfrozen 0\n")
	return dir
}

func setCurrent(t *testing.T, dir string, n int64) {
	t.Helper()
	writeCgroupFile(t, filepath.Join(dir, "memory.current"), strconv.FormatInt(n, 10)+"\n")
}

// What spun on 2026-11-06: agent-37's machine at 58 MB and agent-34's at
// 12 MB, each asked for all of it. Near-empty cgroups are left alone now,
// and so are those that are gone or have nothing running.
func TestReclaimLeavesNearlyEmptyCgroupsAlone(t *testing.T) {
	t.Parallel()
	never := func(context.Context, string, string, time.Duration) error {
		t.Error("asked a cgroup with next to nothing in it to reclaim")
		return nil
	}
	for _, current := range []int64{12 * mib, 58 * mib, reclaimFloor + reclaimMinAsk - 1} {
		dir := fakeCgroup(t, current)
		if before, after, err := reclaimCgroup(t.Context(), dir, never); err != nil || before != current || after != current {
			t.Errorf("reclaimCgroup at %d = %d, %d, %v", current, before, after, err)
		}
	}
	gone := filepath.Join(t.TempDir(), "lxc.payload.gone")
	if _, _, err := reclaimCgroup(t.Context(), gone, never); err != nil {
		t.Errorf("a cgroup that's gone: %v", err)
	}
	stopped := fakeCgroup(t, 4<<30)
	writeCgroupFile(t, filepath.Join(stopped, "cgroup.events"), "populated 0\nfrozen 0\n")
	if _, _, err := reclaimCgroup(t.Context(), stopped, never); err != nil {
		t.Errorf("a cgroup with nothing running: %v", err)
	}
}

// Never "everything current": what's above the floor, at most reclaimMax,
// in chunks.
func TestReclaimAsksForABoundedAmountInChunks(t *testing.T) {
	t.Parallel()
	dir := fakeCgroup(t, 16<<30)
	current := int64(16 << 30)
	var asked []int64
	before, after, err := reclaimCgroup(t.Context(), dir, func(_ context.Context, path, value string, timeout time.Duration) error {
		n, _ := strconv.ParseInt(value, 10, 64)
		if filepath.Base(path) != "memory.reclaim" || timeout != reclaimWriteTimeout {
			t.Errorf("wrote %s with %s", path, timeout)
		}
		asked = append(asked, n)
		current -= n
		setCurrent(t, dir, current)
		return nil
	})
	var total int64
	for _, n := range asked {
		total += n
		if n > reclaimChunk {
			t.Errorf("asked for %d at once", n)
		}
	}
	if err != nil || total != reclaimMax || before != 16<<30 || after != 16<<30-reclaimMax {
		t.Errorf("reclaimCgroup = %d, %d, %v, asking %d in all", before, after, err, total)
	}

	// Just above the floor: only what's above it.
	dir = fakeCgroup(t, reclaimFloor+100*mib)
	asked = nil
	if _, _, err := reclaimCgroup(t.Context(), dir, func(_ context.Context, _, value string, _ time.Duration) error {
		n, _ := strconv.ParseInt(value, 10, 64)
		asked = append(asked, n)
		return errReclaimShort
	}); err != nil || !slices.Equal(asked, []int64{100 * mib}) {
		t.Errorf("asked %v, %v", asked, err)
	}
}

// A cgroup whose pages come straight back in (memory.current no lower
// after a write), or one the kernel falls short on, is asked once, not
// again and again.
func TestReclaimStopsWhenNothingComesOut(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]error{"no progress": nil, "fell short": errReclaimShort} {
		dir := fakeCgroup(t, 8<<30)
		writes := 0
		if _, _, err := reclaimCgroup(t.Context(), dir, func(context.Context, string, string, time.Duration) error {
			writes++
			return answer
		}); err != nil || writes != 1 {
			t.Errorf("%s: %d writes, %v", name, writes, err)
		}
	}
}

// A writer that never returns is cut off by the caller's context, and the
// reclaim's own deadline bounds it the same way.
func TestReclaimGivesUpOnAWriterThatNeverReturns(t *testing.T) {
	t.Parallel()
	dir := fakeCgroup(t, 8<<30)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err := reclaimCgroup(ctx, dir, func(ctx context.Context, path, _ string, _ time.Duration) error {
		<-ctx.Done() // the kernel spinning; a real writer is killed here
		return errReclaimTimeout
	})
	if !errors.Is(err, errReclaimTimeout) || time.Since(started) > 5*time.Second {
		t.Errorf("reclaimCgroup = %v after %s", err, time.Since(started))
	}
}

// One reclaim at a time: a second waits for the first, and gives up after
// reclaimWait rather than pile up. Not parallel: it holds the one slot.
func TestReclaimOneAtATime(t *testing.T) {
	saved := reclaimWait
	reclaimWait = 200 * time.Millisecond
	t.Cleanup(func() { reclaimWait = saved })

	first, second := fakeCgroup(t, 8<<30), fakeCgroup(t, 8<<30)
	inFirst, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _, _ = reclaimCgroup(t.Context(), first, func(context.Context, string, string, time.Duration) error {
			close(inFirst)
			<-release
			return errReclaimShort
		})
	})
	<-inFirst
	wrote := false
	if _, _, err := reclaimCgroup(t.Context(), second, func(context.Context, string, string, time.Duration) error {
		wrote = true
		return nil
	}); !errors.Is(err, errReclaimBusy) || wrote {
		t.Errorf("a second reclaim while one runs: %v, wrote %v", err, wrote)
	}
	close(release)
	wg.Wait()
	if _, _, err := reclaimCgroup(t.Context(), second, func(context.Context, string, string, time.Duration) error {
		return errReclaimShort
	}); err != nil {
		t.Errorf("once the first is done: %v", err)
	}
}

// The real writer is killed at its deadline, with nothing left behind: a
// FIFO nobody reads blocks tee the way a spinning memory.reclaim does.
func TestWriteReclaimKillsAWriterThatNeverReturns(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tee"); err != nil {
		t.Skip("no tee")
	}
	path := filepath.Join(t.TempDir(), "memory.reclaim")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := writeReclaim(t.Context(), path, "1048576", 300*time.Millisecond)
	if !errors.Is(err, errReclaimTimeout) {
		t.Errorf("writeReclaim = %v", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("took %s to give up", took)
	}
	if pids := teesOn(path); len(pids) > 0 {
		t.Errorf("tee %v still writing %s", pids, path)
	}

	// Cancelled by its caller: the same.
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := writeReclaim(ctx, path, "1048576", time.Minute); !errors.Is(err, errReclaimTimeout) {
		t.Errorf("writeReclaim cancelled = %v", err)
	}
	if pids := teesOn(path); len(pids) > 0 {
		t.Errorf("tee %v still writing %s after its caller gave up", pids, path)
	}
}

func TestWriteReclaimWritesTheValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "memory.reclaim")
	writeFile(t, path, "")
	if err := writeReclaim(t.Context(), path, "42", time.Second); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "42" {
		t.Errorf("wrote %q", b)
	}
}

func teesOn(path string) []int {
	var out []int
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if strings.Contains(string(b), path) {
			out = append(out, pid)
		}
	}
	return out
}

func TestIsReclaimWriter(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]bool{
		"tee /sys/fs/cgroup/lxc.payload.ab-onestop-app-agent-37/memory.reclaim":                                           true,
		"sudo -n tee /sys/fs/cgroup/lxc.payload.ab-onestop-app-agent-34/memory.reclaim":                                   true,
		"sudo -n timeout --foreground -s KILL 10 tee /sys/fs/cgroup/memory.reclaim":                                       true,
		"/usr/bin/timeout --foreground -s KILL 10 tee /sys/fs/cgroup/memory.reclaim":                                      true,
		"tee /sys/fs/cgroup/lxc.payload.m/cgroup.freeze":                                                                  false,
		"bash -c pgrep -f '^tee /sys/fs/cgroup/.*memory[.]reclaim' || echo 1 | sudo -n tee /sys/fs/cgroup/memory.reclaim": false,
		"cat /sys/fs/cgroup/lxc.payload.m/memory.reclaim":                                                                 false,
		"/usr/local/bin/agentbox daemon":                                                                                  false,
		"vim /home/u/notes/sys/fs/cgroup/memory.reclaim":                                                                  false,
	} {
		if got := isReclaimWriter(strings.Fields(cmd)); got != want {
			t.Errorf("isReclaimWriter(%q) = %v", cmd, got)
		}
	}
}

func TestReclaimWritersReadsProc(t *testing.T) {
	t.Parallel()
	proc := t.TempDir()
	for pid, argv := range map[string]string{
		"37":   "tee\x00/sys/fs/cgroup/lxc.payload.ab-onestop-app-agent-37/memory.reclaim\x00",
		"34":   "sudo\x00-n\x00tee\x00/sys/fs/cgroup/lxc.payload.ab-onestop-app-agent-34/memory.reclaim\x00",
		"100":  "/usr/local/bin/agentbox\x00daemon\x00",
		"self": "tee\x00/sys/fs/cgroup/memory.reclaim\x00",
		"7":    "",
	} {
		writeCgroupFile(t, filepath.Join(proc, pid, "cmdline"), argv)
	}
	got := reclaimWriters(proc)
	slices.Sort(got)
	if !slices.Equal(got, []int{34, 37}) {
		t.Errorf("reclaimWriters = %v", got)
	}
}

// A writer left from before the daemon started is killed as it starts.
func TestKillReclaimWritersKillsLeftovers(t *testing.T) {
	t.Parallel()
	// A stand-in that looks like one (argv tee, a memory.reclaim under
	// /sys/fs/cgroup) and never ends.
	cmd := exec.Command("bash", "-c", "exec -a tee tail -f /dev/null /sys/fs/cgroup/agentbox-test-"+strconv.Itoa(os.Getpid())+"/memory.reclaim")
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(reclaimWriters("/proc"), cmd.Process.Pid) {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the stand-in never showed up as a reclaim writer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := KillReclaimWriters(); n < 1 {
		t.Errorf("KillReclaimWriters = %d", n)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Error("the leftover writer is still running")
	}
}

// The second case from the live VM: agents 15 and 34, procs 0, 49 MB and
// 9 MB current, a new `sudo tee …/memory.reclaim` every 90 seconds while
// the last never finished. Neither is asked now, and a cgroup already being
// reclaimed is skipped at once rather than given another writer.
func TestReclaimSkipsWhatIsBeingReclaimedAndEmptyMachines(t *testing.T) {
	t.Parallel()
	never := func(context.Context, string, string, time.Duration) error {
		t.Error("asked to reclaim")
		return nil
	}
	for _, current := range []int64{49 * mib, 9 * mib} {
		dir := fakeCgroup(t, current)
		writeCgroupFile(t, filepath.Join(dir, "cgroup.procs"), "")
		if _, _, err := reclaimCgroup(t.Context(), dir, never); err != nil {
			t.Error(err)
		}
	}
	// Plenty in it, nothing running: a stopped machine's leftovers.
	idle := fakeCgroup(t, 4<<30)
	writeCgroupFile(t, filepath.Join(idle, "cgroup.events"), "populated 0\nfrozen 0\n")
	if _, _, err := reclaimCgroup(t.Context(), idle, never); err != nil {
		t.Error(err)
	}

	dir := fakeCgroup(t, 8<<30)
	inFirst, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _, _ = reclaimCgroup(t.Context(), dir, func(context.Context, string, string, time.Duration) error {
			close(inFirst)
			<-release // the kernel spinning
			return errReclaimShort
		})
	})
	<-inFirst
	started := time.Now()
	if _, _, err := reclaimCgroup(t.Context(), dir, never); !errors.Is(err, errReclaimBusy) || time.Since(started) > time.Second {
		t.Errorf("the same cgroup again while it's reclaimed: %v after %s", err, time.Since(started))
	}
	close(release)
	wg.Wait()
}
