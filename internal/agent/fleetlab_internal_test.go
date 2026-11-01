//go:build pressurelab

package agent

// The fleet lab: the stress run for pausing whole agents (pressure.Fleet,
// daemon/agentfreeze.go). Several "agents", each a cgroup laid out the way
// Incus lays out a machine, under a parent capped like a small VM with a
// capped swap, each run a program that grows its memory to a size and then
// keeps working through all of it, the way an agent with a dev server, a
// language server and a test run does. They ask to start a few seconds
// apart, in the order they were made. It needs root, cgroup2 and swap:
//
//	sudo -E env PATH=$PATH go test -tags pressurelab -run TestFleetLab -v -timeout 30m ./internal/agent
//
// LAB_MODE is "off" (today: every agent starts as it asks, nothing is
// frozen, which is what memory pressure does to anything that isn't a heavy
// command) or "on" (the fleet: starts on real free memory, freezes newest
// first, thaws oldest first). LAB_AGENTS (6), LAB_MEMORY_MB (the VM, 2048),
// LAB_SWAP_MB (its zram, 1024), LAB_GROW_MB (each agent's memory, 600),
// LAB_RATE (how fast each grows, MiB/s, 5), LAB_PASSES (how many times each
// works through it, 300), LAB_FLOOR_MB (the floor, 200: the daemon's 1.5
// GiB is most of a lab this size) and LAB_BOOT_MB (what a boot takes, 100).
//
// Growth is slow on purpose: an agent's machine swaps to a device a few
// tens of MiB a second fast, and memory growing faster than swap takes it
// is killed by the kernel before any pressure shows, governor or not.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/pressure"
)

// grower allocates mb MiB at rate MiB/s, a third of each page random (zram
// compresses a real agent's memory about that much), then touches every page
// passes times.
const grower = `import os, sys, time
mb, rate, passes = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3])
start = time.time()
chunks = []
while len(chunks) < mb:
    chunks.append(bytearray(b"".join(os.urandom(1365) + bytes(2731) for _ in range(256))))
    ahead = len(chunks) / rate - (time.time() - start)
    if ahead > 0:
        time.sleep(ahead)
for p in range(passes):
    for c in chunks:
        for i in range(0, len(c), 4096):
            c[i] = (c[i] + 1) & 255
`

func labInt(name string, def int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return def
	}
	return n
}

func labReadInt(path string) int64 {
	b, _ := os.ReadFile(path)
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// labMem is the lab VM's memory as pressure.Mem has it: what its cgroup may
// have, what it has left without its caches, and its swap.
func labMem(root string, total, swap int64) pressure.Mem {
	used := labReadInt(filepath.Join(root, "memory.current"))
	stat, _ := os.ReadFile(filepath.Join(root, "memory.stat"))
	for _, line := range strings.Split(string(stat), "\n") {
		key, value, _ := strings.Cut(line, " ")
		if key == "active_file" || key == "inactive_file" {
			n, _ := strconv.ParseInt(value, 10, 64)
			used -= n
		}
	}
	return pressure.Mem{
		Total: total, Available: max(0, total-used),
		SwapTotal: swap, SwapFree: max(0, swap-labReadInt(filepath.Join(root, "memory.swap.current"))),
	}
}

func TestFleetLab(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the lab needs root, to make cgroups")
	}
	mode := labEnv("LAB_MODE", "on")
	agents := labInt("LAB_AGENTS", 6)
	vm := int64(labInt("LAB_MEMORY_MB", 2048)) << 20
	swap := int64(labInt("LAB_SWAP_MB", 1024)) << 20
	grow, rate, passes := labInt("LAB_GROW_MB", 600), labInt("LAB_RATE", 5), labInt("LAB_PASSES", 300)
	script := filepath.Join(t.TempDir(), "grow.py")
	mustWrite(t, script, grower)

	root := filepath.Join(cgroupRoot, "agentbox-fleetlab")
	removeLab(root)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeLab(root) })
	mustWrite(t, filepath.Join(root, "memory.max"), strconv.FormatInt(vm, 10))
	mustWrite(t, filepath.Join(root, "memory.swap.max"), strconv.FormatInt(swap, 10))
	mustWrite(t, filepath.Join(root, "cgroup.subtree_control"), "+memory")
	limit := MemoryLimit(vm)
	t.Logf("mode %s: VM %s with %s swap, %d agents each growing to %d MiB at %d MiB/s then %d passes over it, memory.max %s",
		mode, HumanBytes(vm), HumanBytes(swap), agents, grow, rate, passes, HumanBytes(limit))

	policy := pressure.DefaultPolicy
	policy.Floor = int64(labInt("LAB_FLOOR_MB", 200)) << 20
	f := pressure.NewFleet(policy)
	f.BootCost = int64(labInt("LAB_BOOT_MB", 100)) << 20

	type labAgent struct {
		ref, instance  string
		made           time.Time
		cmd            *exec.Cmd
		done           chan error
		asked, started bool
		finished       bool
		err            error
		start, end     time.Time
		frozenFor      time.Duration
		frozenAt       time.Time
	}
	began := time.Now()
	var all []*labAgent
	for i := range agents {
		a := &labAgent{ref: fmt.Sprintf("lab/%02d", i), instance: fmt.Sprintf("lab-%02d", i), made: began.Add(time.Duration(i) * 3 * time.Second), done: make(chan error, 1)}
		all = append(all, a)
	}
	byRef := map[string]*labAgent{}
	for _, a := range all {
		byRef[a.ref] = a
	}
	boot := func(a *labAgent) {
		machine := agentCgroup(root, a.instance)
		_ = os.Mkdir(machine, 0o755)
		if err := setCgroupLimit(machine, "memory.max", limit, writeCgroup); err != nil {
			t.Fatal(err)
		}
		a.cmd = exec.Command("python3", script, strconv.Itoa(grow), strconv.Itoa(rate), strconv.Itoa(passes))
		a.cmd.Stdout, a.cmd.Stderr = os.Stderr, os.Stderr
		if err := a.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(machine, "cgroup.procs"), strconv.Itoa(a.cmd.Process.Pid))
		a.started, a.start = true, time.Now()
		go func() { a.done <- a.cmd.Wait() }()
	}

	var worstSome, worstFull float64
	var stalled time.Duration // with some pressure at 20% or more
	freezes, thaws, worstFrozen := 0, 0, 0
	tick := 0
	for ; ; tick++ {
		now := time.Now()
		for _, a := range all {
			if a.started && !a.finished {
				select {
				case err := <-a.done:
					a.finished, a.err, a.end = true, err, now
					if a.frozenAt != (time.Time{}) {
						a.frozenFor += now.Sub(a.frozenAt)
						a.frozenAt = time.Time{}
					}
				default:
				}
			}
			if !a.asked && !now.Before(a.made) {
				a.asked = true
				if mode == "off" {
					boot(a)
				} else if f.Ask(a.ref, now) {
					boot(a)
				}
			}
		}
		psi, err := pressure.Read(filepath.Join(root, "memory.pressure"))
		if err != nil {
			t.Fatal(err)
		}
		sample := pressure.Sample{PSI: psi, Mem: labMem(root, vm, swap)}
		worstSome, worstFull = max(worstSome, psi.Some.Avg10), max(worstFull, psi.Full.Avg10)
		if psi.Some.Avg10 >= 20 {
			stalled += 2 * time.Second
		}
		var acts pressure.FleetActions
		if mode != "off" {
			var ms []pressure.Machine
			for _, a := range all {
				if a.started && !a.finished {
					ms = append(ms, pressure.Machine{Agent: a.ref, Since: a.made, Working: true, Frozen: f.Frozen(a.ref)})
				}
			}
			f.Sync(ms)
			acts = f.Step(now, sample)
			for _, ref := range acts.Start {
				boot(byRef[ref])
			}
			for _, ref := range acts.Freeze {
				a := byRef[ref]
				freezes++
				if err := freezeMachine(root, a.instance, true, writeCgroup); err != nil {
					t.Fatal(err)
				}
				a.frozenAt = now
				go func() { _ = reclaimDir(agentCgroup(root, a.instance), writeCgroup) }()
			}
			for _, ref := range acts.Thaw {
				a := byRef[ref]
				thaws++
				if err := freezeMachine(root, a.instance, false, writeCgroup); err != nil {
					t.Fatal(err)
				}
				a.frozenFor += now.Sub(a.frozenAt)
				a.frozenAt = time.Time{}
			}
		}
		running, frozen, waiting, left := 0, 0, 0, 0
		for _, a := range all {
			switch {
			case !a.finished && a.frozenAt != (time.Time{}):
				frozen++
			case a.started && !a.finished:
				running++
			case !a.started:
				waiting++
			}
			if !a.finished {
				left++
			}
		}
		worstFrozen = max(worstFrozen, frozen)
		if tick%5 == 0 || !acts.Empty() {
			t.Logf("%4ds some %5.1f%% full %5.1f%%  avail %-8s swap %3.0f%%  running %d frozen %d waiting %d  %s",
				int(now.Sub(began).Seconds()), psi.Some.Avg10, psi.Full.Avg10, HumanBytes(sample.Mem.Available), 100*sample.Mem.SwapUsed(),
				running, frozen, waiting, fleetActions(acts))
		}
		if left == 0 {
			break
		}
		if now.Sub(began) > 20*time.Minute {
			t.Fatal("still going after 20 minutes")
		}
		time.Sleep(2 * time.Second)
	}
	var kills, done int64
	var total time.Duration
	for _, a := range all {
		c, _ := memoryEvents(agentCgroup(root, a.instance))
		kills += c.Kills
		status := "ok"
		if a.err != nil {
			status = a.err.Error()
		} else {
			done++
		}
		ran := a.end.Sub(a.start)
		total += ran
		t.Logf("%s: waited %s to start, ran %s (frozen %s), %s, %d killed", a.instance,
			a.start.Sub(a.made).Round(time.Second), ran.Round(time.Second), a.frozenFor.Round(time.Second), status, c.Kills)
	}
	t.Logf("RESULT mode=%s agents=%d finished_ok=%d killed=%d all_done_in=%s mean_run=%s worst_some=%.1f%% worst_full=%.1f%% stalled_20pct=%s freezes=%d thaws=%d most_frozen=%d",
		mode, agents, done, kills, time.Since(began).Round(time.Second), (total / time.Duration(agents)).Round(time.Second),
		worstSome, worstFull, stalled, freezes, thaws, worstFrozen)
}

func fleetActions(a pressure.FleetActions) string {
	var parts []string
	for _, r := range a.Start {
		parts = append(parts, "start "+r)
	}
	for _, r := range a.Freeze {
		parts = append(parts, "FREEZE "+r)
	}
	for _, r := range a.Thaw {
		parts = append(parts, "thaw "+r)
	}
	return strings.Join(parts, ", ")
}
