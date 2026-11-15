//go:build pressurelab

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCapsLab runs a memory hog, and then a disk-heavy writer, in a cgroup set
// up the way the daemon sets up an agent's machine (lxc.payload.<instance>,
// with SetMemoryLimit at MemoryLimit's share of LAB_VM_MB and SetIOWeight),
// while it times what the daemon and Incus do from outside it: a small
// write+fsync, and LAB_PROBE when given (`incus list --format csv` in the VM).
// It checks the hog is killed at the agent's limit, and logs how the probes
// fared. As root, in AgentBox's VM or an agent's machine:
//
//	sudo env PATH=$PATH go test -tags pressurelab -run TestCapsLab -v ./internal/agent
//
// The hog's cgroup gets no swap (LAB_SWAP=1 gives it the VM's, as agents
// have), so a lab in an agent doesn't fill the VM's zram.
func TestCapsLab(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to make cgroups")
	}
	vm := int64(labInt("LAB_VM_MB", 4096)) << 20
	limit := MemoryLimit(vm, labInt("LAB_SHARE", 0))
	instance := "capslab-" + strconv.Itoa(os.Getpid())
	dir := filepath.Join(cgroupRoot, "lxc.payload."+instance)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeLab(dir) })
	if err := SetMemoryLimit(instance, limit); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LAB_SWAP") != "1" {
		mustWrite(t, filepath.Join(dir, "memory.swap.max"), "0")
	}
	if err := SetIOWeight(instance, AgentIOWeight); err != nil {
		t.Fatal(err)
	}
	t.Logf("agent cgroup %s: memory.max %s (%d%% of a %s VM), io.weight %q, io.bfq.weight %q",
		filepath.Base(dir), HumanBytes(limit), labInt("LAB_SHARE", DefaultAgentMemoryShare), HumanBytes(vm),
		strings.TrimSpace(readString(filepath.Join(dir, "io.weight"))), strings.TrimSpace(readString(filepath.Join(dir, "io.bfq.weight"))))
	for _, f := range mustGlob(t, "/sys/block/vd*/queue/scheduler") {
		t.Logf("%s: %s", f, strings.TrimSpace(readString(f)))
	}

	work := t.TempDir()
	quiet := runProbes(t, work, 3*time.Second)
	t.Logf("idle:      %s", quiet)

	// The memory hog: 64 MiB at a time, touched, up to twice the limit.
	hogMB := labInt("LAB_HOG_MB", int(limit>>20)*2)
	hog := `import time
b = []
for i in range(` + strconv.Itoa(hogMB/64) + `):
    b.append(bytearray(64 << 20))
    for j in range(0, 64 << 20, 4096): b[-1][j] = 1
    if i % 8 == 0: print(f"{(i+1)*64} MiB", flush=True)
print("hog survived", flush=True)`
	start := time.Now()
	stop := make(chan struct{})
	var during probeStats
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); during = probeUntil(t, work, stop) }()
	out, err := inCgroup(dir, "python3", "-c", hog).CombinedOutput()
	close(stop)
	wg.Wait()
	counts, _ := MemoryEvents(instance)
	peak := strings.TrimSpace(readString(filepath.Join(dir, "memory.peak")))
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	t.Logf("memory hog: %v after %s, last said %q; oom_kill %d (own limit %d), memory.peak %s",
		err, time.Since(start).Round(time.Millisecond), lines[len(lines)-1], counts.Kills, counts.OwnLimit, peak)
	t.Logf("hog:       %s", during)
	if err == nil || strings.Contains(string(out), "hog survived") {
		t.Errorf("the hog wasn't stopped at %s", HumanBytes(limit))
	}
	if counts.Kills < 1 || counts.OwnLimit < 1 {
		t.Errorf("no kill at the agent's own limit: %+v", counts)
	}
	if p, _ := strconv.ParseInt(peak, 10, 64); p > limit {
		t.Errorf("memory.peak %d is over the limit %d", p, limit)
	}

	// The disk-heavy build: LAB_DISK_MB written and synced, in the agent's
	// cgroup, while the probes run outside it.
	if mb := labInt("LAB_DISK_MB", 1024); mb > 0 {
		big := filepath.Join(work, "big")
		stop = make(chan struct{})
		wg.Add(1)
		go func() { defer wg.Done(); during = probeUntil(t, work, stop) }()
		start = time.Now()
		out, err = inCgroup(dir, "dd", "if=/dev/zero", "of="+big, "bs=1M", "count="+strconv.Itoa(mb), "conv=fsync").CombinedOutput()
		close(stop)
		wg.Wait()
		_ = os.Remove(big)
		if err != nil {
			t.Fatalf("dd: %v: %s", err, out)
		}
		t.Logf("disk writer: %d MiB in %s", mb, time.Since(start).Round(time.Millisecond))
		t.Logf("writer:    %s", during)
	}
}

// inCgroup is a command that joins dir's cgroup before it execs.
func inCgroup(dir, name string, args ...string) *exec.Cmd {
	script := `echo $$ > "$0/cgroup.procs" && exec "$@"`
	return exec.Command("sh", append([]string{"-c", script, dir, name}, args...)...)
}

type probeStats struct {
	fsync, probe []time.Duration
	failed       int
}

func (p probeStats) String() string {
	q := func(ds []time.Duration) string {
		if len(ds) == 0 {
			return "none"
		}
		s := slices.Clone(ds)
		slices.Sort(s)
		at := func(f float64) time.Duration {
			return s[min(int(f*float64(len(s))), len(s)-1)].Round(100 * time.Microsecond)
		}
		return fmt.Sprintf("n=%d p50 %s p99 %s max %s", len(s), at(0.5), at(0.99), s[len(s)-1].Round(100*time.Microsecond))
	}
	out := "write+fsync " + q(p.fsync)
	if os.Getenv("LAB_PROBE") != "" {
		out += fmt.Sprintf("; %q %s, %d failed", os.Getenv("LAB_PROBE"), q(p.probe), p.failed)
	}
	return out
}

func runProbes(t *testing.T, work string, d time.Duration) probeStats {
	stop := make(chan struct{})
	time.AfterFunc(d, func() { close(stop) })
	return probeUntil(t, work, stop)
}

// probeUntil does what the daemon and Incus do, every 100ms, until stop: a
// small write and fsync, and LAB_PROBE.
func probeUntil(t *testing.T, work string, stop <-chan struct{}) probeStats {
	var p probeStats
	path := filepath.Join(work, "probe")
	for {
		select {
		case <-stop:
			return p
		case <-time.After(100 * time.Millisecond):
		}
		start := time.Now()
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			_, _ = f.WriteString(start.String())
			_ = f.Sync()
			_ = f.Close()
		}
		p.fsync = append(p.fsync, time.Since(start))
		if probe := os.Getenv("LAB_PROBE"); probe != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			start = time.Now()
			if err := exec.CommandContext(ctx, "sh", "-c", probe).Run(); err != nil {
				p.failed++
			}
			p.probe = append(p.probe, time.Since(start))
			cancel()
		}
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
